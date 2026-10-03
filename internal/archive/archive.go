// Package archive creates and extracts the .tar.gz snapshots used by backup and
// restore. Paths inside an archive are stored relative to the filesystem root
// (leading "/" stripped), so extracting at "/" restores the original layout.
//
// A manifest file named wdb-manifest.json is written as the first entry; it
// makes each archive self-describing without needing external metadata.
package archive

import (
	"archive/tar"
	"compress/gzip"
	"context"
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

	"github.com/qzrsa/qzrs-webdav-backup/internal/config"
)

// ManifestName is the archive's self-describing header entry.
const ManifestName = "wdb-manifest.json"

// ManifestFormat is bumped when the manifest schema changes incompatibly.
const ManifestFormat = 1

// Manifest describes the contents of one archive.
type Manifest struct {
	Format     int       `json:"format"`
	Tool       string    `json:"tool"`
	CreatedAt  time.Time `json:"created_at"`
	Hostname   string    `json:"hostname,omitempty"`
	Roots      []string  `json:"roots"`
	Include    []string  `json:"include,omitempty"`
	Exclude    []string  `json:"exclude,omitempty"`
	FileCount  int       `json:"file_count"`
	TotalBytes int64     `json:"total_bytes"`
	Errors     []string  `json:"errors,omitempty"`
}

// Options controls archive creation.
type Options struct {
	Compression    string
	GzipLevel      int
	Include        []string
	Exclude        []string
	MaxFileSizeMB  int
	FollowSymlinks bool
	OneFileSystem  bool
	ExcludeCaches  bool
	// Roots are the absolute source paths to walk.
	Roots []string
}

// Stats summarises a create or extract operation.
type Stats struct {
	Files      int      `json:"files"`
	Dirs       int      `json:"dirs"`
	Skipped    int      `json:"skipped"`
	TotalBytes int64    `json:"total_bytes"`
	Errors     []string `json:"errors,omitempty"`
}

// Progress reports incremental progress to the caller.
type Progress struct {
	Stage       string `json:"stage"`
	CurrentFile string `json:"current_file,omitempty"`
	Files       int    `json:"files"`
	Bytes       int64  `json:"bytes"`
}

// defaultExcludes are paths that must never be archived: they are virtual
// filesystems, device nodes or volatile runtime state. Backing them up either
// fails outright or produces a useless, potentially enormous archive.
var defaultExcludes = []string{
	"proc", "sys", "dev", "run",
	"tmp/wdb-staging",
}

// cacheExcludes are dropped when ExcludeCaches is on. They are re-creatable and
// usually dominate archive size on a router.
var cacheExcludes = []string{
	".DS_Store", "Thumbs.db", "$RECYCLE.BIN", "System Volume Information",
	"*.swp", "*.swo", "*~", ".Trash", ".Trash-*",
	"node_modules", ".cache", "__pycache__", "*.pyc",
}

type sourceEntry struct {
	absPath string // real path on disk
	arcName string // name inside the archive
	info    fs.FileInfo
}

