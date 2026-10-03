package archive

import (
	"archive/tar"
	"bytes"
	"context"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/qzrsa/qzrs-webdav-backup/internal/config"
)

func TestMatchPatterns(t *testing.T) {
	cases := []struct {
		pattern string
		path    string
		want    bool
		why     string
	}{
		// Basename patterns match at any depth.
		{"*.log", "var/log/messages.log", true, "basename wildcard at depth"},
		{"*.log", "etc/config/network", false, "extension mismatch"},
		{"messages*", "var/log/messages", true, "prefix at depth"},
		{"node_modules", "opt/app/node_modules/pkg/index.js", true, "dir segment anywhere"},
		{"node_modules", "opt/app/src/index.js", false, "absent segment"},

		// Anchored patterns.
		{"/etc/config/network", "etc/config/network", true, "anchored exact"},
		{"/etc/config/**", "etc/config/wireless", true, "** covers one segment"},
		{"/etc/config/**", "etc/config/a/b/c", true, "** covers many segments"},
		{"/etc/config/**", "etc/other", false, "anchored prefix mismatch"},
		{"var/log/**/*.log", "var/log/nginx/access.log", true, "** then wildcard"},
		{"var/log/**/*.log", "var/log/access.log", true, "** matches zero segments"},
		{"var/log/**/*.log", "var/log/nginx/access.txt", false, "extension mismatch"},

		// Directory-only patterns cover the subtree.
		{"tmp/", "tmp/anything/here", true, "dir-only matches subtree"},
		{"tmp/", "tmp", false, "dir-only does not match the bare name as a leaf"},

		// Character classes.
		{"data[0-9].bin", "data5.bin", true, "char class hit"},
		{"data[0-9].bin", "dataX.bin", false, "char class miss"},

		// Edge cases.
		{"", "anything", false, "empty pattern"},
		{"**", "a/b/c", true, "bare double star"},
		{"./etc/passwd", "etc/passwd", true, "./ prefix is ignored"},
	}

	for _, c := range cases {
		if got := Match(c.pattern, c.path); got != c.want {
			t.Errorf("Match(%q, %q) = %v, want %v (%s)", c.pattern, c.path, got, c.want, c.why)
		}
	}
}

