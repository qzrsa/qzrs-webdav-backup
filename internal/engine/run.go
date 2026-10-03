// Package engine executes backup and restore jobs and records what happened.
package engine

import (
	crand "crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/qzrsa/qzrs-webdav-backup/internal/config"
)

// Run kinds.
const (
	TypeBackup  = "backup"
	TypeRestore = "restore"
)

// Triggers.
const (
	TriggerManual   = "manual"
	TriggerSchedule = "schedule"
)

// Run is one execution of a job, persisted as a single JSON file.
type Run struct {
	ID         string     `json:"id"`
	JobID      string     `json:"job_id"`
	JobName    string     `json:"job_name"`
	Type       string     `json:"type"`
	Status     string     `json:"status"`
	Trigger    string     `json:"trigger"`
	StartedAt  time.Time  `json:"started_at"`
	FinishedAt *time.Time `json:"finished_at,omitempty"`
	DurationMS int64      `json:"duration_ms"`

	RemotePath  string `json:"remote_path,omitempty"`
	ArchiveSize int64  `json:"archive_size,omitempty"`
	RawSize     int64  `json:"raw_size,omitempty"`
	FileCount   int    `json:"file_count,omitempty"`
	DirCount    int    `json:"dir_count,omitempty"`
	Skipped     int    `json:"skipped,omitempty"`
	ErrorCount  int    `json:"error_count,omitempty"`

	// Restore-only fields.
	RestoreFrom string `json:"restore_from,omitempty"`
	RestoreDest string `json:"restore_dest,omitempty"`
	DryRun      bool   `json:"dry_run,omitempty"`

	Message string   `json:"message,omitempty"`
	Errors  []string `json:"errors,omitempty"`

	// Stage is a human-readable progress hint for the UI.
	Stage string `json:"stage,omitempty"`
}

// Finished reports whether the run has reached a terminal state.
func (r *Run) Finished() bool {
	return r.Status != config.StatusRunning
}

// ---------------------------------------------------------------------------
// Run logging
// ---------------------------------------------------------------------------

// RunLogger appends human-readable lines to a per-run log file and keeps track
// of its size so the UI can poll for increments.
type RunLogger struct {
	mu   sync.Mutex
	f    *os.File
	path string
	size int64
}

func newRunLogger(path string) (*RunLogger, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_TRUNC|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return nil, err
	}
	return &RunLogger{f: f, path: path}, nil
}

// Printf writes a timestamped line.
func (l *RunLogger) Printf(format string, args ...any) {
	if l == nil {
		return
	}
	msg := fmt.Sprintf(format, args...)
	line := time.Now().Format("2006-01-02 15:04:05") + "  " + msg + "\n"
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.f == nil {
		return
	}
	n, _ := l.f.WriteString(line)
	l.size += int64(n)
}

// Size returns the number of bytes written so far.
func (l *RunLogger) Size() int64 {
	if l == nil {
		return 0
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.size
}

// Close flushes and closes the log file.
func (l *RunLogger) Close() {
	if l == nil {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.f != nil {
		_ = l.f.Sync()
		_ = l.f.Close()
		l.f = nil
	}
}

// ---------------------------------------------------------------------------
// History store
// ---------------------------------------------------------------------------

// History persists runs as individual JSON files under <dir>/runs and their
// logs under <dir>/logs.
type History struct {
	dir      string
	runsDir  string
	logsDir  string
	mu       sync.RWMutex
	cache    map[string]*Run
	keep     int
	keepLock sync.Mutex
}

// NewHistory opens (and creates) the history store.
func NewHistory(dir string, keep int) (*History, error) {
	h := &History{
		dir:     dir,
		runsDir: filepath.Join(dir, "runs"),
		logsDir: filepath.Join(dir, "logs"),
		cache:   map[string]*Run{},
		keep:    keep,
	}
	if h.keep <= 0 {
		h.keep = 200
	}
	for _, d := range []string{h.runsDir, h.logsDir} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			return nil, fmt.Errorf("create history dir %s: %w", d, err)
		}
	}
	h.loadAll()
	return h, nil
}

func (h *History) loadAll() {
	entries, err := os.ReadDir(h.runsDir)
	if err != nil {
		return
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		id := strings.TrimSuffix(e.Name(), ".json")
		if run, err := h.readRunFile(id); err == nil {
			h.cache[id] = run
		}
	}
}

func (h *History) runPath(id string) string { return filepath.Join(h.runsDir, id+".json") }
func (h *History) logPath(id string) string { return filepath.Join(h.logsDir, id+".log") }

// LogPath returns where a run's log lives on disk.
func (h *History) LogPath(id string) string { return h.logPath(id) }