// Create walks opts.Roots and writes a tar (optionally gzip) stream to dst.
// ctx cancellation aborts between files.
func Create(ctx context.Context, dst io.Writer, opts Options, onProgress func(Progress)) (*Stats, *Manifest, error) {
	stats := &Stats{}
	manifest := &Manifest{
		Format:    ManifestFormat,
		Tool:      "qzrs-webdav-backup",
		CreatedAt: time.Now(),
		Roots:     append([]string{}, opts.Roots...),
		Include:   append([]string{}, opts.Include...),
		Exclude:   append([]string{}, opts.Exclude...),
	}
	if host, err := os.Hostname(); err == nil {
		manifest.Hostname = host
	}

	var gz *gzip.Writer
	var tw *tar.Writer

	switch opts.Compression {
	case config.CompressionNone:
		tw = tar.NewWriter(dst)
	default:
		level := opts.GzipLevel
		if level <= 0 || level > 9 {
			level = 6
		}
		w, err := gzip.NewWriterLevel(dst, level)
		if err != nil {
			return nil, nil, fmt.Errorf("gzip init: %w", err)
		}
		// Keep the original filename out of the header so the archive bytes are
		// reproducible for identical input.
		w.Name = ""
		gz = w
		tw = tar.NewWriter(gz)
	}

	// The manifest is written first with a placeholder; a second pass is not
	// possible on a stream, so we write it again as the final entry with the
	// real statistics. Readers should prefer the last manifest they see.
	if err := writeManifest(tw, manifest); err != nil {
		return nil, nil, err
	}

	for _, root := range opts.Roots {
		if err := ctx.Err(); err != nil {
			return stats, manifest, err
		}
		if err := walkRoot(ctx, root, opts, tw, stats, onProgress); err != nil {
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				return stats, manifest, err
			}
			stats.Errors = append(stats.Errors, fmt.Sprintf("%s: %v", root, err))
		}
	}

	manifest.FileCount = stats.Files
	manifest.TotalBytes = stats.TotalBytes
	manifest.Errors = append([]string{}, stats.Errors...)
	if err := writeManifest(tw, manifest); err != nil {
		return stats, manifest, err
	}

	if err := tw.Close(); err != nil {
		return stats, manifest, fmt.Errorf("close tar: %w", err)
	}
	if gz != nil {
		if err := gz.Close(); err != nil {
			return stats, manifest, fmt.Errorf("close gzip: %w", err)
		}
	}
	return stats, manifest, nil
}

func writeManifest(tw *tar.Writer, m *Manifest) error {
	raw, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal manifest: %w", err)
	}
	raw = append(raw, '\n')
	hdr := &tar.Header{
		Name:     ManifestName,
		Mode:     0o644,
		Size:     int64(len(raw)),
		ModTime:  m.CreatedAt,
		Typeflag: tar.TypeReg,
	}
	if err := tw.WriteHeader(hdr); err != nil {
		return fmt.Errorf("write manifest header: %w", err)
	}
	if _, err := tw.Write(raw); err != nil {
		return fmt.Errorf("write manifest body: %w", err)
	}
	return nil
}

