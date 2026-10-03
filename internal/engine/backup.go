package engine

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/qzrsa/qzrs-webdav-backup/internal/archive"
	"github.com/qzrsa/qzrs-webdav-backup/internal/config"
	"github.com/qzrsa/qzrs-webdav-backup/internal/dav"
)

// ErrJobRunning is returned when the same job is already executing.
var ErrJobRunning = errors.New("this job is already running")

// Runner executes jobs and records their outcome.
type Runner struct {
	store   *config.Store
	history *History
	logf    func(format string, args ...any)
	running map[string]context.CancelFunc
	mu      sync.Mutex
}

// NewRunner builds a Runner.
func NewRunner(store *config.Store, history *History, syslog func(string, ...any)) *Runner {
	if syslog == nil {
		syslog = func(string, ...any) {}
	}
	return &Runner{
		store:   store,
		history: history,
		logf:    syslog,
		running: map[string]context.CancelFunc{},
	}
}

// History exposes the run store.
func (r *Runner) History() *History { return r.history }

// Running reports whether a job currently has an execution in flight.
func (r *Runner) Running(jobID string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	_, ok := r.running[jobID]
	return ok
}

// RunningJobs lists job ids with an execution in flight.
func (r *Runner) RunningJobs() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]string, 0, len(r.running))
	for id := range r.running {
		out = append(out, id)
	}
	sort.Strings(out)
	return out
}

// Cancel stops an in-flight execution. It reports whether one was found.
func (r *Runner) Cancel(jobID string) bool {
	r.mu.Lock()
	cancel, ok := r.running[jobID]
	r.mu.Unlock()
	if ok {
		cancel()
	}
	return ok
}

// acquire registers an execution slot under the given key and returns the
// derived, cancellable context that the execution must use. Runner.Cancel
// cancels this context, so handing back anything else means cancellation is
// silently ignored. The returned release function frees the slot (and cancels).
func (r *Runner) acquire(parent context.Context, key string) (context.Context, func(), error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.running[key]; ok {
		return nil, nil, ErrJobRunning
	}
	if parent == nil {
		parent = context.Background()
	}
	ctx, cancel := context.WithCancel(parent)
	r.running[key] = cancel
	return ctx, func() {
		cancel()
		r.mu.Lock()
		delete(r.running, key)
		r.mu.Unlock()
	}, nil
}

// ---------------------------------------------------------------------------
// Backup
// ---------------------------------------------------------------------------

// Backup runs one job and returns its run record. A non-nil error means the run
// could not even be started; execution failures are reported inside the Run.
func (r *Runner) Backup(ctx context.Context, jobID, trigger string) (*Run, error) {
	cfg := r.store.Snapshot()
	job := cfg.FindJob(jobID)
	if job == nil {
		return nil, fmt.Errorf("job %q not found", jobID)
	}
	profile := cfg.FindProfile(job.Target.ProfileID)
	if profile == nil {
		return nil, fmt.Errorf("job %q references a profile that no longer exists", job.Name)
	}
	password, err := cfg.DecryptPassword(profile)
	if err != nil {
		return nil, fmt.Errorf("cannot decrypt stored credentials for %q: %w", profile.Name, err)
	}

	execCtx, release, err := r.acquire(ctx, jobID)
	if err != nil {
		return nil, err
	}
	defer release()

	run := NewRun(job, TypeBackup, trigger)
	if err := r.history.Save(run); err != nil {
		return nil, err
	}
	lg, logErr := newRunLogger(r.history.LogPath(run.ID))
	if logErr != nil {
		r.logf("cannot create run log for %s: %v", run.ID, logErr)
	}
	defer lg.Close()

	lg.Printf("=== 备份任务开始：%s (%s) ===", job.Name, job.ID)
	lg.Printf("触发方式：%s", triggerName(trigger))
	lg.Printf("远端：%s  目录：%s", profile.URL, orRoot(job.Target.Dir))
	lg.Printf("源路径：%s", strings.Join(job.Source.Paths, ", "))

	execErr := r.executeBackup(execCtx, cfg, job, profile, password, run, lg)

	finishRun(run, execErr)
	if execErr != nil {
		// The message also lands in the run record, but the log is what people
		// actually read line by line — leaving it out made failures look like
		// they had no cause at all.
		lg.Printf("      失败原因：%s", execErr)
	}
	lg.Printf("=== 结束：%s  用时 %s ===", statusZh(run.Status), time.Duration(run.DurationMS)*time.Millisecond)
	if err := r.history.Save(run); err != nil {
		r.logf("cannot save run %s: %v", run.ID, err)
	}

	// Reflect the outcome on the job for quick display in the UI.
	now := run.StartedAt
	_, _ = r.store.Update(func(c *config.Config) error {
		if j := c.FindJob(jobID); j != nil {
			j.LastRunAt = &now
			j.LastStatus = run.Status
		}
		return nil
	})
	r.history.Prune(0)

	return run, nil
}

