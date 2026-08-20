package backup

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// ManifestName is the archive-relative path of the manifest, per AI.md
// PART 21 "Manifest (`manifest.json`)".
const ManifestName = "manifest.json"

// ManifestVersion is the backup-format version recorded in every manifest,
// per AI.md PART 21's manifest example ("version": "1.0.0").
const ManifestVersion = "1.0.0"

// maxArchiveBytes bounds how much a single archive may expand to during
// in-memory verification/decryption (8 GiB). Archives larger than this are
// rejected rather than allowed to exhaust the host's memory.
const maxArchiveBytes = 8 << 30

// errUnsafeEntry marks a path-traversal rejection and errInvalidFormat a
// structurally unreadable archive, so Verify can attribute each to the
// right AI.md PART 21 check.
var (
	errUnsafeEntry   = errors.New("unsafe archive entry")
	errInvalidFormat = errors.New("invalid archive format")
)

// Kind is the role a backup archive plays in AI.md PART 21 "Backup Files
// Created (Single Task at 02:00)".
type Kind string

// Backup kinds. KindFull and KindManual are both full archives — they
// differ only in filename shape (dated vs. dated+timestamped) and both
// count toward `max_backups`. KindDaily/KindHourly are the always-exactly-
// one incrementals, which retention never prunes.
const (
	KindFull   Kind = "full"
	KindManual Kind = "manual"
	KindDaily  Kind = "daily"
	KindHourly Kind = "hourly"
)

// Incremental reports whether k is one of the replace-in-place incremental
// archives.
func (k Kind) Incremental() bool { return k == KindDaily || k == KindHourly }

// Manifest is the `manifest.json` embedded in every archive, per AI.md
// PART 21 "Manifest".
//
// Kind, Files, and BaseChecksum extend the spec's illustrated shape
// because PART 21's own required behavior cannot be implemented without
// them: "Checksum valid — SHA-256 matches manifest" needs per-file digests
// to check against, and "Daily incremental (changes since full)" needs the
// base full backup's digests to diff against.
type Manifest struct {
	Version          string    `json:"version"`
	CreatedAt        time.Time `json:"created_at"`
	CreatedBy        string    `json:"created_by"`
	AppVersion       string    `json:"app_version"`
	Contents         []string  `json:"contents"`
	Encrypted        bool      `json:"encrypted"`
	EncryptionMethod string    `json:"encryption_method,omitempty"`
	Checksum         string    `json:"checksum"`
	Kind             Kind      `json:"kind"`
	// Files maps each archive-relative file path to its "sha256:<hex>"
	// digest.
	Files map[string]string `json:"files"`
	// BaseChecksum is the Checksum of the full backup an incremental was
	// diffed against; empty for full backups.
	BaseChecksum string `json:"base_checksum,omitempty"`
}

// sourceFile is one file selected for inclusion in an archive.
type sourceFile struct {
	// Name is the archive-relative path (always slash-separated).
	Name    string
	Path    string
	Mode    fs.FileMode
	ModTime time.Time
	Size    int64
	// Digest is "sha256:<hex>" of the file's contents.
	Digest string
}

// collectSources resolves opts into the concrete file list AI.md PART 21
// "Backup Contents" prescribes, hashing each file as it goes. Missing
// optional sources are skipped silently ("If exists"); a missing
// server.yml or server.db is an error, since both are "Always" included.
func collectSources(opts Options) ([]sourceFile, error) {
	var files []sourceFile

	for _, required := range []struct{ name, path string }{
		{"server.yml", opts.ConfigFile},
		{"server.db", opts.DBPath},
	} {
		f, err := newSourceFile(required.name, required.path)
		if err != nil {
			return nil, fmt.Errorf("backup: %s: %w", required.name, err)
		}
		files = append(files, *f)
	}

	dirs := []struct {
		name    string
		root    string
		include bool
	}{
		{"template", filepath.Join(opts.ConfigDir, "template"), true},
		{"theme", filepath.Join(opts.ConfigDir, "theme"), true},
		{"ssl", filepath.Join(opts.ConfigDir, "ssl"), opts.IncludeSSL},
		{"data", opts.DataDir, opts.IncludeData},
	}
	for _, d := range dirs {
		if !d.include || d.root == "" {
			continue
		}
		collected, err := collectDir(d.name, d.root, opts.excludedRoots())
		if err != nil {
			return nil, err
		}
		files = append(files, collected...)
	}

	sort.Slice(files, func(i, j int) bool { return files[i].Name < files[j].Name })
	return files, nil
}