func walkRoot(ctx context.Context, root string, opts Options, tw *tar.Writer, stats *Stats, onProgress func(Progress)) error {
	abs, err := filepath.Abs(root)
	if err != nil {
		return fmt.Errorf("resolve path: %w", err)
	}
	info, err := os.Lstat(abs)
	if err != nil {
		return err
	}

	// A single file given as the root is archived directly.
	if !info.IsDir() {
		entry := sourceEntry{absPath: abs, arcName: archiveName(abs), info: info}
		return addEntry(ctx, tw, entry, opts, stats, onProgress)
	}

	var rootDev uint64
	if opts.OneFileSystem {
		if st, ok := statDevice(info); ok {
			rootDev = st
		}
	}

	return filepath.WalkDir(abs, func(p string, d fs.DirEntry, walkErr error) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if walkErr != nil {
			stats.Errors = append(stats.Errors, fmt.Sprintf("%s: %v", p, walkErr))
			stats.Skipped++
			if d != nil && d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}

		arc := archiveName(p)
		if arc == "" {
			return nil
		}

		// Never archive virtual filesystems.
		if isDefaultExcluded(arc) {
			if d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if matchesAny(arc, opts.Exclude) {
			stats.Skipped++
			if d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if opts.ExcludeCaches && matchesAny(arc, cacheExcludes) {
			stats.Skipped++
			if d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}

		info, err := d.Info()
		if err != nil {
			stats.Errors = append(stats.Errors, fmt.Sprintf("%s: %v", p, err))
			stats.Skipped++
			return nil
		}

		// Do not cross into another mounted filesystem.
		if opts.OneFileSystem && info.IsDir() && p != abs {
			if dev, ok := statDevice(info); ok && dev != rootDev {
				return fs.SkipDir
			}
		}

		// Symlinks are stored as links unless explicitly followed.
		if info.Mode()&os.ModeSymlink != 0 && !opts.FollowSymlinks {
			return addEntry(ctx, tw, sourceEntry{absPath: p, arcName: arc, info: info}, opts, stats, onProgress)
		}
		if info.Mode()&os.ModeSymlink != 0 && opts.FollowSymlinks {
			target, err := filepath.EvalSymlinks(p)
			if err != nil {
				stats.Errors = append(stats.Errors, fmt.Sprintf("%s: dangling symlink", p))
				stats.Skipped++
				return nil
			}
			ti, err := os.Stat(target)
			if err != nil {
				stats.Skipped++
				return nil
			}
			info = ti
		}

		// Device nodes, sockets and FIFOs are not backed up.
		switch {
		case info.Mode()&os.ModeDevice != 0, info.Mode()&os.ModeSocket != 0,
			info.Mode()&os.ModeNamedPipe != 0:
			stats.Skipped++
			return nil
		}

		if info.IsDir() {
			if opts.Include != nil && !includedDir(arc, opts.Include) {
				// A directory that can never contain a match is skipped, but a
				// directory that *might* (wildcards in include) is descended.
				if !couldMatchBelow(arc, opts.Include) {
					stats.Skipped++
					return fs.SkipDir
				}
			}
			return addEntry(ctx, tw, sourceEntry{absPath: p, arcName: arc, info: info}, opts, stats, onProgress)
		}

		if opts.MaxFileSizeMB > 0 && info.Size() > int64(opts.MaxFileSizeMB)<<20 {
			stats.Skipped++
			return nil
		}
		if len(opts.Include) > 0 && !matchesAny(arc, opts.Include) {
			stats.Skipped++
			return nil
		}
		return addEntry(ctx, tw, sourceEntry{absPath: p, arcName: arc, info: info}, opts, stats, onProgress)
	})
}

func addEntry(ctx context.Context, tw *tar.Writer, e sourceEntry, opts Options, stats *Stats, onProgress func(Progress)) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	hdr, err := tar.FileInfoHeader(e.info, "")
	if err != nil {
		stats.Errors = append(stats.Errors, fmt.Sprintf("%s: %v", e.absPath, err))
		stats.Skipped++
		return nil
	}
	hdr.Name = e.arcName
	if e.info.IsDir() {
		hdr.Name = strings.TrimSuffix(hdr.Name, "/") + "/"
	}
	hdr.Format = tar.FormatPAX

	if err := tw.WriteHeader(hdr); err != nil {
		return fmt.Errorf("write header for %s: %w", e.arcName, err)
	}

	if e.info.IsDir() {
		stats.Dirs++
		return nil
	}
	if e.info.Mode()&os.ModeSymlink != 0 {
		if onProgress != nil {
			onProgress(Progress{Stage: "scan", CurrentFile: e.arcName, Files: stats.Files, Bytes: stats.TotalBytes})
		}
		stats.Files++
		return nil
	}

	src, err := os.Open(e.absPath)
	if err != nil {
		stats.Errors = append(stats.Errors, fmt.Sprintf("%s: %v", e.arcName, err))
		stats.Skipped++
		// The header is already written; emit padding to keep the stream valid.
		return padZeros(tw, hdr.Size)
	}
	defer src.Close()

	// If the file shrank since it was stat'ed, pad so the tar framing stays
	// intact instead of producing a corrupt archive.
	n, err := io.CopyN(tw, src, hdr.Size)
	if err != nil && !errors.Is(err, io.EOF) {
		stats.Errors = append(stats.Errors, fmt.Sprintf("%s: %v", e.arcName, err))
	}
	if n < hdr.Size {
		if padErr := padZeros(tw, hdr.Size-n); padErr != nil {
			return padErr
		}
		stats.Errors = append(stats.Errors, fmt.Sprintf("%s: truncated on read", e.arcName))
	}

	stats.Files++
	stats.TotalBytes += hdr.Size
	if onProgress != nil {
		onProgress(Progress{Stage: "scan", CurrentFile: e.arcName, Files: stats.Files, Bytes: stats.TotalBytes})
	}
	return nil
}

func padZeros(tw *tar.Writer, n int64) error {
	if n <= 0 {
		return nil
	}
	buf := make([]byte, 32<<10)
	for n > 0 {
		c := int64(len(buf))
		if c > n {
			c = n
		}
		if _, err := tw.Write(buf[:c]); err != nil {
			return err
		}
		n -= c
	}
	return nil
}

