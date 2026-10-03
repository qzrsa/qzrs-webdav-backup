package engine

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/qzrsa/qzrs-webdav-backup/internal/archive"
	"github.com/qzrsa/qzrs-webdav-backup/internal/config"
	"github.com/qzrsa/qzrs-webdav-backup/internal/dav"
)

// RestoreRequest describes a restore operation.
type RestoreRequest struct {
	// JobID is optional. When set, the job's target profile is used to reach
	// the archive, which is how the UI triggers a "restore from my own backup"
	// flow without repeating credentials.
	JobID string
	// ProfileID/RemotePath identify the archive directly. ProfileID is ignored
	// when JobID is set.
	ProfileID  string
	RemotePath string
	// DestDir is where the archive is unpacked locally.
	DestDir string
	// Overwrite replaces existing files instead of skipping them.
	Overwrite bool
	// DryRun lists what would happen without writing anything.
	DryRun bool
	// StripComponents drops leading path elements, like tar --strip-components.
	StripComponents int
	// StagingDir overrides where the archive is downloaded before extraction.
	StagingDir string
	Trigger    string
	// VerifyOnly downloads the archive and reads its manifest without writing.
	VerifyOnly bool
}

// RestorePreview summarises an archive without extracting it.
type RestorePreview struct {
	RemotePath  string            `json:"remote_path"`
	TotalSize   int64             `json:"total_size"`
	Manifest    *archive.Manifest `json:"manifest,omitempty"`
	Entries     []string          `json:"entries,omitempty"`
	EntryLimit  int               `json:"entry_limit"`
	EntriesMore bool              `json:"entries_truncated,omitempty"`
}

// Preview downloads an archive to a staging file and inspects it.
func (r *Runner) Preview(ctx context.Context, profileID, remotePath, stagingDir string) (*RestorePreview, error) {
	cfg := r.store.Snapshot()
	profile := cfg.FindProfile(profileID)
	if profile == nil {
		return nil, fmt.Errorf("profile %q not found", profileID)
	}
	password, err := cfg.DecryptPassword(profile)
	if err != nil {
		return nil, err
	}
	client, err := dav.New(dav.Config{
		BaseURL:     profile.URL,
		Username:    profile.Username,
		Password:    password,
		InsecureTLS: profile.InsecureTLS,
		Timeout:     time.Duration(profile.TimeoutSec) * time.Second,
	})
	if err != nil {
		return nil, err
	}

	if stagingDir == "" {
		stagingDir = filepath.Join(cfg.DataDir, "tmp")
	}
	if err := os.MkdirAll(stagingDir, 0o700); err != nil {
		return nil, err
	}
	safe := sanitizeName(path.Base(remotePath))
	local := filepath.Join(stagingDir, "preview-"+safe)
	defer func() { _ = os.Remove(local) }()

	size, err := client.DownloadToFile(ctx, remotePath, local)
	if err != nil {
		return nil, fmt.Errorf("下载归档失败：%w", err)
	}

	preview := &RestorePreview{RemotePath: remotePath, TotalSize: size, EntryLimit: 200}

	f, err := os.Open(local)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	if m, err := archive.ReadManifest(f); err == nil {
		preview.Manifest = m
	}
	if _, err := f.Seek(0, 0); err != nil {
		return nil, err
	}
	entries, err := archive.ListEntries(f, preview.EntryLimit+1)
	if err == nil {
		if len(entries) > preview.EntryLimit {
			preview.EntriesMore = true
			entries = entries[:preview.EntryLimit]
		}
		preview.Entries = entries
	}
	return preview, nil
}