func (h *History) readRunFile(id string) (*Run, error) {
	raw, err := os.ReadFile(h.runPath(id))
	if err != nil {
		return nil, err
	}
	var r Run
	if err := json.Unmarshal(raw, &r); err != nil {
		return nil, err
	}
	return &r, nil
}

// Save writes a run to disk atomically and refreshes the cache.
func (h *History) Save(run *Run) error {
	raw, err := json.MarshalIndent(run, "", "  ")
	if err != nil {
		return err
	}
	tmp := h.runPath(run.ID) + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o600); err != nil {
		return err
	}
	if err := os.Rename(tmp, h.runPath(run.ID)); err != nil {
		return err
	}
	h.mu.Lock()
	h.cache[run.ID] = cloneRun(run)
	h.mu.Unlock()
	return nil
}

func cloneRun(r *Run) *Run {
	if r == nil {
		return nil
	}
	cp := *r
	if r.Errors != nil {
		cp.Errors = append([]string{}, r.Errors...)
	}
	return &cp
}

// Get returns a copy of the run with the given id.
func (h *History) Get(id string) (*Run, bool) {
	h.mu.RLock()
	r, ok := h.cache[id]
	h.mu.RUnlock()
	if !ok {
		if loaded, err := h.readRunFile(id); err == nil {
			return loaded, true
		}
		return nil, false
	}
	return cloneRun(r), true
}

// ListOptions filters and bounds a history query.
type ListOptions struct {
	JobID string
	Type  string
	Limit int
}

// List returns runs newest-first.
func (h *History) List(opts ListOptions) []*Run {
	h.mu.RLock()
	all := make([]*Run, 0, len(h.cache))
	for _, r := range h.cache {
		if opts.JobID != "" && r.JobID != opts.JobID {
			continue
		}
		if opts.Type != "" && r.Type != opts.Type {
			continue
		}
		all = append(all, cloneRun(r))
	}
	h.mu.RUnlock()

	sort.Slice(all, func(i, j int) bool { return all[i].StartedAt.After(all[j].StartedAt) })
	if opts.Limit > 0 && len(all) > opts.Limit {
		all = all[:opts.Limit]
	}
	return all
}

// Delete removes a run and its log.
func (h *History) Delete(id string) error {
	h.mu.Lock()
	delete(h.cache, id)
	h.mu.Unlock()
	errRun := os.Remove(h.runPath(id))
	_ = os.Remove(h.logPath(id))
	if errRun != nil && !errors.Is(errRun, os.ErrNotExist) {
		return errRun
	}
	return nil
}

// Prune keeps the newest `keep` runs (0 uses the store default) and deletes the
// rest along with their logs.
func (h *History) Prune(keep int) int {
	h.keepLock.Lock()
	defer h.keepLock.Unlock()

	if keep <= 0 {
		keep = h.keep
	}
	if keep <= 0 {
		return 0
	}
	all := h.List(ListOptions{})
	if len(all) <= keep {
		return 0
	}
	removed := 0
	for _, r := range all[keep:] {
		if err := h.Delete(r.ID); err == nil {
			removed++
		}
	}
	return removed
}

// ReadLog returns the run log from byte offset, capped at maxBytes. It also
// returns the new total size so the caller can poll incrementally.
func (h *History) ReadLog(id string, offset int64, maxBytes int64) (string, int64, error) {
	if maxBytes <= 0 {
		maxBytes = 256 << 10
	}
	f, err := os.Open(h.logPath(id))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", 0, nil
		}
		return "", 0, err
	}
	defer f.Close()

	st, err := f.Stat()
	if err != nil {
		return "", 0, err
	}
	size := st.Size()
	if offset < 0 {
		offset = 0
	}
	if offset > size {
		offset = 0 // log was truncated/replaced; restart
	}
	if _, err := f.Seek(offset, io.SeekStart); err != nil {
		return "", size, err
	}
	buf := make([]byte, maxBytes)
	n, err := f.Read(buf)
	if err != nil && n == 0 {
		return "", size, nil
	}
	return string(buf[:n]), size, nil
}

// NewRun builds an unstarted run record with a sortable, unique id.
func NewRun(job *config.Job, typ, trigger string) *Run {
	now := time.Now()
	return &Run{
		ID:        fmt.Sprintf("%s-%s", now.Format("20060102-150405"), randSuffix(6)),
		JobID:     job.ID,
		JobName:   job.Name,
		Type:      typ,
		Status:    config.StatusRunning,
		Trigger:   trigger,
		StartedAt: now,
		Stage:     "starting",
	}
}

func randSuffix(n int) string {
	const alphabet = "abcdefghijklmnopqrstuvwxyz0123456789"
	b := make([]byte, n)
	raw := make([]byte, n)
	if _, err := crand.Read(raw); err != nil {
		return strings.Repeat("0", n)
	}
	for i := range b {
		b[i] = alphabet[int(raw[i])%len(alphabet)]
	}
	return string(b)
}
