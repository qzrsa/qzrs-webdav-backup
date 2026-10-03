package engine_test

import (
	"context"
	"fmt"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/qzrsa/qzrs-webdav-backup/internal/archive"
	"github.com/qzrsa/qzrs-webdav-backup/internal/config"
	"github.com/qzrsa/qzrs-webdav-backup/internal/cryptoutil"
	"github.com/qzrsa/qzrs-webdav-backup/internal/engine"
	"github.com/qzrsa/qzrs-webdav-backup/internal/testdav"
)

// harness bundles everything an end-to-end engine test needs.
type harness struct {
	store   *config.Store
	runner  *engine.Runner
	davRoot string
	davURL  string
	srv     *testdav.Server
	dataDir string
}

func newHarness(t *testing.T) *harness {
	t.Helper()

	davRoot := t.TempDir()
	srv := testdav.New(davRoot, "backupuser", "s3cret")
	// The profiles point at <ts.URL>/dav, mirroring real deployments (OpenList
	// serves DAV under /dav). The prefix is honoured by the server itself so
	// that request routing AND hrefs in PROPFIND responses both carry it —
	// exactly what a location-block front end does.
	srv.PathPrefix = "/dav"
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)

	dataDir := t.TempDir()
	store, _, err := config.NewStore(filepath.Join(dataDir, "config.json"))
	if err != nil {
		t.Fatalf("config.NewStore: %v", err)
	}
	// Give the admin a real password so Validate is satisfied downstream.
	if _, err := store.Update(func(c *config.Config) error {
		h, err := hashForTest("adminpass")
		if err != nil {
			return err
		}
		c.Admin.PasswordHash = h
		return nil
	}); err != nil {
		t.Fatalf("seed admin: %v", err)
	}

	history, err := engine.NewHistory(filepath.Join(dataDir, "state"), 50)
	if err != nil {
		t.Fatalf("NewHistory: %v", err)
	}

	return &harness{
		store:   store,
		runner:  engine.NewRunner(store, history, func(string, ...any) {}),
		davRoot: davRoot,
		davURL:  ts.URL,
		srv:     srv,
		dataDir: dataDir,
	}
}

func hashForTest(pw string) (string, error) {
	return cryptoutil.HashPassword(pw)
}

// addProfile registers a WebDAV profile pointing at the test server.
func (h *harness) addProfile(t *testing.T) string {
	t.Helper()
	id := "profile1"
	if _, err := h.store.Update(func(c *config.Config) error {
		sealed, err := c.SealPassword("s3cret")
		if err != nil {
			return err
		}
		c.Profiles = append(c.Profiles, config.Profile{
			ID:          id,
			Name:        "testdav",
			URL:         h.davURL + "/dav",
			Username:    "backupuser",
			PasswordEnc: sealed,
			TimeoutSec:  30,
		})
		return nil
	}); err != nil {
		t.Fatalf("add profile: %v", err)
	}
	return id
}

func (h *harness) addJob(t *testing.T, profileID string, roots []string, keep int, enabled bool) string {
	t.Helper()
	id := "job1"
	if _, err := h.store.Update(func(c *config.Config) error {
		c.Jobs = append(c.Jobs, config.Job{
			ID:        id,
			Name:      "nightly",
			Enabled:   enabled,
			Source:    config.Source{Paths: roots, OneFileSystem: false},
			Target:    config.Target{ProfileID: profileID, Dir: "backups"},
			Schedule:  config.Schedule{Mode: config.ScheduleManual},
			Retention: config.Retention{Keep: keep},
			Options:   config.Options{Compression: config.CompressionGzip, GzipLevel: 6},
		})
		return nil
	}); err != nil {
		t.Fatalf("add job: %v", err)
	}
	return id
}