// excludedRoots lists directories that must never be walked into when
// --include-data is set: the backup directory (a backup of the backups
// grows without bound) and the database directory (server.db is already
// captured separately, and copying a live SQLite file mid-walk would
// capture a torn page).
func (o Options) excludedRoots() []string {
	var roots []string
	for _, r := range []string{o.Dir, filepath.Dir(o.DBPath)} {
		if r == "" {
			continue
		}
		if abs, err := filepath.Abs(r); err == nil {
			roots = append(roots, abs)
		}
	}
	return roots
}

// newSourceFile stats and hashes one file.
func newSourceFile(name, filePath string) (*sourceFile, error) {
	info, err := os.Stat(filePath)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("not a regular file")
	}
	digest, err := hashFile(filePath)
	if err != nil {
		return nil, err
	}
	return &sourceFile{
		Name:    name,
		Path:    filePath,
		Mode:    info.Mode().Perm(),
		ModTime: info.ModTime(),
		Size:    info.Size(),
		Digest:  digest,
	}, nil
}

// collectDir walks root, returning every regular file below it as an
// archive entry rooted at name. A missing root is not an error — AI.md
// PART 21 marks these sources "If exists". Symlinks are skipped: following
// them could pull in arbitrary files outside the configured directories.
func collectDir(name, root string, excluded []string) ([]sourceFile, error) {
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf("backup: resolve %s: %w", root, err)
	}
	if _, err := os.Stat(absRoot); err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("backup: stat %s: %w", root, err)
	}

	var files []sourceFile
	walkErr := filepath.WalkDir(absRoot, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		for _, ex := range excluded {
			if p == ex {
				return fs.SkipDir
			}
		}
		if d.IsDir() || !d.Type().IsRegular() {
			return nil
		}
		rel, err := filepath.Rel(absRoot, p)
		if err != nil {
			return err
		}
		f, err := newSourceFile(path.Join(name, filepath.ToSlash(rel)), p)
		if err != nil {
			return err
		}
		files = append(files, *f)
		return nil
	})
	if walkErr != nil {
		return nil, fmt.Errorf("backup: collect %s: %w", root, walkErr)
	}
	return files, nil
}

// hashFile returns "sha256:<hex>" for the contents of filePath.
func hashFile(filePath string) (string, error) {
	f, err := os.Open(filePath)
	if err != nil {
		return "", err
	}
	defer f.Close()

	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return "sha256:" + hex.EncodeToString(h.Sum(nil)), nil
}

// contentsOf renders the manifest's `contents` list from the selected
// files: top-level filenames verbatim, directories as "name/", in the
// order AI.md PART 21's manifest example shows them.
func contentsOf(files []sourceFile) []string {
	seen := map[string]bool{}
	var out []string
	for _, f := range files {
		entry := f.Name
		if i := strings.Index(entry, "/"); i >= 0 {
			entry = entry[:i+1]
		}
		if seen[entry] {
			continue
		}
		seen[entry] = true
		out = append(out, entry)
	}
	return out
}

// filesMap renders the manifest's per-file digest map.
func filesMap(files []sourceFile) map[string]string {
	out := make(map[string]string, len(files))
	for _, f := range files {
		out[f.Name] = f.Digest
	}
	return out
}

// checksumOf computes the manifest's `checksum` — a SHA-256 over every
// archived file's name and content digest, in sorted order. It is
// deliberately computed over the payload rather than over the finished
// archive file: the manifest that carries the checksum lives inside the
// archive, so an archive-level self-checksum is impossible, and a digest
// of the contents is what actually detects corruption of the restored
// data.
func checksumOf(files map[string]string) string {
	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}
	sort.Strings(names)

	h := sha256.New()
	for _, name := range names {
		fmt.Fprintf(h, "%s\x00%s\n", name, files[name])
	}
	return "sha256:" + hex.EncodeToString(h.Sum(nil))
}

// writeArchive writes a gzip-compressed tar of m plus files to w. The
// manifest is written first so a reader can learn what to expect before
// streaming the payload.
func writeArchive(w io.Writer, m Manifest, files []sourceFile) error {
	gz := gzip.NewWriter(w)
	tw := tar.NewWriter(gz)

	manifestJSON, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return fmt.Errorf("backup: marshal manifest: %w", err)
	}
	manifestJSON = append(manifestJSON, '\n')
	if err := writeTarFile(tw, ManifestName, 0o600, m.CreatedAt, int64(len(manifestJSON)), bytes.NewReader(manifestJSON)); err != nil {
		return err
	}

	for _, f := range files {
		src, err := os.Open(f.Path)
		if err != nil {
			return fmt.Errorf("backup: open %s: %w", f.Path, err)
		}
		err = writeTarFile(tw, f.Name, f.Mode, f.ModTime, f.Size, src)
		src.Close()
		if err != nil {
			return err
		}
	}

	if err := tw.Close(); err != nil {
		return fmt.Errorf("backup: close tar: %w", err)
	}
	if err := gz.Close(); err != nil {
		return fmt.Errorf("backup: close gzip: %w", err)
	}
	return nil
}