func TestMatchDoesNotHangOnPathologicalPattern(t *testing.T) {
	// Deeply nested ** must terminate quickly thanks to the depth guard.
	deep := "a/a/a/a/a/a/a/a/a/a/a/a/a/a/a/a/a/a/a/a/a/a/a"
	done := make(chan bool, 1)
	go func() {
		done <- Match("**/z", deep)
	}()
	select {
	case got := <-done:
		if got {
			t.Fatalf("expected no match for %q against **/z", deep)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("pattern matching did not terminate")
	}
}

// buildTree creates a small filesystem fixture and returns its root.
func buildTree(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	mk := func(rel, content string) {
		full := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	mk("etc/config/network", "config interface lan")
	mk("etc/config/wireless", "config wifi-device radio0")
	mk("var/log/messages.log", "log line one\nlog line two\n")
	mk("var/log/messages.log.1", "rotated")
	mk("opt/app/node_modules/pkg/index.js", "module.exports = 1")
	mk("opt/app/src/main.js", "console.log(1)")
	mk("mnt/data/report.pdf", "%PDF-1.4 fake")

	// A symlink, to exercise the link-vs-follow branches.
	if err := os.Symlink("messages.log", filepath.Join(root, "var/log/current")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	return root
}

func TestCreateAndExtractRoundTrip(t *testing.T) {
	root := buildTree(t)
	rel := func(p string) string { return filepath.Join(root, p) }

	var buf bytes.Buffer
	stats, manifest, err := Create(context.Background(), &buf, Options{
		Compression: config.CompressionGzip,
		GzipLevel:   6,
		Roots:       []string{rel("etc"), rel("opt/app/src")},
	}, nil)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if stats.Files == 0 {
		t.Fatal("no files were archived")
	}
	if manifest == nil || manifest.FileCount != stats.Files {
		t.Fatalf("manifest mismatch: %+v vs stats %+v", manifest, stats)
	}
	if buf.Len() == 0 {
		t.Fatal("archive is empty")
	}

	dest := t.TempDir()
	exStats, err := Extract(context.Background(), bytes.NewReader(buf.Bytes()), dest, ExtractOptions{Overwrite: true}, nil)
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if exStats.Files != stats.Files {
		t.Fatalf("extracted %d files, archived %d", exStats.Files, stats.Files)
	}

	// Roots were absolute, so entries carry their path with the leading slash
	// (and any Windows volume) stripped. Rather than reconstructing that name
	// here, walk the result and assert on content.
	found := 0
	walkErr := filepath.WalkDir(dest, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		switch d.Name() {
		case "network":
			data, readErr := os.ReadFile(p)
			if readErr != nil {
				return readErr
			}
			if string(data) != "config interface lan" {
				t.Errorf("content mismatch in %s: %q", p, data)
			}
			found++
		case "main.js":
			found++
		}
		return nil
	})
	if walkErr != nil {
		t.Fatalf("walking extraction result: %v", walkErr)
	}
	if found < 2 {
		t.Fatalf("expected at least 2 known files after extraction, found %d", found)
	}
}

func TestExclusionKeepsEverythingElse(t *testing.T) {
	root := buildTree(t)

	var buf bytes.Buffer
	stats, _, err := Create(context.Background(), &buf, Options{
		Compression:   config.CompressionGzip,
		Roots:         []string{root},
		Exclude:       []string{"*.log", "node_modules", "*.log.1"},
		ExcludeCaches: true,
	}, nil)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	names, err := ListEntries(bytes.NewReader(buf.Bytes()), 1000)
	if err != nil {
		t.Fatalf("ListEntries: %v", err)
	}
	joined := ""
	for _, n := range names {
		joined += n + "\n"
	}
	for _, banned := range []string{"messages.log", "node_modules", "messages.log.1"} {
		if bytes.Contains([]byte(joined), []byte(banned)) {
			t.Errorf("excluded entry %q is present in archive:\n%s", banned, joined)
		}
	}
	for _, want := range []string{"etc/config/network", "opt/app/src/main.js"} {
		if !bytes.Contains([]byte(joined), []byte(want)) {
			t.Errorf("expected %q in archive:\n%s", want, joined)
		}
	}
	if stats.Skipped == 0 {
		t.Error("expected some entries to be skipped")
	}
}

func TestExtractRejectsZipSlip(t *testing.T) {
	// Hand-craft a tarball containing a traversal path.
	var buf bytes.Buffer
	if err := writeRawTar(&buf, "../escaped.txt", "pwned"); err != nil {
		t.Fatal(err)
	}
	dest := t.TempDir()
	_, err := Extract(context.Background(), bytes.NewReader(buf.Bytes()), dest, ExtractOptions{}, nil)
	if err == nil {
		t.Fatal("expected path traversal to be rejected")
	}
	if _, statErr := os.Stat(filepath.Join(filepath.Dir(dest), "escaped.txt")); statErr == nil {
		t.Fatal("traversal file was written outside the destination")
	}
}

func TestExtractDryRunWritesNothing(t *testing.T) {
	root := buildTree(t)
	var buf bytes.Buffer
	if _, _, err := Create(context.Background(), &buf, Options{
		Compression: config.CompressionGzip,
		Roots:       []string{filepath.Join(root, "etc")},
	}, nil); err != nil {
		t.Fatal(err)
	}

	dest := t.TempDir()
	stats, err := Extract(context.Background(), bytes.NewReader(buf.Bytes()), dest, ExtractOptions{DryRun: true}, nil)
	if err != nil {
		t.Fatalf("Extract dry run: %v", err)
	}
	if stats.Files == 0 {
		t.Fatal("dry run reported no files")
	}
	entries, err := os.ReadDir(dest)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("dry run created %d entries in the destination", len(entries))
	}
}

func TestNormaliseAndGzipDetection(t *testing.T) {
	root := buildTree(t)

	// Compression disabled: the archive must still be readable by Extract,
	// which sniffs gzip by magic bytes.
	var buf bytes.Buffer
	if _, _, err := Create(context.Background(), &buf, Options{
		Compression: config.CompressionNone,
		Roots:       []string{filepath.Join(root, "etc")},
	}, nil); err != nil {
		t.Fatal(err)
	}
	if buf.Len() >= 2 && buf.Bytes()[0] == 0x1f && buf.Bytes()[1] == 0x8b {
		t.Fatal("uncompressed archive should not carry a gzip magic header")
	}
	dest := t.TempDir()
	if _, err := Extract(context.Background(), bytes.NewReader(buf.Bytes()), dest, ExtractOptions{}, nil); err != nil {
		t.Fatalf("Extract of uncompressed archive: %v", err)
	}
}

func TestManifestIsReadable(t *testing.T) {
	root := buildTree(t)
	var buf bytes.Buffer
	stats, created, err := Create(context.Background(), &buf, Options{
		Compression: config.CompressionGzip,
		Roots:       []string{filepath.Join(root, "etc")},
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if stats.Files == 0 {
		t.Fatal("test tree produced no files; the assertions below would be vacuous")
	}
	if created.FileCount != stats.Files || created.TotalBytes != stats.TotalBytes {
		t.Fatalf("Create returned stale manifest: %+v (stats files=%d bytes=%d)",
			created, stats.Files, stats.TotalBytes)
	}

	m, err := ReadManifest(bytes.NewReader(buf.Bytes()))
	if err != nil {
		t.Fatalf("ReadManifest: %v", err)
	}
	if m.Tool != "qzrs-webdav-backup" || m.Format != ManifestFormat {
		t.Fatalf("unexpected manifest: %+v", m)
	}
	if len(m.Roots) != 1 {
		t.Fatalf("manifest roots: %v", m.Roots)
	}
	// Regression guard: Create writes a placeholder manifest first and the final
	// one last. Reading the placeholder yields file_count=0 on a non-empty
	// archive, which silently broke archive preview and restore validation.
	if m.FileCount != stats.Files || m.TotalBytes != stats.TotalBytes {
		t.Fatalf("ReadManifest picked up the placeholder: got files=%d bytes=%d, want files=%d bytes=%d",
			m.FileCount, m.TotalBytes, stats.Files, stats.TotalBytes)
	}
	if m.Hostname == "" {
		t.Log("note: hostname was empty in this environment")
	}
}

// writeRawTar emits a single regular file entry with an arbitrary name, which
// lets tests construct archives that the writer would never produce.
func writeRawTar(w io.Writer, name, content string) error {
	tw := tar.NewWriter(w)
	if err := tw.WriteHeader(&tar.Header{
		Name:     name,
		Mode:     0o644,
		Size:     int64(len(content)),
		Typeflag: tar.TypeReg,
	}); err != nil {
		return err
	}
	if _, err := tw.Write([]byte(content)); err != nil {
		return err
	}
	return tw.Close()
}