// archiveName converts an on-disk path to its in-archive name.
//
// On Unix "/etc/config" becomes "etc/config", so extracting at "/" restores the
// original layout. On Windows the volume prefix is dropped ("C:\data\x" becomes
// "data/x"), because a drive letter is not a meaningful archive component.
func archiveName(p string) string {
	p = filepath.ToSlash(p)
	if len(p) >= 2 && p[1] == ':' {
		p = p[2:]
	}
	p = strings.TrimPrefix(p, "/")
	// Collapse "./" and trailing slashes.
	for strings.HasPrefix(p, "./") {
		p = strings.TrimPrefix(p, "./")
	}
	p = strings.Trim(p, "/")
	return p
}

func isDefaultExcluded(arc string) bool {
	top := arc
	if i := strings.Index(arc, "/"); i >= 0 {
		top = arc[:i]
	}
	for _, d := range defaultExcludes {
		if top == d {
			return true
		}
	}
	return false
}

// ---------------------------------------------------------------------------
// Pattern matching
// ---------------------------------------------------------------------------

// matchesAny reports whether relPath matches any of the glob patterns.
//
// Patterns without a slash match the basename at any depth (gitignore-style),
// which is what users expect from "*.log" or "node_modules".
func matchesAny(relPath string, patterns []string) bool {
	relPath = strings.TrimPrefix(filepath.ToSlash(relPath), "/")
	if relPath == "" {
		return false
	}
	for _, pat := range patterns {
		if Match(pat, relPath) {
			return true
		}
	}
	return false
}

// Match reports whether relPath matches a single glob pattern.
func Match(pattern, relPath string) bool {
	pat := strings.TrimSpace(strings.ReplaceAll(pattern, "\\", "/"))
	if pat == "" {
		return false
	}
	relPath = strings.TrimPrefix(strings.Trim(relPath, "/"), "./")
	if relPath == "" {
		return false
	}

	// Directory-only patterns ("foo/") match the directory and its subtree.
	dirOnly := strings.HasSuffix(pat, "/")
	pat = strings.TrimSuffix(pat, "/")
	if pat == "" {
		return false
	}
	pat = strings.TrimPrefix(pat, "/")
	for strings.HasPrefix(pat, "./") {
		pat = strings.TrimPrefix(pat, "./")
	}
	if pat == "" {
		return false
	}

	if !strings.Contains(pat, "/") {
		segs := strings.Split(relPath, "/")
		for i, seg := range segs {
			if ok, err := path.Match(pat, seg); err == nil && ok {
				// For a dir-only pattern the match must not be the last
				// segment (that would be a file).
				if dirOnly && i == len(segs)-1 {
					return false
				}
				return true
			}
		}
		return false
	}

	if globMatch(pat, relPath) {
		return true
	}
	return dirOnly && globMatch(pat+"/**", relPath)
}

// globMatch matches a slash-separated pattern against a slash-separated path.
// A "**" segment matches zero or more path segments.
func globMatch(pattern, name string) bool {
	pats := strings.Split(strings.Trim(pattern, "/"), "/")
	segs := strings.Split(strings.Trim(name, "/"), "/")
	return matchSegments(pats, segs, 0)
}

func matchSegments(pats, segs []string, depth int) bool {
	if depth > 64 {
		return false
	}
	if len(pats) == 0 {
		return len(segs) == 0
	}
	p := pats[0]
	if p == "**" {
		for i := 0; i <= len(segs); i++ {
			if matchSegments(pats[1:], segs[i:], depth+1) {
				return true
			}
		}
		return false
	}
	if len(segs) == 0 {
		return false
	}
	ok, err := path.Match(p, segs[0])
	if err != nil || !ok {
		return false
	}
	return matchSegments(pats[1:], segs[1:], depth+1)
}

func includedDir(arc string, include []string) bool {
	if len(include) == 0 {
		return true
	}
	return matchesAny(arc, include)
}