// Restore downloads an archive and unpacks it.
func (r *Runner) Restore(ctx context.Context, req RestoreRequest) (*Run, error) {
	cfg := r.store.Snapshot()

	var (
		profile    *config.Profile
		jobName    = "恢复"
		jobID      = req.JobID
		remotePath = req.RemotePath
	)
	if req.JobID != "" {
		job := cfg.FindJob(req.JobID)
		if job == nil {
			return nil, fmt.Errorf("job %q not found", req.JobID)
		}
		profile = cfg.FindProfile(job.Target.ProfileID)
		jobName = job.Name
	} else {
		profile = cfg.FindProfile(req.ProfileID)
		jobName = path.Base(remotePath)
	}
	if profile == nil {
		return nil, errors.New("no usable WebDAV profile for this restore")
	}
	if strings.TrimSpace(remotePath) == "" {
		return nil, errors.New("remote archive path is required")
	}
	if strings.TrimSpace(req.DestDir) == "" {
		return nil, errors.New("destination directory is required")
	}

	password, err := cfg.DecryptPassword(profile)
	if err != nil {
		return nil, err
	}

	// Restores are serialised per destination so two restores cannot interleave
	// writes into the same tree.
	lockKey := "restore:" + req.DestDir
	execCtx, release, err := r.acquire(ctx, lockKey)
	if err != nil {
		return nil, errors.New("another restore into this destination is already running")
	}
	defer release()

	trigger := req.Trigger
	if trigger == "" {
		trigger = TriggerManual
	}
	run := &Run{
		ID:          NewRun(&config.Job{ID: jobID, Name: jobName}, TypeRestore, trigger).ID,
		JobID:       jobID,
		JobName:     jobName,
		Type:        TypeRestore,
		Status:      config.StatusRunning,
		Trigger:     trigger,
		StartedAt:   time.Now(),
		Stage:       "starting",
		RestoreFrom: remotePath,
		RestoreDest: req.DestDir,
		DryRun:      req.DryRun || req.VerifyOnly,
	}
	if err := r.history.Save(run); err != nil {
		return nil, err
	}
	lg, logErr := newRunLogger(r.history.LogPath(run.ID))
	if logErr != nil {
		r.logf("cannot create run log for %s: %v", run.ID, logErr)
	}
	defer lg.Close()

	lg.Printf("=== 恢复任务开始 ===")
	lg.Printf("远端归档：%s", remotePath)
	lg.Printf("目标目录：%s", req.DestDir)
	if req.DryRun {
		lg.Printf("模式：试运行（不写入任何文件）")
	} else if req.VerifyOnly {
		lg.Printf("模式：仅校验")
	} else {
		lg.Printf("覆盖已存在文件：%v", req.Overwrite)
	}

	client, err := dav.New(dav.Config{
		BaseURL:     profile.URL,
		Username:    profile.Username,
		Password:    password,
		InsecureTLS: profile.InsecureTLS,
		Timeout:     time.Duration(profile.TimeoutSec) * time.Second,
	})
	if err != nil {
		finishRun(run, err)
		lg.Printf("=== 失败：%v ===", err)
		_ = r.history.Save(run)
		return run, nil
	}

	execErr := r.executeRestore(execCtx, client, &req, run, lg)
	finishRun(run, execErr)
	if execErr != nil {
		lg.Printf("      失败原因：%s", execErr)
	}
	lg.Printf("=== 结束：%s  用时 %s ===", statusZh(run.Status), time.Duration(run.DurationMS)*time.Millisecond)
	_ = r.history.Save(run)
	r.history.Prune(0)
	return run, nil
}

