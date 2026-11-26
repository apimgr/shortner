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

	"gopkg.in/yaml.v3"
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
	Version          string            `json:"version"`
	CreatedAt        time.Time         `json:"created_at"`
	CreatedBy        string            `json:"created_by"`
	AppVersion       string            `json:"app_version"`
	Contents         []string          `json:"contents"`
	Encrypted        bool              `json:"encrypted"`
	EncryptionMethod string            `json:"encryption_method,omitempty"`
	Type             string            `json:"type"`
	SnapshotAt       time.Time         `json:"snapshot_at"`
	SHA256           string            `json:"sha256"`
	Checksum         string            `json:"-"`
	Kind             Kind              `json:"-"`
	Files            []FileEntry       `json:"files"`
	FileDigests      map[string]string `json:"-"`
	// BaseChecksum is the Checksum of the full backup an incremental was
	// diffed against; empty for full backups.
	BaseChecksum string   `json:"-"`
	BaseSHA256   string   `json:"base_sha256,omitempty"`
	BaseBackup   string   `json:"base_backup,omitempty"`
	Deleted      []string `json:"deleted,omitempty"`
}

// FileEntry records every file's stable path, size, mtime and digest in the
// backup manifest.
type FileEntry struct {
	Path   string `json:"path"`
	Size   int64  `json:"size"`
	MTime  int64  `json:"mtime"`
	SHA256 string `json:"sha256"`
}

type ArchiveLimits struct {
	MaxArchiveSize  int64
	MaxExpandedSize int64
	MaxFiles        int
	MaxFileSize     int64
	MaxRatio        int
}

type expansionUsage struct {
	files      int
	bytes      int64
	compressed int64
}

// UnmarshalJSON accepts older map-shaped manifests while keeping the
// canonical manifest representation as a list of file metadata entries.
func (m *Manifest) UnmarshalJSON(data []byte) error {
	var wire struct {
		Version          string          `json:"version"`
		CreatedAt        time.Time       `json:"created_at"`
		CreatedBy        string          `json:"created_by"`
		AppVersion       string          `json:"app_version"`
		Contents         []string        `json:"contents"`
		Encrypted        bool            `json:"encrypted"`
		EncryptionMethod string          `json:"encryption_method"`
		Type             string          `json:"type"`
		SnapshotAt       time.Time       `json:"snapshot_at"`
		SHA256           string          `json:"sha256"`
		Checksum         string          `json:"checksum"`
		Kind             Kind            `json:"kind"`
		Files            json.RawMessage `json:"files"`
		BaseSHA256       string          `json:"base_sha256"`
		BaseChecksum     string          `json:"base_checksum"`
		BaseBackup       string          `json:"base_backup"`
		Deleted          []string        `json:"deleted"`
	}
	if err := json.Unmarshal(data, &wire); err != nil {
		return err
	}
	var files []FileEntry
	if len(wire.Files) > 0 && string(wire.Files) != "null" {
		if err := json.Unmarshal(wire.Files, &files); err != nil {
			var legacy map[string]string
			if mapErr := json.Unmarshal(wire.Files, &legacy); mapErr != nil {
				return err
			}
			files = make([]FileEntry, 0, len(legacy))
			for name, digest := range legacy {
				files = append(files, FileEntry{Path: name, SHA256: digest})
			}
			sort.Slice(files, func(i, j int) bool { return files[i].Path < files[j].Path })
		}
	}
	m.Version, m.CreatedAt, m.CreatedBy, m.AppVersion = wire.Version, wire.CreatedAt, wire.CreatedBy, wire.AppVersion
	m.Contents, m.Encrypted, m.EncryptionMethod = wire.Contents, wire.Encrypted, wire.EncryptionMethod
	m.Type, m.SnapshotAt, m.SHA256 = wire.Type, wire.SnapshotAt, wire.SHA256
	m.Checksum = wire.Checksum
	m.Kind = wire.Kind
	m.BaseSHA256, m.BaseChecksum = wire.BaseSHA256, wire.BaseChecksum
	m.BaseBackup, m.Deleted = wire.BaseBackup, wire.Deleted
	m.Files = files
	m.FileDigests = make(map[string]string, len(files))
	for _, file := range files {
		m.FileDigests[file.Path] = file.SHA256
	}
	if m.SHA256 == "" {
		m.SHA256 = m.Checksum
	}
	if m.Checksum == "" {
		m.Checksum = m.SHA256
	}
	m.SHA256 = strings.TrimPrefix(m.SHA256, "sha256:")
	if m.Checksum != "" && !strings.HasPrefix(m.Checksum, "sha256:") {
		m.Checksum = "sha256:" + m.Checksum
	}
	if m.BaseSHA256 == "" {
		m.BaseSHA256 = m.BaseChecksum
	}
	if m.BaseChecksum == "" {
		m.BaseChecksum = m.BaseSHA256
	}
	m.BaseSHA256 = strings.TrimPrefix(m.BaseSHA256, "sha256:")
	if m.SnapshotAt.IsZero() {
		m.SnapshotAt = m.CreatedAt
	}
	if m.CreatedAt.IsZero() {
		m.CreatedAt = m.SnapshotAt
	}
	if m.Type == "" {
		if m.Kind.Incremental() {
			m.Type = "incremental"
		} else {
			m.Type = "full"
		}
	}
	if m.Kind == "" {
		if m.Type == "incremental" {
			m.Kind = KindDaily
		} else {
			m.Kind = KindFull
		}
	}
	for name, digest := range m.FileDigests {
		if digest != "" && !strings.HasPrefix(digest, "sha256:") {
			m.FileDigests[name] = "sha256:" + digest
		}
	}
	return nil
}

