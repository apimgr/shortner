package server

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"encoding/xml"
	"errors"
	"fmt"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	"image/png"
	"io"
	"mime"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	xdraw "golang.org/x/image/draw"
	"golang.org/x/image/webp"

	"github.com/apimgr/shortner/src/common/urlutil"
)

const brandingRefreshInterval = 24 * time.Hour

func (sd *sitemapDeps) remoteBrandingImage(ctx context.Context, rawURL string) ([]byte, string, time.Time, error) {
	sd.brandMu.Lock()
	defer sd.brandMu.Unlock()
	if sd.cacheDir == "" {
		return nil, "", time.Time{}, fmt.Errorf("branding cache directory is unavailable")
	}
	dir := filepath.Join(sd.cacheDir, "branding-images")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, "", time.Time{}, err
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		return nil, "", time.Time{}, err
	}
	hash := sha256.Sum256([]byte(rawURL))
	path := filepath.Join(dir, hex.EncodeToString(hash[:])+".cache")
	if data, contentType, modified, err := readCachedBrandingImage(path); err == nil && time.Since(modified) < brandingRefreshInterval {
		return data, contentType, modified, nil
	}
	fetchConfig := urlutil.DefaultFetchRemoteImageConfig()
	fetchCtx, cancel := context.WithTimeout(ctx, fetchConfig.Timeout)
	defer cancel()
	data, contentType, err := urlutil.FetchRemoteImage(fetchCtx, rawURL, fetchConfig)
	if err != nil {
		if stale, staleType, modified, readErr := readCachedBrandingImage(path); readErr == nil {
			return stale, staleType, modified, nil
		}
		return nil, "", time.Time{}, err
	}
	if err := validateBrandingImage(data, contentType); err != nil {
		return nil, "", time.Time{}, err
	}
	cacheData := append([]byte(contentType+"\n"), data...)
	tmp, err := os.CreateTemp(dir, ".branding-*.tmp")
	if err != nil {
		return nil, "", time.Time{}, err
	}
	tmpPath := tmp.Name()
	defer func() { _ = os.Remove(tmpPath) }()
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return nil, "", time.Time{}, err
	}
	if _, err := tmp.Write(cacheData); err != nil {
		_ = tmp.Close()
		return nil, "", time.Time{}, err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return nil, "", time.Time{}, err
	}
	if err := tmp.Close(); err != nil {
		return nil, "", time.Time{}, err
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return nil, "", time.Time{}, err
	}
	info, err := os.Stat(path)
	if err != nil {
		return nil, "", time.Time{}, err
	}
	return data, contentType, info.ModTime(), nil
}

func (sd *sitemapDeps) scaledBrandingImage(source []byte, variant string) ([]byte, error) {
	if sd.cacheDir == "" {
		return scaleBrandingImage(source, variant)
	}
	sourceHash := sha256.Sum256(source)
	key := sha256.Sum256([]byte(hex.EncodeToString(sourceHash[:]) + ":" + variant))
	dir := filepath.Join(sd.cacheDir, "branding-images")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	path := filepath.Join(dir, hex.EncodeToString(key[:])+".png")
	if cached, err := os.ReadFile(path); err == nil {
		return cached, nil
	}
	result, err := scaleBrandingImage(source, variant)
	if err != nil {
		return nil, err
	}
	tmp, err := os.CreateTemp(dir, ".scaled-branding-*.tmp")
	if err != nil {
		return nil, err
	}
	tmpPath := tmp.Name()
	defer func() { _ = os.Remove(tmpPath) }()
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return nil, err
	}
	if _, err := tmp.Write(result); err != nil {
		_ = tmp.Close()
		return nil, err
	}
	if err := tmp.Close(); err != nil {
		return nil, err
	}
	if err := os.Rename(tmpPath, path); err != nil {
		if cached, readErr := os.ReadFile(path); readErr == nil {
			return cached, nil
		}
		return nil, err
	}
	return result, nil
}

