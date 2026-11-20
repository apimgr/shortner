package httpserver

import (
	"bytes"
	"encoding/base64"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/apimgr/shortner/src/config"
)

func TestScaleBrandingImagePreservesAspectAndCachesVariant(t *testing.T) {
	source := image.NewRGBA(image.Rect(0, 0, 100, 50))
	for y := 0; y < 50; y++ {
		for x := 0; x < 100; x++ {
			source.Set(x, y, color.RGBA{R: 255, A: 255})
		}
	}
	var encoded bytes.Buffer
	if err := png.Encode(&encoded, source); err != nil {
		t.Fatal(err)
	}
	sd := &sitemapDeps{cacheDir: t.TempDir()}
	first, err := sd.scaledBrandingImage(encoded.Bytes(), "width=50")
	if err != nil {
		t.Fatalf("scaledBrandingImage() error = %v", err)
	}
	decoded, _, err := image.DecodeConfig(bytes.NewReader(first))
	if err != nil {
		t.Fatal(err)
	}
	if decoded.Width != 50 || decoded.Height != 25 {
		t.Fatalf("scaled dimensions = %dx%d, want 50x25", decoded.Width, decoded.Height)
	}
	second, err := sd.scaledBrandingImage(encoded.Bytes(), "width=50")
	if err != nil || !bytes.Equal(first, second) {
		t.Fatalf("cached scaled image differs or failed: err=%v", err)
	}
}

func TestBrandingConfigWatcherReloadsDropIn(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "server.yml")
	if err := os.WriteFile(configPath, []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(dir, "yaml.d"), 0o700); err != nil {
		t.Fatal(err)
	}
	s := &Server{brandingState: newBrandingStateStore(config.Default("/db"))}
	s.startBrandingConfigWatcher(configPath, "/db")
	dropIn := filepath.Join(dir, "yaml.d", "branding.yml")
	if err := os.WriteFile(dropIn, []byte("server:\n  branding:\n    title: Reloaded Title\nweb:\n  ui:\n    theme: light\n"), 0o600); err != nil {
		s.brandingWatcherCancel()
		<-s.brandingWatcherDone
		t.Fatal(err)
	}
	deadline := time.Now().Add(4 * time.Second)
	for time.Now().Before(deadline) {
		settings := s.brandingState.load(nil)
		if settings.Branding.Title == "Reloaded Title" && settings.Theme == "light" {
			s.brandingWatcherCancel()
			<-s.brandingWatcherDone
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	s.brandingWatcherCancel()
	<-s.brandingWatcherDone
	t.Fatalf("branding drop-in was not reloaded; got %+v", s.brandingState.load(nil))
}

func TestBrandingCacheRejectsMalformedImage(t *testing.T) {
	if err := validateBrandingImage([]byte("not an image"), "image/png"); err == nil {
		t.Fatal("validateBrandingImage() accepted malformed PNG data")
	}
}

func TestDecodeBrandingDataURIValidatesTypeAndSize(t *testing.T) {
	var imageData bytes.Buffer
	if err := png.Encode(&imageData, image.NewRGBA(image.Rect(0, 0, 1, 1))); err != nil {
		t.Fatal(err)
	}
	encoded := base64.StdEncoding.EncodeToString(imageData.Bytes())
	data, contentType, err := decodeBrandingDataURI("data:image/png;base64," + encoded)
	if err != nil || contentType != "image/png" || !bytes.Equal(data, imageData.Bytes()) {
		t.Fatalf("decodeBrandingDataURI() = type %q, err %v", contentType, err)
	}
	for _, invalid := range []string{
		"data:text/html;base64," + base64.StdEncoding.EncodeToString([]byte("<script/>")),
		"data:image/svg+xml;base64," + base64.StdEncoding.EncodeToString([]byte("<!DOCTYPE svg><svg/>")),
	} {
		if _, _, err := decodeBrandingDataURI(invalid); err == nil {
			t.Errorf("decodeBrandingDataURI() accepted %q", invalid[:min(len(invalid), 32)])
		}
	}
}