func (r *Runner) executeRestore(ctx context.Context, client *dav.Client, req *RestoreRequest,
	run *Run, lg *RunLogger) error {

	cfg := r.store.Snapshot()

	// 1. Fetch the archive.
	stagingDir := req.StagingDir
	if stagingDir == "" {
		stagingDir = filepath.Join(cfg.DataDir, "tmp")
	}
	if err := os.MkdirAll(stagingDir, 0o700); err != nil {
		return fmt.Errorf("cannot create staging directory: %w", err)
	}
	local := filepath.Join(stagingDir, run.ID+"-"+sanitizeName(path.Base(req.RemotePath)))
	defer func() { _ = os.Remove(local) }()

	run.Stage = "downloading"
	lg.Printf("[1/3] 下载归档到本地暂存 ...")
	start := time.Now()
	size, err := client.DownloadToFile(ctx, req.RemotePath, local)
	if err != nil {
		return fmt.Errorf("下载失败：%w", err)
	}
	run.ArchiveSize = size
	rate := float64(size) / 1024 / 1024 / time.Since(start).Seconds()
	lg.Printf("      已下载 %s，用时 %s（%.2f MB/s）", humanBytes(size), time.Since(start).Round(time.Second), rate)

	// 2. Validate the archive before touching the destination.
	run.Stage = "verifying"
	lg.Printf("[2/3] 校验归档 ...")
	f, err := os.Open(local)
	if err != nil {
		return err
	}
	manifest, mErr := archive.ReadManifest(f)
	_ = f.Close()
	if mErr != nil {
		lg.Printf("      警告：未能读取归档清单（%v）；将按普通 tar 处理", mErr)
	} else {
		lg.Printf("      清单正常：由 %s 于 %s 创建，包含 %d 个文件（原始 %s）",
			manifest.Tool, manifest.CreatedAt.Format("2006-01-02 15:04:05"),
			manifest.FileCount, humanBytes(manifest.TotalBytes))
		if manifest.Hostname != "" {
			lg.Printf("      来源主机：%s", manifest.Hostname)
		}
		if len(manifest.Roots) > 0 {
			lg.Printf("      原始路径：%s", strings.Join(manifest.Roots, ", "))
		}
	}

	if req.VerifyOnly {
		lg.Printf("      仅校验模式，到此结束")
		run.Stage = "done"
		return nil
	}

	// 3. Extract.
	run.Stage = "extracting"
	mode := "解包"
	if req.DryRun {
		mode = "试运行（只列出，不写入）"
	}
	lg.Printf("[3/3] %s到 %s ...", mode, req.DestDir)

	f, err = os.Open(local)
	if err != nil {
		return err
	}
	defer f.Close()

	lastSave := time.Now()
	progress := func(p archive.Progress) {
		run.FileCount = p.Files
		run.RawSize = p.Bytes
		if time.Since(lastSave) > time.Second {
			lastSave = time.Now()
			_ = r.history.Save(run)
		}
	}

	stats, err := archive.Extract(ctx, f, req.DestDir, archive.ExtractOptions{
		StripComponents: req.StripComponents,
		Overwrite:       req.Overwrite,
		DryRun:          req.DryRun,
	}, progress)
	if err != nil {
		return fmt.Errorf("解包失败：%w", err)
	}

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
	}

	if req.DryRun {
		lg.Printf("      试运行完成：将写入 %d 个文件 / %d 个目录，跳过 %d 个已存在文件",
			stats.Files, stats.Dirs, stats.Skipped)
	} else {
		lg.Printf("      已恢复 %d 个文件 / %d 个目录（跳过 %d 个），共 %s",
			stats.Files, stats.Dirs, stats.Skipped, humanBytes(stats.TotalBytes))
	}
	for _, e := range run.Errors {
		lg.Printf("      - %s", e)
	}
	return nil
}

// RemoteArchive is one archive file found on a WebDAV endpoint.
type RemoteArchive struct {
	Name     string    `json:"name"`
	Path     string    `json:"path"`
	Size     int64     `json:"size"`
	Modified time.Time `json:"modified"`
	Profile  string    `json:"profile_id"`
	JobID    string    `json:"job_id,omitempty"`
	JobName  string    `json:"job_name,omitempty"`
}