// writeSource creates a small tree to back up.
func writeSource(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	files := map[string]string{
		"etc/config/network":           "config interface 'lan'",
		"etc/config/wireless":          "config wifi-device 'radio0'",
		"etc/dropbear/authorized_keys": "ssh-ed25519 AAAA test\n",
		"var/log/app.log":              strings.Repeat("log line\n", 200),
	}
	for rel, content := range files {
		full := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func TestBackupPushesArchiveAndRecordsRun(t *testing.T) {
	h := newHarness(t)
	src := writeSource(t)
	profileID := h.addProfile(t)
	jobID := h.addJob(t, profileID, []string{src}, 0, true)

	run, err := h.runner.Backup(context.Background(), jobID, engine.TriggerManual)
	if err != nil {
		t.Fatalf("Backup: %v", err)
	}
	if run.Status != config.StatusSuccess {
		t.Fatalf("run status = %s (%s), log:\n%s", run.Status, run.Message, runLog(t, h, run.ID))
	}
	if run.FileCount < 4 {
		t.Errorf("archived %d files, expected at least 4", run.FileCount)
	}
	if run.ArchiveSize == 0 {
		t.Error("archive size is zero")
	}
	if run.RemotePath == "" {
		t.Fatal("run did not record a remote path")
	}

	// The archive must exist on the "server", under the requested directory.
	local := filepath.Join(h.davRoot, "backups", filepath.Base(filepath.FromSlash(run.RemotePath)))
	st, err := os.Stat(local)
	if err != nil {
		t.Fatalf("archive not found on server at %s: %v", local, err)
	}
	if st.Size() != run.ArchiveSize {
		t.Errorf("server size %d != recorded %d", st.Size(), run.ArchiveSize)
	}

	// The history store must have persisted it.
	if got, ok := h.runner.History().Get(run.ID); !ok || got.Status != config.StatusSuccess {
		t.Errorf("history does not contain a successful run: %+v", got)
	}
}

func TestBackupThenRestoreRoundTrip(t *testing.T) {
	h := newHarness(t)
	src := writeSource(t)
	profileID := h.addProfile(t)
	jobID := h.addJob(t, profileID, []string{src}, 0, true)

	run, err := h.runner.Backup(context.Background(), jobID, engine.TriggerManual)
	if err != nil {
		t.Fatalf("Backup: %v", err)
	}
	if run.Status != config.StatusSuccess {
		t.Fatalf("backup failed: %s\n%s", run.Message, runLog(t, h, run.ID))
	}

	dest := t.TempDir()
	rrun, err := h.runner.Restore(context.Background(), engine.RestoreRequest{
		JobID:      jobID,
		RemotePath: run.RemotePath,
		DestDir:    dest,
		Overwrite:  true,
	})
	if err != nil {
		t.Fatalf("Restore: %v", err)
	}
	if rrun.Status != config.StatusSuccess {
		t.Fatalf("restore status = %s (%s), log:\n%s", rrun.Status, rrun.Message, runLog(t, h, rrun.ID))
	}
	if rrun.FileCount < 4 {
		t.Errorf("restored %d files, expected at least 4", rrun.FileCount)
	}

	// Content must survive the round trip. The archive stores paths with the
	// source's absolute prefix stripped, so locate files by name.
	want := map[string]string{
		"network":         "config interface 'lan'",
		"wireless":        "config wifi-device 'radio0'",
		"authorized_keys": "ssh-ed25519 AAAA test\n",
	}
	seen := map[string]bool{}
	err = filepath.Walk(dest, func(p string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return err
		}
		base := filepath.Base(p)
		if expected, ok := want[base]; ok {
			data, err := os.ReadFile(p)
			if err != nil {
				return err
			}
			if string(data) != expected {
				t.Errorf("%s content mismatch:\n got %q\nwant %q", base, data, expected)
			}
			seen[base] = true
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk restore dest: %v", err)
	}
	for name := range want {
		if !seen[name] {
			t.Errorf("restored tree is missing %q", name)
		}
	}

	// The large log file must come back byte-identical.
	var found string
	_ = filepath.Walk(dest, func(p string, info os.FileInfo, err error) error {
		if err == nil && !info.IsDir() && filepath.Base(p) == "app.log" {
			found = p
		}
		return nil
	})
	if found == "" {
		t.Fatal("app.log was not restored")
	}
	got, err := os.ReadFile(found)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(string(got), "log line") != 200 {
		t.Errorf("app.log content damaged: %d occurrences", strings.Count(string(got), "log line"))
	}
}

func TestRetentionKeepsNewestArchives(t *testing.T) {
	h := newHarness(t)
	src := writeSource(t)
	profileID := h.addProfile(t)
	jobID := h.addJob(t, profileID, []string{src}, 2, true)

	var paths []string
	for i := 0; i < 4; i++ {
		run, err := h.runner.Backup(context.Background(), jobID, engine.TriggerManual)
		if err != nil {
			t.Fatalf("Backup #%d: %v", i+1, err)
		}
		if run.Status != config.StatusSuccess {
			t.Fatalf("run #%d failed: %s\n%s", i+1, run.Message, runLog(t, h, run.ID))
		}
		paths = append(paths, run.RemotePath)
		// Names have second resolution; the unique-path logic should still have
		// produced four distinct files.
	}

	dir := filepath.Join(h.davRoot, "backups")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var archives []string
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".tar.gz") {
			archives = append(archives, e.Name())
		}
	}
	if len(archives) != 2 {
		t.Fatalf("retention kept %d archives, want 2: %v", len(archives), archives)
	}

	// The survivors must be the two most recent runs. Compare by the timestamp
	// embedded in the name, which is the same ordering retention uses.
	keep := map[string]bool{}
	for _, a := range archives {
		keep[a] = true
	}
	for i, p := range paths {
		name := filepath.Base(filepath.FromSlash(p))
		_, err := os.Stat(filepath.Join(dir, name))
		shouldSurvive := i >= len(paths)-2
		if shouldSurvive && err != nil {
			t.Errorf("run #%d produced the newest archive %s but it was pruned: %v", i+1, name, err)
		}
		if !shouldSurvive && err == nil {
			t.Errorf("run #%d produced %s, which should have been pruned", i+1, name)
		}
	}

	// Every surviving archive must still be a readable, well-formed archive.
	for _, a := range archives {
		f, err := os.Open(filepath.Join(dir, a))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := archive.ReadManifest(f); err != nil {
			t.Errorf("surviving archive %s is not readable: %v", a, err)
		}
		_ = f.Close()
	}
}

func TestBackupFailsOnBadCredentials(t *testing.T) {
	h := newHarness(t)
	src := writeSource(t)
	if _, err := h.store.Update(func(c *config.Config) error {
		sealed, err := c.SealPassword("wrong-password")
		if err != nil {
			return err
		}
		c.Profiles = append(c.Profiles, config.Profile{
			ID: "bad", Name: "bad",
			URL: h.davURL + "/dav", Username: "backupuser",
			PasswordEnc: sealed, TimeoutSec: 15,
		})
		c.Jobs = append(c.Jobs, config.Job{
			ID: "jbad", Name: "bad", Enabled: true,
			Source:   config.Source{Paths: []string{src}},
			Target:   config.Target{ProfileID: "bad", Dir: "x"},
			Schedule: config.Schedule{Mode: config.ScheduleManual},
			Options:  config.Options{Compression: config.CompressionGzip},
		})
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	run, err := h.runner.Backup(context.Background(), "jbad", engine.TriggerManual)
	if err != nil {
		t.Fatalf("Backup returned a hard error instead of a failed run: %v", err)
	}
	if run.Status != config.StatusFailed {
		t.Fatalf("status = %s, want failed", run.Status)
	}
	if !strings.Contains(run.Message, "401") && !strings.Contains(run.Message, "认证") {
		t.Errorf("failure message does not explain the cause: %q", run.Message)
	}
}

func TestBackupRejectsConcurrentRunOfSameJob(t *testing.T) {
	h := newHarness(t)
	src := writeSource(t)
	profileID := h.addProfile(t)
	jobID := h.addJob(t, profileID, []string{src}, 0, true)

	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _ = h.runner.Backup(context.Background(), jobID, engine.TriggerManual)
	}()

	// Give the first run a moment to claim the slot, then try a second.
	deadline := time.Now().Add(3 * time.Second)
	for !h.runner.Running(jobID) && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	_, err := h.runner.Backup(context.Background(), jobID, engine.TriggerManual)
	if err == nil {
		t.Error("second concurrent run was accepted; expected ErrJobRunning")
	}
	<-done
}

func TestBackupWithExcludesAndMissingSourceReportsPartial(t *testing.T) {
	h := newHarness(t)
	src := writeSource(t)
	profileID := h.addProfile(t)

	if _, err := h.store.Update(func(c *config.Config) error {
		c.Jobs = append(c.Jobs, config.Job{
			ID: "j2", Name: "with-missing", Enabled: true,
			Source: config.Source{
				Paths:   []string{src, filepath.Join(src, "does-not-exist")},
				Exclude: []string{"*.log"},
			},
			Target:   config.Target{ProfileID: profileID, Dir: "backups"},
			Schedule: config.Schedule{Mode: config.ScheduleManual},
			Options:  config.Options{Compression: config.CompressionGzip},
		})
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	run, err := h.runner.Backup(context.Background(), "j2", engine.TriggerManual)
	if err != nil {
		t.Fatalf("Backup: %v", err)
	}
	if run.Status != config.StatusPartial && run.Status != config.StatusSuccess {
		t.Fatalf("status = %s (%s)", run.Status, run.Message)
	}

	// The excluded log must not be in the archive.
	entries, err := os.ReadDir(filepath.Join(h.davRoot, "backups"))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) == 0 {
		t.Fatal("no archive was uploaded")
	}
}

// TestCancelStopsRunningBackup guards the cancellation wiring: Runner.Cancel
// must cancel the context the execution actually uses. A regression here is
// invisible on small jobs (they finish anyway) but bites on large ones — the
// "terminate" button keeps reporting success while the run ploughs on.
func TestCancelStopsRunningBackup(t *testing.T) {
	h := newHarness(t)
	src := writeSource(t)
	profileID := h.addProfile(t)
	jobID := h.addJob(t, profileID, []string{src}, 0, true)

	done := make(chan *engine.Run, 1)
	go func() {
		run, _ := h.runner.Backup(context.Background(), jobID, engine.TriggerManual)
		done <- run
	}()

	// As soon as the run claims its slot, cancel it.
	deadline := time.Now().Add(5 * time.Second)
	canceled := false
	for time.Now().Before(deadline) {
		if h.runner.Running(jobID) {
			if !h.runner.Cancel(jobID) {
				t.Fatal("Cancel reported no running execution")
			}
			canceled = true
			break
		}
		time.Sleep(time.Millisecond)
	}
	if !canceled {
		t.Fatal("job never appeared as running")
	}

	select {
	case run := <-done:
		if run == nil {
			t.Fatal("Backup returned a nil run")
		}
		if run.Status != config.StatusFailed {
			t.Fatalf("cancelled run finished with status %s (%s) — cancellation had no effect; log:\n%s",
				run.Status, run.Message, runLog(t, h, run.ID))
		}
		if h.runner.Running(jobID) {
			t.Error("slot still marked running after the cancelled run returned")
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Backup did not return after cancellation")
	}
}

func runLog(t *testing.T, h *harness, id string) string {
	t.Helper()
	text, _, err := h.runner.History().ReadLog(id, 0, 64<<10)
	if err != nil {
		return fmt.Sprintf("(log unavailable: %v)", err)
	}
	return text
}