func (r *Runner) executeBackup(ctx context.Context, cfg *config.Config, job *config.Job,
	profile *config.Profile, password string, run *Run, lg *RunLogger) error {

	client, err := dav.New(dav.Config{
		BaseURL:     profile.URL,
		Username:    profile.Username,
		Password:    password,
		InsecureTLS: profile.InsecureTLS,
		Timeout:     time.Duration(profile.TimeoutSec) * time.Second,
	})
	if err != nil {
		return err
	}

	run.Stage = "connecting"
	lg.Printf("[1/5] 测试 WebDAV 连接 ...")
	if err := client.Ping(ctx); err != nil {
		return fmt.Errorf("无法连接 WebDAV：%w", err)
	}
	lg.Printf("      连接正常，认证通过")

	remoteDir := strings.Trim(strings.ReplaceAll(job.Target.Dir, "\\", "/"), "/")
	if remoteDir != "" {
		run.Stage = "preparing"
		lg.Printf("[2/5] 确保远端目录存在：/%s", remoteDir)
		if err := client.MkdirAll(ctx, remoteDir); err != nil {
			return fmt.Errorf("创建远端目录失败：%w", err)
		}
	} else {
		// The archive lands in the directory the profile URL points at. That
		// directory is part of the *configured* URL, so it is never created
		// automatically — blindly MKCOL-ing its segments would litter a
		// collection prefix like OpenList's /dav with junk directories in the
		// storage root. Verify it up front instead of failing later on the
		// PUT with a bare 404.
		run.Stage = "preparing"
		lg.Printf("[2/5] 使用 WebDAV 地址指向的目录")
		if _, err := client.Stat(ctx, ""); errors.Is(err, dav.ErrNotFound) {
			return fmt.Errorf("WebDAV 地址指向的目录在服务器上不存在（%s）。" +
				"请先在 OpenList/网盘里创建该目录，或把地址末尾的目录段移到" +
				"任务「目标目录」中并精简 WebDAV 地址，后者会自动逐级创建",
				displayBaseDir(profile.URL))
		} else if err != nil {
			lg.Printf("      提示：无法确认远端目录是否存在（%v），继续尝试上传", err)
		}
	}

	started := time.Now()
	fileName := archiveFileName(job.Name, started, archiveExt(job.Options.Compression))
	remotePath := r.uniqueRemotePath(ctx, client, remoteDir, fileName, lg)

	opts := archive.Options{
		Compression:    job.Options.Compression,
		GzipLevel:      job.Options.GzipLevel,
		Include:        job.Source.Include,
		Exclude:        job.Source.Exclude,
		MaxFileSizeMB:  job.Source.MaxFileSizeMB,
		FollowSymlinks: job.Source.FollowSymlinks,
		OneFileSystem:  job.Source.OneFileSystem,
		ExcludeCaches:  job.Options.ExcludeCaches,
		Roots:          job.Source.Paths,
	}

	// A progress callback keeps the run record fresh without hammering the
	// disk: at most one save per second.
	lastSave := time.Now()
	progress := func(p archive.Progress) {
		run.FileCount = p.Files
		run.RawSize = p.Bytes
		if time.Since(lastSave) > time.Second {
			lastSave = time.Now()
			_ = r.history.Save(run)
		}
	}

	var stats *archive.Stats
	var manifest *archive.Manifest

	if job.Options.Stream {
		run.Stage = "archiving"
		lg.Printf("[3/5] 流式打包并上传（不占用本地临时空间）")
		uploaded, err := r.streamUpload(ctx, client, remotePath, opts, progress)
		if err != nil {
			return err
		}
		run.ArchiveSize = uploaded
		lg.Printf("      已上传 %s", humanBytes(uploaded))
		// Streamed archives cannot report detailed statistics; re-derive what
		// we can from the counters the caller tracked.
		stats = &archive.Stats{Files: run.FileCount, TotalBytes: run.RawSize}
	} else {
		run.Stage = "archiving"
		tempDir := r.stagingDir(cfg, job)
		if err := os.MkdirAll(tempDir, 0o700); err != nil {
			return fmt.Errorf("cannot create staging directory %s: %w", tempDir, err)
		}
		stagingPath := filepath.Join(tempDir, run.ID+"-"+fileName)
		defer func() { _ = os.Remove(stagingPath) }()

		lg.Printf("[3/5] 打包到本地暂存：%s", stagingPath)
		stats, manifest, err = r.createArchive(ctx, stagingPath, opts, progress, lg)
		if err != nil {
			return err
		}
		run.FileCount = stats.Files
		run.DirCount = stats.Dirs
		run.Skipped = stats.Skipped
		run.RawSize = stats.TotalBytes

		fi, statErr := os.Stat(stagingPath)
		if statErr != nil {
			return fmt.Errorf("cannot stat staged archive: %w", statErr)
		}
		run.ArchiveSize = fi.Size()
		lg.Printf("      打包完成：%d 个文件 / %d 个目录，原始 %s → 压缩 %s（%.0f%%）",
			stats.Files, stats.Dirs, humanBytes(stats.TotalBytes), humanBytes(fi.Size()),
			compressionRatio(stats.TotalBytes, fi.Size()))

		run.Stage = "uploading"
		lg.Printf("[4/5] 上传到 /%s ...", remotePath)
		up := time.Now()
		uploaded, err := client.UploadFile(ctx, stagingPath, remotePath)
		if err != nil {
			return fmt.Errorf("上传失败：%w", err)
		}
		rate := float64(uploaded) / 1024 / 1024 / time.Since(up).Seconds()
		lg.Printf("      上传完成：%s，用时 %s（%.2f MB/s）",
			humanBytes(uploaded), time.Since(up).Round(time.Second), rate)

		// Verify what landed on the server.
		if st, err := client.Stat(ctx, remotePath); err == nil && st.Size > 0 && st.Size != uploaded {
			lg.Printf("      警告：远端报告大小 %s 与本地 %s 不一致，请检查服务端配额或压缩传输",
				humanBytes(st.Size), humanBytes(uploaded))
		} else if err == nil {
			lg.Printf("      校验通过：远端文件大小一致")
		}
	}

	run.RemotePath = remotePath

	// Record non-fatal problems found while walking the tree.
	if stats != nil {
		run.FileCount = stats.Files
		run.DirCount = stats.Dirs
		run.Skipped = stats.Skipped
		run.RawSize = stats.TotalBytes
		run.ErrorCount = len(stats.Errors)
		if len(stats.Errors) > 0 {
			limit := len(stats.Errors)
			if limit > 20 {
				limit = 20
			}
			run.Errors = append([]string{}, stats.Errors[:limit]...)
			lg.Printf("      读取过程中有 %d 个警告：", len(stats.Errors))
			for _, e := range stats.Errors[:limit] {
				lg.Printf("        - %s", e)
			}
			if len(stats.Errors) > limit {
				lg.Printf("        ... 其余 %d 条略", len(stats.Errors)-limit)
			}
		}
	}

	if manifest != nil {
		lg.Printf("      归档清单：%s 于 %s 创建，主机 %s",
			manifest.Tool, manifest.CreatedAt.Format(time.RFC3339), manifest.Hostname)
	}

	run.Stage = "pruning"
	lg.Printf("[5/5] 应用保留策略 ...")
	r.applyRetention(ctx, client, remoteDir, job, lg)

	if errLater := ctx.Err(); errLater != nil {
		return fmt.Errorf("任务被取消：%w", errLater)
	}
	return nil
}