// couldMatchBelow decides whether a directory might contain a future match.
func couldMatchBelow(dir string, include []string) bool {
	if len(include) == 0 {
		return true
	}
	dir = strings.Trim(dir, "/")
	for _, pat := range include {
		pat = strings.TrimPrefix(strings.TrimSpace(pat), "/")
		if pat == "" {
			continue
		}
		base := pat
		if i := strings.IndexAny(pat, "*?["); i >= 0 {
			base = pat[:i]
		}
		base = strings.TrimSuffix(base, "/")
		if base == "" {
			return true
		}
		// Descend when the pattern's literal prefix overlaps this directory.
		if strings.HasPrefix(base, dir+"/") || strings.HasPrefix(dir+"/", base+"/") || dir == base {
			return true
		}
		// A basename-only pattern could match anywhere below.
		if !strings.Contains(pat, "/") {
			return true
		}
	}
	return false
}

// ---------------------------------------------------------------------------
// Extraction
// ---------------------------------------------------------------------------

// ExtractOptions controls extraction behaviour.
type ExtractOptions struct {
	// StripComponents removes leading path elements, mirroring tar(1).
	StripComponents int
	// Overwrite replaces existing files. When false they are skipped.
	Overwrite bool
	// DryRun reports what would happen without touching the filesystem.
	DryRun bool
	// MaxBytes caps the total uncompressed size (0 = unlimited). It guards
	// against a malicious or corrupt archive filling the device.
	MaxBytes int64
}

// Extract unpacks a tar (optionally gzip'd) stream into destDir.
// It transparently detects gzip by magic bytes.
func Extract(ctx context.Context, src io.Reader, destDir string, opts ExtractOptions, onProgress func(Progress)) (*Stats, error) {
	stats := &Stats{}

	br := newPeekReader(src)
	isGzip := false
	if magic, err := br.Peek(2); err == nil && len(magic) == 2 && magic[0] == 0x1f && magic[1] == 0x8b {
		isGzip = true
	}

	var tr *tar.Reader
	if isGzip {
		gz, err := gzip.NewReader(br)
		if err != nil {
			return nil, fmt.Errorf("gzip open: %w", err)
		}
		defer gz.Close()
		tr = tar.NewReader(gz)
	} else {
		tr = tar.NewReader(br)
	}

	destDir = filepath.Clean(destDir)
	if !opts.DryRun {
		if err := os.MkdirAll(destDir, 0o755); err != nil {
			return nil, fmt.Errorf("create destination: %w", err)
		}
	}
	var extracted int64

	for {
		if err := ctx.Err(); err != nil {
			return stats, err
		}
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return stats, fmt.Errorf("read archive: %w", err)
		}

		name := path.Clean(strings.TrimPrefix(filepath.ToSlash(hdr.Name), "/"))
		if name == "." || name == "/" {
			continue
		}
		if name == ManifestName {
			// Consume the manifest so tar framing stays aligned, but do not
			// write it into the destination tree.
			if _, err := io.Copy(io.Discard, tr); err != nil {
				return stats, err
			}
			continue
		}

		if opts.StripComponents > 0 {
			segs := strings.Split(name, "/")
			if len(segs) <= opts.StripComponents {
				if _, err := io.Copy(io.Discard, tr); err != nil {
					return stats, err
				}
				continue
			}
			name = strings.Join(segs[opts.StripComponents:], "/")
		}

		target := filepath.Join(destDir, filepath.FromSlash(name))
		// Zip-slip guard: the resolved path must stay under destDir.
		if !withinDir(destDir, target) {
			return stats, fmt.Errorf("archive contains an unsafe path %q", hdr.Name)
		}

		switch hdr.Typeflag {
		case tar.TypeDir:
			stats.Dirs++
			if !opts.DryRun {
				if err := os.MkdirAll(target, fs.FileMode(hdr.Mode)&fs.ModePerm|0o700); err != nil {
					return stats, fmt.Errorf("create dir %s: %w", name, err)
				}
			}
			continue

		case tar.TypeSymlink:
			stats.Files++
			if opts.DryRun {
				continue
			}
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return stats, err
			}
			// Remove first: symlink() fails if the path exists.
			_ = os.Remove(target)
			if err := os.Symlink(hdr.Linkname, target); err != nil {
				stats.Errors = append(stats.Errors, fmt.Sprintf("%s: symlink: %v", name, err))
			}
			continue

		case tar.TypeReg:
			// handled below

		default:
			// Hardlinks, devices, FIFOs: skip but stay aligned.
			if _, err := io.Copy(io.Discard, tr); err != nil {
				return stats, err
			}
			stats.Skipped++
			continue
		}

		if opts.MaxBytes > 0 && extracted+hdr.Size > opts.MaxBytes {
			return stats, fmt.Errorf("archive exceeds the %d byte extraction limit", opts.MaxBytes)
		}

		stats.Files++
		stats.TotalBytes += hdr.Size
		extracted += hdr.Size
		if onProgress != nil {
			onProgress(Progress{Stage: "extract", CurrentFile: name, Files: stats.Files, Bytes: stats.TotalBytes})
		}
		if opts.DryRun {
			if _, err := io.Copy(io.Discard, tr); err != nil {
				return stats, err
			}
			continue
		}

		if !opts.Overwrite {
			if _, err := os.Lstat(target); err == nil {
				stats.Skipped++
				if _, err := io.Copy(io.Discard, tr); err != nil {
					return stats, err
				}
				continue
			}
		}

		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return stats, fmt.Errorf("create parent of %s: %w", name, err)
		}
		mode := fs.FileMode(hdr.Mode) & fs.ModePerm
		if mode == 0 {
			mode = 0o644
		}
		f, err := os.OpenFile(target, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, mode)
		if err != nil {
			return stats, fmt.Errorf("create %s: %w", name, err)
		}
		_, copyErr := io.Copy(f, tr)
		closeErr := f.Close()
		if copyErr != nil {
			stats.Errors = append(stats.Errors, fmt.Sprintf("%s: %v", name, copyErr))
			continue
		}
		if closeErr != nil {
			stats.Errors = append(stats.Errors, fmt.Sprintf("%s: %v", name, closeErr))
			continue
		}
		if hdr.ModTime.After(time.Unix(0, 0)) {
			_ = os.Chtimes(target, hdr.ModTime, hdr.ModTime)
		}
	}
	return stats, nil
}