func (m Manifest) HasFile(name string) bool {
	for _, file := range m.Files {
		if file.Path == name {
			return true
		}
	}
	return false
}

func ReadManifestFromArchive(pathname string) (*Manifest, error) {
	f, err := os.Open(pathname)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return nil, err
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	hdr, err := tr.Next()
	if err != nil {
		return nil, err
	}
	name, err := safeArchiveName(hdr.Name)
	if err != nil || name != ManifestName {
		return nil, fmt.Errorf("backup: manifest is not the first archive entry")
	}
	data, err := readAllLimited(tr, 1<<20)
	if err != nil {
		return nil, err
	}
	var manifest Manifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		return nil, err
	}
	return &manifest, nil
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
	Digest  string
	Content []byte
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
		if required.name == "server.yml" {
			content, err := redactOperatorToken(required.path)
			if err != nil {
				return nil, fmt.Errorf("backup: redact server.token: %w", err)
			}
			f.Content = content
			f.Size = int64(len(content))
			h := sha256.Sum256(content)
			f.Digest = "sha256:" + hex.EncodeToString(h[:])
			files[len(files)-1] = *f
		}
	}
	secretPath := filepath.Join(opts.ConfigDir, "secrets.yml")
	if _, err := os.Lstat(secretPath); err == nil {
		f, err := newSourceFile("secrets.yml", secretPath)
		if err != nil {
			return nil, fmt.Errorf("backup: secrets.yml: %w", err)
		}
		content, err := redactOperatorToken(secretPath)
		if err != nil {
			return nil, fmt.Errorf("backup: redact secrets.yml server.token: %w", err)
		}
		f.Content = content
		f.Size = int64(len(content))
		h := sha256.Sum256(content)
		f.Digest = "sha256:" + hex.EncodeToString(h[:])
		files = append(files, *f)
	} else if !os.IsNotExist(err) {
		return nil, fmt.Errorf("backup: inspect secrets.yml: %w", err)
	}
	if err := collectConnectionTLS(&files, opts); err != nil {
		return nil, err
	}

	dirs := []struct {
		name    string
		root    string
		include bool
	}{
		{"template", filepath.Join(opts.ConfigDir, "template"), true},
		{"theme", filepath.Join(opts.ConfigDir, "theme"), true},
		{"yaml.d", filepath.Join(opts.ConfigDir, "yaml.d"), true},
		{"html", filepath.Join(opts.ConfigDir, "html"), true},
		{"assets", filepath.Join(opts.ConfigDir, "assets"), true},
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

func redactOperatorToken(pathname string) ([]byte, error) {
	data, err := os.ReadFile(pathname)
	if err != nil {
		return nil, err
	}
	var document yaml.Node
	if err := yaml.Unmarshal(data, &document); err != nil {
		return nil, err
	}
	if len(document.Content) == 0 {
		return data, nil
	}
	root := document.Content[0]
	changed := false
	for i := 0; i+1 < len(root.Content); i += 2 {
		if root.Content[i].Value != "server" || root.Content[i+1].Kind != yaml.MappingNode {
			continue
		}
		server := root.Content[i+1]
		for j := 0; j+1 < len(server.Content); j += 2 {
			if server.Content[j].Value == "token" {
				server.Content[j+1].Value = ""
				server.Content[j+1].Tag = "!!str"
				changed = true
			}
		}
	}
	if !changed {
		return data, nil
	}
	var output bytes.Buffer
	if err := yaml.NewEncoder(&output).Encode(&document); err != nil {
		return nil, err
	}
	return output.Bytes(), nil
}

func collectConnectionTLS(files *[]sourceFile, opts Options) error {
	data, err := os.ReadFile(opts.ConfigFile)
	if err != nil {
		return err
	}
	var cfg struct {
		Server struct {
			Cache struct {
				TLSCA   string `yaml:"tls_ca"`
				TLSCert string `yaml:"tls_cert"`
				TLSKey  string `yaml:"tls_key"`
			} `yaml:"cache"`
			SSL struct {
				Cert string `yaml:"cert"`
				Key  string `yaml:"key"`
			} `yaml:"ssl"`
		} `yaml:"server"`
	}
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return fmt.Errorf("backup: parse connection TLS paths: %w", err)
	}
	seen := make(map[string]bool, len(*files))
	for _, file := range *files {
		seen[file.Name] = true
	}
	for _, name := range []string{cfg.Server.Cache.TLSCA, cfg.Server.Cache.TLSCert, cfg.Server.Cache.TLSKey} {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		if filepath.IsAbs(name) {
			return fmt.Errorf("backup: cache TLS path %q must be relative to config_dir/ssl", name)
		}
		archiveName, err := safeArchiveName(path.Join("ssl", name))
		if err != nil || !strings.HasPrefix(archiveName, "ssl/") {
			return fmt.Errorf("backup: invalid cache TLS path %q", name)
		}
		if seen[archiveName] {
			continue
		}
		f, err := newSourceFile(archiveName, filepath.Join(opts.ConfigDir, "ssl", filepath.FromSlash(name)))
		if err != nil {
			return fmt.Errorf("backup: cache TLS file %s: %w", name, err)
		}
		*files = append(*files, *f)
		seen[archiveName] = true
	}
	for _, item := range []struct{ value, label string }{{cfg.Server.SSL.Cert, "cert"}, {cfg.Server.SSL.Key, "key"}} {
		value := strings.TrimSpace(item.value)
		if value == "" {
			continue
		}
		pathValue := value
		if !filepath.IsAbs(pathValue) {
			pathValue = filepath.Join(opts.ConfigDir, pathValue)
		}
		resolved, err := filepath.Abs(pathValue)
		if err != nil {
			return err
		}
		sslRoot, err := filepath.Abs(filepath.Join(opts.ConfigDir, "ssl"))
		if err != nil {
			return err
		}
		rel, relErr := filepath.Rel(sslRoot, resolved)
		archiveName := path.Join("ssl-external", item.label, filepath.Base(value))
		if relErr == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			archiveName = path.Join("ssl", filepath.ToSlash(rel))
		}
		if seen[archiveName] {
			continue
		}
		f, err := newSourceFile(archiveName, pathValue)
		if err != nil {
			return fmt.Errorf("backup: server.ssl.%s file: %w", item.label, err)
		}
		*files = append(*files, *f)
		seen[archiveName] = true
	}
	return nil
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
func hashFile(filePath string) (digest string, resultErr error) {
	f, err := os.Open(filePath)
	if err != nil {
		return "", err
	}
	defer func() {
		if err := f.Close(); err != nil {
			resultErr = errors.Join(resultErr, fmt.Errorf("backup: close %s: %w", filePath, err))
		}
	}()

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

// filesMap renders the per-file digests used for checksum calculation.
func filesMap(files []sourceFile) map[string]string {
	out := make(map[string]string, len(files))
	for _, f := range files {
		out[f.Name] = f.Digest
	}
	return out
}

// fileEntries renders the manifest's canonical per-file metadata list.
func fileEntries(files []sourceFile) []FileEntry {
	out := make([]FileEntry, 0, len(files))
	for _, file := range files {
		out = append(out, FileEntry{Path: file.Name, Size: file.Size, MTime: file.ModTime.Unix(), SHA256: strings.TrimPrefix(file.Digest, "sha256:")})
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
		fmt.Fprintf(h, "%s  %s\n", strings.TrimPrefix(files[name], "sha256:"), name)
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
		var src io.Reader
		var file *os.File
		if f.Content != nil {
			src = bytes.NewReader(f.Content)
		} else {
			file, err = os.Open(f.Path)
			if err != nil {
				return fmt.Errorf("backup: open %s: %w", f.Path, err)
			}
			src = file
		}
		err = writeTarFile(tw, f.Name, f.Mode, f.ModTime, f.Size, src)
		if file != nil {
			if closeErr := file.Close(); closeErr != nil {
				err = errors.Join(err, fmt.Errorf("backup: close %s: %w", f.Path, closeErr))
			}
		}
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
func extractArchive(raw []byte, destDir string) (manifestResult *Manifest, digestsResult map[string]string, resultErr error) {
	return extractArchiveLimited(raw, destDir, ArchiveLimits{}, nil)
}

func extractArchiveLimited(raw []byte, destDir string, limits ArchiveLimits, usage *expansionUsage) (manifestResult *Manifest, digestsResult map[string]string, resultErr error) {
	if usage == nil {
		usage = &expansionUsage{}
	}
	usage.compressed += int64(len(raw))
	gz, err := gzip.NewReader(bytes.NewReader(raw))
	if err != nil {
		return nil, nil, fmt.Errorf("backup: %w: %v", errInvalidFormat, err)
	}
	defer func() {
		if err := gz.Close(); err != nil {
			resultErr = errors.Join(resultErr, fmt.Errorf("backup: close gzip reader: %w", err))
		}
	}()

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
		usage.files++
		if limits.MaxFiles > 0 && usage.files > limits.MaxFiles {
			return nil, nil, fmt.Errorf("backup: archive exceeds maximum file count %d", limits.MaxFiles)
		}
		if limits.MaxFileSize > 0 && hdr.Size > limits.MaxFileSize {
			return nil, nil, fmt.Errorf("backup: file %s exceeds maximum size %d", name, limits.MaxFileSize)
		}
		if limits.MaxExpandedSize > 0 && hdr.Size > limits.MaxExpandedSize-usage.bytes {
			return nil, nil, fmt.Errorf("backup: expanded archive exceeds %d bytes", limits.MaxExpandedSize)
		}

		digest, err := extractOneLimited(tr, name, fs.FileMode(hdr.Mode).Perm(), destDir, limits, usage)
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

type limitedWriter struct {
	writer io.Writer
	limits ArchiveLimits
	usage  *expansionUsage
}

func (w limitedWriter) Write(p []byte) (int, error) {
	if w.limits.MaxExpandedSize > 0 && int64(len(p)) > w.limits.MaxExpandedSize-w.usage.bytes {
		return 0, fmt.Errorf("backup: expanded archive exceeds %d bytes", w.limits.MaxExpandedSize)
	}
	if w.limits.MaxFileSize > 0 && int64(len(p)) > w.limits.MaxFileSize {
		return 0, fmt.Errorf("backup: file exceeds maximum size %d", w.limits.MaxFileSize)
	}
	n, err := w.writer.Write(p)
	w.usage.bytes += int64(n)
	if w.limits.MaxRatio > 0 && w.usage.compressed > 0 && w.usage.bytes > w.usage.compressed*int64(w.limits.MaxRatio) {
		return n, fmt.Errorf("backup: expanded-to-compressed ratio exceeds %d:1", w.limits.MaxRatio)
	}
	return n, err
}

func extractOneLimited(r io.Reader, name string, mode fs.FileMode, destDir string, limits ArchiveLimits, usage *expansionUsage) (digest string, resultErr error) {
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
		defer func() {
			if err := f.Close(); err != nil {
				resultErr = errors.Join(resultErr, fmt.Errorf("backup: close extracted file %s: %w", name, err))
			}
		}()
		w = io.MultiWriter(h, limitedWriter{writer: f, limits: limits, usage: usage})
	} else {
		w = limitedWriter{writer: h, limits: limits, usage: usage}
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