// writeTarFile appends one regular-file entry to tw. Ownership fields are
// deliberately zeroed: a backup archive is an unauthenticated artifact
// that may leave the host, and the operator's uid/gid/username is Tier 1
// data under AI.md PART 11.
func writeTarFile(tw *tar.Writer, name string, mode fs.FileMode, modTime time.Time, size int64, r io.Reader) error {
	hdr := &tar.Header{
		Typeflag: tar.TypeReg,
		Name:     name,
		Mode:     int64(mode.Perm()),
		Size:     size,
		ModTime:  modTime.UTC().Truncate(time.Second),
		Format:   tar.FormatPAX,
	}
	if err := tw.WriteHeader(hdr); err != nil {
		return fmt.Errorf("backup: write tar header %s: %w", name, err)
	}
	written, err := io.Copy(tw, io.LimitReader(r, size))
	if err != nil {
		return fmt.Errorf("backup: write %s: %w", name, err)
	}
	if written != size {
		return fmt.Errorf("backup: %s changed size while archiving", name)
	}
	return nil
}

// extractArchive decompresses raw (a plain .tar.gz byte slice) into
// destDir, returning the manifest and the digest of every extracted file.
// destDir may be empty, in which case entries are hashed and discarded —
// that is the "test extract" mode AI.md PART 21 "Verification" requires.
func extractArchive(raw []byte, destDir string) (*Manifest, map[string]string, error) {
	gz, err := gzip.NewReader(bytes.NewReader(raw))
	if err != nil {
		return nil, nil, fmt.Errorf("backup: %w: %v", errInvalidFormat, err)
	}
	defer gz.Close()

	tr := tar.NewReader(gz)
	var manifest *Manifest
	digests := map[string]string{}

	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, nil, fmt.Errorf("backup: read archive: %w", err)
		}
		if hdr.Typeflag != tar.TypeReg {
			continue
		}
		name, err := safeArchiveName(hdr.Name)
		if err != nil {
			return nil, nil, err
		}

		if name == ManifestName {
			data, err := readAllLimited(tr, 1<<20)
			if err != nil {
				return nil, nil, fmt.Errorf("backup: read manifest: %w", err)
			}
			var m Manifest
			if err := json.Unmarshal(data, &m); err != nil {
				return nil, nil, fmt.Errorf("backup: parse manifest: %w", err)
			}
			manifest = &m
			continue
		}

		digest, err := extractOne(tr, name, fs.FileMode(hdr.Mode).Perm(), destDir)
		if err != nil {
			return nil, nil, err
		}
		digests[name] = digest
	}

	if manifest == nil {
		return nil, nil, fmt.Errorf("backup: archive has no %s", ManifestName)
	}
	return manifest, digests, nil
}

// extractOne hashes one archive entry, writing it under destDir when
// destDir is non-empty.
func extractOne(r io.Reader, name string, mode fs.FileMode, destDir string) (string, error) {
	h := sha256.New()
	var w io.Writer = h

	if destDir != "" {
		target := filepath.Join(destDir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(target), 0o750); err != nil {
			return "", fmt.Errorf("backup: create dir for %s: %w", name, err)
		}
		if mode == 0 {
			mode = 0o600
		}
		f, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode)
		if err != nil {
			return "", fmt.Errorf("backup: create %s: %w", name, err)
		}
		defer f.Close()
		w = io.MultiWriter(h, f)
	}

	if _, err := io.Copy(w, io.LimitReader(r, maxArchiveBytes)); err != nil {
		return "", fmt.Errorf("backup: extract %s: %w", name, err)
	}
	return "sha256:" + hex.EncodeToString(h.Sum(nil)), nil
}

// safeArchiveName rejects archive entry names that would escape the
// extraction directory (absolute paths, "..", drive letters, backslashes).
// An archive is untrusted input — it may have been produced anywhere.
func safeArchiveName(name string) (string, error) {
	clean := path.Clean(strings.ReplaceAll(name, "\\", "/"))
	if clean == "." || clean == "/" || path.IsAbs(clean) || strings.HasPrefix(clean, "../") || clean == ".." {
		return "", fmt.Errorf("backup: %w %q", errUnsafeEntry, name)
	}
	if filepath.VolumeName(clean) != "" {
		return "", fmt.Errorf("backup: %w %q", errUnsafeEntry, name)
	}
	return clean, nil
}