func scaleBrandingImage(source []byte, variant string) ([]byte, error) {
	src, _, err := image.Decode(bytes.NewReader(source))
	if err != nil {
		if webpImage, webpErr := webp.Decode(bytes.NewReader(source)); webpErr == nil {
			src, err = webpImage, nil
		}
	}
	if err != nil {
		return nil, err
	}
	var box image.Point
	switch variant {
	case "width=50":
		box = image.Pt(50, max(1, src.Bounds().Dy()*50/src.Bounds().Dx()))
	case "width=200":
		box = image.Pt(200, max(1, src.Bounds().Dy()*200/src.Bounds().Dx()))
	case "size=1200x630":
		box = image.Pt(1200, 630)
	default:
		if !strings.HasPrefix(variant, "size=") {
			return nil, fmt.Errorf("unsupported image variant")
		}
		size, err := strconv.Atoi(strings.TrimPrefix(variant, "size="))
		if err != nil || (size != 16 && size != 32 && size != 48 && size != 180 && size != 192 && size != 512) {
			return nil, fmt.Errorf("unsupported image size")
		}
		box = image.Pt(size, size)
	}
	canvas := image.NewRGBA(image.Rect(0, 0, box.X, box.Y))
	if variant == "size=1200x630" {
		bounds := src.Bounds()
		sourceRatio := float64(bounds.Dx()) / float64(bounds.Dy())
		targetRatio := float64(box.X) / float64(box.Y)
		crop := bounds
		if sourceRatio > targetRatio {
			width := int(float64(bounds.Dy())*targetRatio + 0.5)
			left := bounds.Min.X + (bounds.Dx()-width)/2
			crop = image.Rect(left, bounds.Min.Y, left+width, bounds.Max.Y)
		} else if sourceRatio < targetRatio {
			height := int(float64(bounds.Dx())/targetRatio + 0.5)
			top := bounds.Min.Y + (bounds.Dy()-height)/2
			crop = image.Rect(bounds.Min.X, top, bounds.Max.X, top+height)
		}
		xdraw.CatmullRom.Scale(canvas, canvas.Bounds(), src, crop, xdraw.Over, nil)
	} else {
		bounds := src.Bounds()
		scale := min(float64(box.X)/float64(bounds.Dx()), float64(box.Y)/float64(bounds.Dy()))
		w, h := max(1, int(float64(bounds.Dx())*scale)), max(1, int(float64(bounds.Dy())*scale))
		dst := image.Rect((box.X-w)/2, (box.Y-h)/2, (box.X-w)/2+w, (box.Y-h)/2+h)
		xdraw.CatmullRom.Scale(canvas, dst, src, bounds, xdraw.Over, nil)
	}
	var out bytes.Buffer
	if err := png.Encode(&out, canvas); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

func readCachedBrandingImage(path string) ([]byte, string, time.Time, error) {
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() || info.Size() > (10<<20)+64 {
		return nil, "", time.Time{}, fmt.Errorf("invalid cached image")
	}
	content, err := os.ReadFile(path)
	if err != nil {
		return nil, "", time.Time{}, err
	}
	header, data, ok := bytes.Cut(content, []byte("\n"))
	if !ok || len(data) == 0 {
		return nil, "", time.Time{}, fmt.Errorf("invalid cached image")
	}
	contentType, _, err := mime.ParseMediaType(string(header))
	if err != nil || !validBrandingContentType(contentType) {
		return nil, "", time.Time{}, fmt.Errorf("invalid cached image type")
	}
	return data, contentType, info.ModTime(), nil
}

func validateBrandingImage(data []byte, contentType string) error {
	if !validBrandingContentType(contentType) || len(data) == 0 {
		return fmt.Errorf("unsupported remote image type")
	}
	if contentType == "image/x-icon" {
		width, height, err := icoDimensions(data)
		if err != nil || width > 8192 || height > 8192 {
			return fmt.Errorf("invalid icon image")
		}
		return nil
	}
	config, format, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil && contentType == "image/webp" {
		config, err = webp.DecodeConfig(bytes.NewReader(data))
		format = "webp"
	}
	if err != nil {
		return fmt.Errorf("invalid image data")
	}
	if config.Width < 1 || config.Height < 1 || config.Width > 8192 || config.Height > 8192 || int64(config.Width)*int64(config.Height) > 32_000_000 {
		return fmt.Errorf("image dimensions exceed limits")
	}
	expected := map[string]string{"image/png": "png", "image/jpeg": "jpeg", "image/gif": "gif", "image/webp": "webp"}[contentType]
	if expected != "" && format != expected {
		return fmt.Errorf("image content does not match its media type")
	}
	return nil
}

func validBrandingContentType(contentType string) bool {
	switch strings.ToLower(contentType) {
	case "image/png", "image/jpeg", "image/gif", "image/webp", "image/x-icon", "image/svg+xml":
		return true
	default:
		return false
	}
}

func decodeBrandingDataURI(raw string) ([]byte, string, error) {
	if !strings.HasPrefix(strings.ToLower(raw), "data:") {
		return nil, "", fmt.Errorf("not a data URI")
	}
	metadata, encoded, ok := strings.Cut(raw[len("data:"):], ",")
	if !ok || len(encoded) > 400*1024 {
		return nil, "", fmt.Errorf("invalid branding data URI")
	}
	base64Encoded := strings.HasSuffix(strings.ToLower(metadata), ";base64")
	if base64Encoded {
		metadata = metadata[:len(metadata)-len(";base64")]
	}
	contentType, _, err := mime.ParseMediaType(metadata)
	if err != nil || !validBrandingContentType(contentType) {
		return nil, "", fmt.Errorf("unsupported branding data URI type")
	}
	var data []byte
	if base64Encoded {
		data, err = base64.StdEncoding.DecodeString(encoded)
	} else {
		var decoded string
		decoded, err = url.PathUnescape(encoded)
		data = []byte(decoded)
	}
	if err != nil || len(data) == 0 || len(data) > 256*1024 {
		return nil, "", fmt.Errorf("invalid branding data URI payload")
	}
	if contentType == "image/svg+xml" {
		if err := validateSVGImage(data); err != nil {
			return nil, "", err
		}
	} else if err := validateBrandingImage(data, contentType); err != nil {
		return nil, "", err
	}
	return data, contentType, nil
}

func validateSVGImage(data []byte) error {
	if bytes.Contains(bytes.ToLower(data), []byte("<!doctype")) || bytes.Contains(bytes.ToLower(data), []byte("<!entity")) {
		return fmt.Errorf("SVG declarations are not allowed")
	}
	decoder := xml.NewDecoder(bytes.NewReader(data))
	decoder.Strict = true
	rootSeen := false
	for {
		token, err := decoder.Token()
		if err != nil {
			if errors.Is(err, io.EOF) && rootSeen {
				return nil
			}
			return fmt.Errorf("invalid SVG data")
		}
		if root, ok := token.(xml.StartElement); ok && !rootSeen {
			if root.Name.Local != "svg" {
				return fmt.Errorf("SVG root element is missing")
			}
			rootSeen = true
		}
	}
}

func icoDimensions(data []byte) (int, int, error) {
	if len(data) < 22 || data[0] != 0 || data[1] != 0 || data[2] != 1 || data[3] != 0 {
		return 0, 0, fmt.Errorf("invalid ICO header")
	}
	count := int(binary.LittleEndian.Uint16(data[4:6]))
	if count < 1 || len(data) < 6+count*16 {
		return 0, 0, fmt.Errorf("invalid ICO directory")
	}
	width, height := int(data[6]), int(data[7])
	if width == 0 {
		width = 256
	}
	if height == 0 {
		height = 256
	}
	size := int(binary.LittleEndian.Uint32(data[14:18]))
	offset := int(binary.LittleEndian.Uint32(data[18:22]))
	if size < 1 || offset < 6+count*16 || offset+size > len(data) {
		return 0, 0, fmt.Errorf("invalid ICO image data")
	}
	return width, height, nil
}