// displayBaseDir returns the path portion of a profile URL for error messages,
// e.g. "http://host:5244/dav/Backup/OpenWrt/" → "/Backup/OpenWrt".
func displayBaseDir(rawURL string) string {
	u, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil || u.Path == "" || u.Path == "/" {
		return rawURL
	}
	return strings.TrimSuffix(u.Path, "/")
}

func (r *Runner) createArchive(ctx context.Context, dest string, opts archive.Options,
	progress func(archive.Progress), lg *RunLogger) (*archive.Stats, *archive.Manifest, error) {

	f, err := os.OpenFile(dest, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, nil, fmt.Errorf("cannot create archive file: %w", err)
	}
	defer f.Close()

	stats, manifest, err := archive.Create(ctx, f, opts, progress)
	if err != nil {
		return nil, nil, classifyArchiveError(err)
	}
	if err := f.Sync(); err != nil {
		return nil, nil, fmt.Errorf("cannot flush archive to disk: %w", err)
	}
	return stats, manifest, nil
}

func (r *Runner) streamUpload(ctx context.Context, client *dav.Client, remotePath string,
	opts archive.Options, progress func(archive.Progress)) (int64, error) {

	pr, pw := io.Pipe()
	counter := &countingWriter{w: pw}
	done := make(chan error, 1)

	go func() {
		_, _, err := archive.Create(ctx, counter, opts, progress)
		// Closing with the error propagates it to the reader side.
		if err != nil {
			_ = pw.CloseWithError(classifyArchiveError(err))
			done <- err
			return
		}
		done <- pw.Close()
	}()

	uploadErr := client.Upload(ctx, remotePath, pr, -1)
	if uploadErr != nil {
		// Unblock the producer so the goroutine can exit.
		_ = pr.CloseWithError(uploadErr)
		<-done
		return 0, fmt.Errorf("流式上传失败（部分服务端不支持无长度 PUT，请关闭流式模式）：%w", uploadErr)
	}
	if err := <-done; err != nil {
		return 0, err
	}
	return counter.n, nil
}