// ReadManifest scans a tar stream for its manifest. It is used to validate an
// archive and show what a restore would do before touching the filesystem.
func ReadManifest(src io.Reader) (*Manifest, error) {
	br := newPeekReader(src)
	if magic, err := br.Peek(2); err == nil && len(magic) == 2 && magic[0] == 0x1f && magic[1] == 0x8b {
		gz, err := gzip.NewReader(br)
		if err != nil {
			return nil, fmt.Errorf("gzip open: %w", err)
		}
		defer gz.Close()
		br = newPeekReader(gz)
	}
	tr := tar.NewReader(br)
	// Create 会写两个同名清单：打包前先写占位（tar 无法原地回填），遍历结束
	// 后再写一份带真实统计的。因此必须读完整个流并保留最后一个，否则拿到的
	// 是 file_count=0 / total_bytes=0 的占位值。
	var found *Manifest
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, err
		}
		if path.Base(strings.TrimPrefix(hdr.Name, "/")) != ManifestName {
			continue
		}
		var m Manifest
		if err := json.NewDecoder(io.LimitReader(tr, 1<<20)).Decode(&m); err != nil {
			return nil, fmt.Errorf("parse manifest: %w", err)
		}
		found = &m
	}
	if found == nil {
		return nil, errors.New("archive has no manifest")
	}
	return found, nil
}

// ListEntries returns the first n file names inside an archive (for previews).
func ListEntries(src io.Reader, limit int) ([]string, error) {
	br := newPeekReader(src)
	if magic, err := br.Peek(2); err == nil && len(magic) == 2 && magic[0] == 0x1f && magic[1] == 0x8b {
		gz, err := gzip.NewReader(br)
		if err != nil {
			return nil, err
		}
		defer gz.Close()
		br = newPeekReader(gz)
	}
	tr := tar.NewReader(br)
	var out []string
	for len(out) < limit {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return out, err
		}
		if path.Base(strings.TrimPrefix(hdr.Name, "/")) == ManifestName {
			continue
		}
		out = append(out, hdr.Name)
	}
	sort.Strings(out)
	return out, nil
}

func withinDir(dir, target string) bool {
	rel, err := filepath.Rel(dir, target)
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}