// ListArchives enumerates backup archives under a profile's target directory.
// When jobID is set, only that job's archives are returned.
func (r *Runner) ListArchives(ctx context.Context, profileID, dir, jobID string) ([]RemoteArchive, error) {
	cfg := r.store.Snapshot()
	profile := cfg.FindProfile(profileID)
	if profile == nil {
		return nil, fmt.Errorf("profile %q not found", profileID)
	}
	password, err := cfg.DecryptPassword(profile)
	if err != nil {
		return nil, err
	}
	client, err := dav.New(dav.Config{
		BaseURL:     profile.URL,
		Username:    profile.Username,
		Password:    password,
		InsecureTLS: profile.InsecureTLS,
		Timeout:     time.Duration(profile.TimeoutSec) * time.Second,
	})
	if err != nil {
		return nil, err
	}

	dir = strings.Trim(strings.ReplaceAll(dir, "\\", "/"), "/")

	// Map archive prefixes to jobs so the UI can attribute each file.
	prefixes := map[string]struct{ jobID, jobName string }{}
	for i := range cfg.Jobs {
		j := &cfg.Jobs[i]
		if j.Target.ProfileID != profileID {
			continue
		}
		if dir != "" && strings.Trim(strings.ReplaceAll(j.Target.Dir, "\\", "/"), "/") != dir {
			continue
		}
		if jobID != "" && j.ID != jobID {
			continue
		}
		prefixes[sanitizeName(j.Name)+"-"] = struct{ jobID, jobName string }{j.ID, j.Name}
	}

	entries, err := client.List(ctx, dir)
	if err != nil {
		if errors.Is(err, dav.ErrNotFound) {
			return []RemoteArchive{}, nil
		}
		return nil, err
	}

	out := make([]RemoteArchive, 0, len(entries))
	for _, e := range entries {
		if e.IsDir {
			continue
		}
		if !strings.HasSuffix(e.Name, ".tar.gz") && !strings.HasSuffix(e.Name, ".tar") {
			continue
		}
		item := RemoteArchive{
			Name:     e.Name,
			Path:     path.Join(dir, e.Name),
			Size:     e.Size,
			Modified: e.Modified,
			Profile:  profileID,
		}
		if jobID != "" {
			item.JobID, item.JobName = jobID, jobNameOf(cfg, jobID)
		} else {
			for prefix, owner := range prefixes {
				if strings.HasPrefix(e.Name, prefix) {
					item.JobID, item.JobName = owner.jobID, owner.jobName
					break
				}
			}
		}
		out = append(out, item)
	}
	return out, nil
}

func jobNameOf(cfg *config.Config, id string) string {
	if j := cfg.FindJob(id); j != nil {
		return j.Name
	}
	return ""
}

// BrowseLocal lists a directory on the local filesystem for the path picker.
// It returns only direct children.
func BrowseLocal(ctx context.Context, dir string, showHidden bool) ([]LocalEntry, error) {
	if dir == "" {
		dir = "/"
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	out := make([]LocalEntry, 0, len(entries))
	for _, e := range entries {
		name := e.Name()
		if !showHidden && strings.HasPrefix(name, ".") {
			continue
		}
		full := filepath.Join(dir, name)
		info, err := e.Info()
		if err != nil {
			// Unreadable entries (permission, dangling link) are reported as
			// unknown rather than dropped, so the tree stays navigable.
			out = append(out, LocalEntry{Name: name, Path: full, Error: err.Error()})
			continue
		}
		le := LocalEntry{
			Name:     name,
			Path:     full,
			IsDir:    info.IsDir(),
			Size:     info.Size(),
			Modified: info.ModTime(),
			Mode:     info.Mode().String(),
		}
		if info.Mode()&os.ModeSymlink != 0 {
			le.IsSymlink = true
			if target, err := filepath.EvalSymlinks(full); err == nil {
				if ti, err := os.Stat(target); err == nil {
					le.IsDir = ti.IsDir()
				}
			}
		}
		out = append(out, le)
	}
	return out, nil
}

// LocalEntry is one filesystem entry for the path picker.
type LocalEntry struct {
	Name      string    `json:"name"`
	Path      string    `json:"path"`
	IsDir     bool      `json:"is_dir"`
	IsSymlink bool      `json:"is_symlink,omitempty"`
	Size      int64     `json:"size"`
	Modified  time.Time `json:"modified"`
	Mode      string    `json:"mode"`
	Error     string    `json:"error,omitempty"`
}