func (r *Runner) stagingDir(cfg *config.Config, job *config.Job) string {
	if d := strings.TrimSpace(job.Options.TempDir); d != "" {
		return d
	}
	if d := strings.TrimSpace(cfg.TempDir); d != "" {
		return d
	}
	return filepath.Join(cfg.DataDir, "tmp")
}

// uniqueRemotePath picks a path that does not clobber an existing archive.
// Archive names carry second resolution, so two runs inside the same second
// would otherwise silently overwrite the first one.
func (r *Runner) uniqueRemotePath(ctx context.Context, client *dav.Client, dir, name string, lg *RunLogger) string {
	base := strings.TrimSuffix(name, ".tar.gz")
	base = strings.TrimSuffix(base, ".tar")
	ext := strings.TrimPrefix(name, base)

	candidate := path.Join(dir, name)
	for i := 2; i <= 50; i++ {
		exists, err := client.Exists(ctx, candidate)
		if err != nil {
			// If existence cannot be determined, prefer proceeding with the
			// plain name over failing the whole backup.
			return path.Join(dir, name)
		}
		if !exists {
			return candidate
		}
		if i == 2 {
			lg.Printf("      远端已存在同名归档，自动改用带序号的名称")
		}
		candidate = path.Join(dir, fmt.Sprintf("%s-%d%s", base, i, ext))
	}
	return path.Join(dir, fmt.Sprintf("%s-%d%s", base, time.Now().UnixNano(), ext))
}

// applyRetention deletes the oldest archives produced by this job, keeping the
// newest `Keep` of them. Keep == 0 disables pruning.
func (r *Runner) applyRetention(ctx context.Context, client *dav.Client, remoteDir string,
	job *config.Job, lg *RunLogger) {

	keep := job.Retention.Keep
	if keep <= 0 {
		lg.Printf("      保留策略为「不限制」，跳过清理")
		return
	}

	entries, err := client.List(ctx, remoteDir)
	if err != nil {
		lg.Printf("      清理跳过：无法列出远端目录（%v）", err)
		return
	}

	prefix := sanitizeName(job.Name) + "-"
	ext := archiveExt(job.Options.Compression)
	var mine []dav.Resource
	for _, e := range entries {
		if e.IsDir || !strings.HasPrefix(e.Name, prefix) || !strings.HasSuffix(e.Name, ext) {
			continue
		}
		mine = append(mine, e)
	}

	// Newest first. Ordering is derived from the timestamp embedded in the
	// file name rather than from server metadata, which may be second-precision
	// or missing entirely.
	sort.Slice(mine, func(i, j int) bool {
		ka, kb := archiveSortKey(mine[i].Name), archiveSortKey(mine[j].Name)
		if ka != kb {
			return ka > kb
		}
		return mine[i].Name > mine[j].Name
	})

	lg.Printf("      远端已有 %d 个本任务的备份，保留最新 %d 个", len(mine), keep)
	if len(mine) <= keep {
		return
	}
	for _, old := range mine[keep:] {
		oldPath := path.Join(remoteDir, old.Name)
		if err := client.Delete(ctx, oldPath); err != nil {
			lg.Printf("      删除失败 %s：%v", old.Name, err)
			continue
		}
		lg.Printf("      已删除旧备份：%s（%s）", old.Name, humanBytes(old.Size))
	}
}

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

type countingWriter struct {
	w io.Writer
	n int64
}

func (c *countingWriter) Write(p []byte) (int, error) {
	n, err := c.w.Write(p)
	c.n += int64(n)
	return n, err
}

func finishRun(run *Run, err error) {
	now := time.Now()
	run.FinishedAt = &now
	run.DurationMS = now.Sub(run.StartedAt).Milliseconds()
	run.Stage = "done"
	switch {
	case err != nil:
		run.Status = config.StatusFailed
		run.Message = err.Error()
	case run.ErrorCount > 0:
		run.Status = config.StatusPartial
		run.Message = fmt.Sprintf("完成，但有 %d 个文件被跳过或读取失败", run.ErrorCount)
	default:
		run.Status = config.StatusSuccess
		run.Message = ""
	}
}

// classifyArchiveError turns common OS failures into actionable messages.
func classifyArchiveError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, context.Canceled) {
		return errors.New("任务被取消")
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return errors.New("任务超时")
	}
	msg := err.Error()
	switch {
	case strings.Contains(msg, "no space left on device"):
		return fmt.Errorf("本地空间不足，请把暂存目录指向容量更大的分区：%w", err)
	case strings.Contains(msg, "permission denied"):
		return fmt.Errorf("权限不足，请确认服务以 root 运行或路径可读：%w", err)
	case strings.Contains(msg, "too many open files"):
		return fmt.Errorf("打开文件数超出上限，请调高 ulimit 或缩减备份范围：%w", err)
	}
	return err
}

// archiveFileName builds "<job>-<timestamp><ext>".
//
// The timestamp includes milliseconds and is fixed width
// ("20261002-135332483"), so plain string comparison on the name reproduces
// chronological order — which is what retention relies on, since server
// modification times have only one-second resolution and are sometimes absent.
//
// The millisecond field is appended explicitly rather than through a "000"
// layout token: Go only recognises fractional seconds when the layout uses a
// leading dot, and a literal dot in the name would read as an extension.
func archiveFileName(jobName string, t time.Time, ext string) string {
	ts := t.Format("20060102-150405") + fmt.Sprintf("%03d", t.Nanosecond()/1_000_000)
	return sanitizeName(jobName) + "-" + ts + ext
}

// archiveSortKey returns the chronological portion of an archive name, with any
// "-N" conflict suffix removed so that a retried upload still sorts by the time
// it was produced rather than by its suffix.
func archiveSortKey(name string) string {
	base := name
	for _, ext := range []string{".tar.gz", ".tar"} {
		if strings.HasSuffix(base, ext) {
			base = strings.TrimSuffix(base, ext)
			break
		}
	}
	if i := strings.LastIndex(base, "-"); i > 0 {
		suffix := base[i+1:]
		// The timestamp field is 9 digits (HHMMSSmmm), so a suffix of three
		// digits or fewer can only be a conflict counter.
		if len(suffix) <= 3 {
			if _, err := strconv.Atoi(suffix); err == nil {
				base = base[:i]
			}
		}
	}
	return base
}

func archiveExt(compression string) string {
	if compression == config.CompressionNone {
		return ".tar"
	}
	return ".tar.gz"
}

// sanitizeName keeps a filename safe for any WebDAV server and for sorting.
func sanitizeName(name string) string {
	name = strings.TrimSpace(name)
	var b strings.Builder
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == '-' || r == '_':
			b.WriteRune(r)
		case r == ' ' || r == '.':
			b.WriteRune('_')
		case r > 0x7f:
			// Keep CJK and other letters; they are valid in WebDAV paths once
			// percent-encoded, and users expect readable names.
			if isLetterOrDigit(r) {
				b.WriteRune(r)
			} else {
				b.WriteRune('_')
			}
		default:
			b.WriteRune('_')
		}
	}
	out := strings.Trim(b.String(), "_-")
	if out == "" {
		out = "backup"
	}
	if len(out) > 64 {
		out = out[:64]
	}
	return out
}

func isLetterOrDigit(r rune) bool {
	return (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') ||
		(r >= 0x4e00 && r <= 0x9fff) || // CJK unified ideographs
		(r >= 0x3040 && r <= 0x30ff) // kana
}

func triggerName(t string) string {
	if t == TriggerSchedule {
		return "定时调度"
	}
	return "手动触发"
}

func statusZh(s string) string {
	switch s {
	case config.StatusSuccess:
		return "成功"
	case config.StatusPartial:
		return "部分成功"
	case config.StatusFailed:
		return "失败"
	case config.StatusRunning:
		return "执行中"
	}
	return s
}

func orRoot(dir string) string {
	d := strings.Trim(dir, "/")
	if d == "" {
		return "/"
	}
	return "/" + d
}

func humanBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for v := n / unit; v >= unit; v /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.2f %cB", float64(n)/float64(div), "KMGTPE"[exp])
}

func compressionRatio(raw, compressed int64) float64 {
	if raw <= 0 {
		return 0
	}
	return float64(compressed) / float64(raw) * 100
}
