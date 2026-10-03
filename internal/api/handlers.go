package api

import (
	"context"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/qzrsa/qzrs-webdav-backup/internal/config"
	"github.com/qzrsa/qzrs-webdav-backup/internal/cryptoutil"
	"github.com/qzrsa/qzrs-webdav-backup/internal/dav"
	"github.com/qzrsa/qzrs-webdav-backup/internal/engine"
)

// ---------------------------------------------------------------------------
// WebDAV profiles
// ---------------------------------------------------------------------------

type profileInput struct {
	Name          string `json:"name"`
	URL           string `json:"url"`
	Username      string `json:"username"`
	Password      string `json:"password"`
	ClearPassword bool   `json:"clear_password"`
	InsecureTLS   bool   `json:"insecure_tls"`
	TimeoutSec    int    `json:"timeout_sec"`
}

// profileView is what the API returns: never the password, not even sealed.
type profileView struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	URL         string `json:"url"`
	Username    string `json:"username"`
	HasPassword bool   `json:"has_password"`
	InsecureTLS bool   `json:"insecure_tls"`
	TimeoutSec  int    `json:"timeout_sec"`
	UsedByJobs  int    `json:"used_by_jobs"`
	BaseDir     string `json:"base_dir"`
}

func (s *Server) viewProfile(cfg *config.Config, p *config.Profile) profileView {
	v := profileView{
		ID:          p.ID,
		Name:        p.Name,
		URL:         p.URL,
		Username:    p.Username,
		HasPassword: p.HasPassword(),
		InsecureTLS: p.InsecureTLS,
		TimeoutSec:  p.TimeoutSec,
	}
	for i := range cfg.Jobs {
		if cfg.Jobs[i].Target.ProfileID == p.ID {
			v.UsedByJobs++
		}
	}
	return v
}

func (s *Server) handleListProfiles(w http.ResponseWriter, r *http.Request) {
	cfg := s.store.Snapshot()
	views := make([]profileView, 0, len(cfg.Profiles))
	for i := range cfg.Profiles {
		views = append(views, s.viewProfile(cfg, &cfg.Profiles[i]))
	}
	writeJSON(w, http.StatusOK, map[string]any{"profiles": views})
}

func (s *Server) handleGetProfile(w http.ResponseWriter, r *http.Request) {
	cfg := s.store.Snapshot()
	p := cfg.FindProfile(r.PathValue("id"))
	if p == nil {
		writeError(w, http.StatusNotFound, "not_found", "配置不存在")
		return
	}
	writeJSON(w, http.StatusOK, s.viewProfile(cfg, p))
}

func validateProfileInput(in *profileInput) error {
	in.Name = strings.TrimSpace(in.Name)
	in.URL = strings.TrimSpace(in.URL)
	if in.Name == "" {
		return errors.New("名称不能为空")
	}
	if in.URL == "" {
		return errors.New("WebDAV 地址不能为空")
	}
	if !strings.HasPrefix(in.URL, "http://") && !strings.HasPrefix(in.URL, "https://") {
		return errors.New("地址必须以 http:// 或 https:// 开头")
	}
	if in.TimeoutSec <= 0 {
		in.TimeoutSec = 120
	}
	if in.TimeoutSec > 3600 {
		in.TimeoutSec = 3600
	}
	return nil
}

func (s *Server) handleCreateProfile(w http.ResponseWriter, r *http.Request) {
	var in profileInput
	if !decodeJSON(w, r, &in) {
		return
	}
	if err := validateProfileInput(&in); err != nil {
		writeError(w, http.StatusBadRequest, "invalid", err.Error())
		return
	}

	var created *config.Profile
	updated, err := s.store.Update(func(c *config.Config) error {
		sealed, err := c.SealPassword(in.Password)
		if err != nil {
			return err
		}
		p := config.Profile{
			ID:          cryptoutil.RandomHex(6),
			Name:        in.Name,
			URL:         in.URL,
			Username:    in.Username,
			PasswordEnc: sealed,
			InsecureTLS: in.InsecureTLS,
			TimeoutSec:  in.TimeoutSec,
		}
		c.Profiles = append(c.Profiles, p)
		cp := p
		created = &cp
		return nil
	})
	if err != nil {
		writeError(w, http.StatusBadRequest, "create_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, s.viewProfile(updated, created))
}

func (s *Server) handleUpdateProfile(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var in profileInput
	if !decodeJSON(w, r, &in) {
		return
	}
	if err := validateProfileInput(&in); err != nil {
		writeError(w, http.StatusBadRequest, "invalid", err.Error())
		return
	}

	var result *config.Profile
	updated, err := s.store.Update(func(c *config.Config) error {
		p := c.FindProfile(id)
		if p == nil {
			return errors.New("配置不存在")
		}
		p.Name = in.Name
		p.URL = in.URL
		p.Username = in.Username
		p.InsecureTLS = in.InsecureTLS
		p.TimeoutSec = in.TimeoutSec
		switch {
		case in.ClearPassword:
			p.PasswordEnc = ""
		case in.Password != "":
			sealed, err := c.SealPassword(in.Password)
			if err != nil {
				return err
			}
			p.PasswordEnc = sealed
		}
		cp := *p
		result = &cp
		return nil
	})
	if err != nil {
		status := http.StatusBadRequest
		if err.Error() == "配置不存在" {
			status = http.StatusNotFound
		}
		writeError(w, status, "update_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, s.viewProfile(updated, result))
}

func (s *Server) handleDeleteProfile(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	_, err := s.store.Update(func(c *config.Config) error {
		idx := -1
		for i := range c.Profiles {
			if c.Profiles[i].ID == id {
				idx = i
				break
			}
		}
		if idx < 0 {
			return errors.New("配置不存在")
		}
		for i := range c.Jobs {
			if c.Jobs[i].Target.ProfileID == id {
				return errors.New("仍有任务在使用该配置（" + c.Jobs[i].Name + "），请先修改或删除该任务")
			}
		}
		c.Profiles = append(c.Profiles[:idx], c.Profiles[idx+1:]...)
		return nil
	})
	if err != nil {
		status := http.StatusBadRequest
		if err.Error() == "配置不存在" {
			status = http.StatusNotFound
		}
		writeError(w, status, "delete_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

type testProfileRequest struct {
	URL         string `json:"url"`
	Username    string `json:"username"`
	Password    string `json:"password"`
	InsecureTLS bool   `json:"insecure_tls"`
	TimeoutSec  int    `json:"timeout_sec"`
	Dir         string `json:"dir"`
}

// handleTestProfile verifies connectivity. Values in the request body take
// precedence over the stored profile, so the UI can test before saving.
func (s *Server) handleTestProfile(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var in testProfileRequest
	// The body is optional: an empty POST means "test the stored profile".
	if r.ContentLength > 0 && !decodeJSON(w, r, &in) {
		return
	}

	cfg := s.store.Snapshot()
	var stored *config.Profile
	if id != "" && id != "new" {
		stored = cfg.FindProfile(id)
		if stored == nil {
			writeError(w, http.StatusNotFound, "not_found", "配置不存在")
			return
		}
	}

	url := strings.TrimSpace(in.URL)
	if url == "" && stored != nil {
		url = stored.URL
	}
	username := in.Username
	if username == "" && stored != nil {
		username = stored.Username
	}
	timeout := in.TimeoutSec
	if timeout <= 0 && stored != nil {
		timeout = stored.TimeoutSec
	}
	if timeout <= 0 {
		timeout = 120
	}
	insecure := in.InsecureTLS || (stored != nil && stored.InsecureTLS)

	password := in.Password
	if password == "" && stored != nil {
		decrypted, err := cfg.DecryptPassword(stored)
		if err != nil {
			writeError(w, http.StatusBadRequest, "decrypt_failed",
				"无法读取已保存的密码，请重新填写并保存："+err.Error())
			return
		}
		password = decrypted
	}
	if url == "" {
		writeError(w, http.StatusBadRequest, "invalid", "缺少 WebDAV 地址")
		return
	}

	client, err := dav.New(dav.Config{
		BaseURL:     url,
		Username:    username,
		Password:    password,
		InsecureTLS: insecure,
		Timeout:     time.Duration(timeout) * time.Second,
	})
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"ok": false, "error": err.Error()})
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), time.Duration(timeout)*time.Second)
	defer cancel()

	if err := client.Ping(ctx); err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"ok": false, "error": err.Error()})
		return
	}

	result := map[string]any{"ok": true, "message": "连接成功，认证通过"}

	// Optional: report what is already in the chosen directory.
	dir := strings.Trim(strings.ReplaceAll(in.Dir, "\\", "/"), "/")
	if entries, err := client.List(ctx, dir); err == nil {
		archives := 0
		var total int64
		names := make([]string, 0, len(entries))
		for _, e := range entries {
			kind := "文件"
			if e.IsDir {
				kind = "目录"
			}
			names = append(names, kind+" "+e.Name)
			if !e.IsDir && (strings.HasSuffix(e.Name, ".tar.gz") || strings.HasSuffix(e.Name, ".tar")) {
				archives++
				total += e.Size
			}
		}
		result["entries"] = len(entries)
		result["names"] = names
		result["archives"] = archives
		result["archive_bytes"] = total
		result["base_dir"] = dir
	} else if !errors.Is(err, dav.ErrNotFound) {
		result["list_warning"] = err.Error()
	} else if dir != "" {
		result["list_warning"] = "目录尚不存在，首次备份时会自动创建"
	} else {
		result["list_warning"] = "WebDAV 地址指向的目录尚不存在；" +
			"请先在 OpenList/网盘里创建它，或把目录段从地址移到任务的「目标目录」（后者会自动创建）"
	}

	writeJSON(w, http.StatusOK, result)
}

// ---------------------------------------------------------------------------
// Jobs
// ---------------------------------------------------------------------------

func (s *Server) handleListJobs(w http.ResponseWriter, r *http.Request) {
	cfg := s.store.Snapshot()
	cfg.SortJobs()
	jobs := cfg.Jobs
	if jobs == nil {
		jobs = []config.Job{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"jobs": jobs})
}

func (s *Server) handleGetJob(w http.ResponseWriter, r *http.Request) {
	cfg := s.store.Snapshot()
	job := cfg.FindJob(r.PathValue("id"))
	if job == nil {
		writeError(w, http.StatusNotFound, "not_found", "任务不存在")
		return
	}
	writeJSON(w, http.StatusOK, job)
}

// normaliseJob applies defaults and cleans user input.
func normaliseJob(j *config.Job) {
	j.Name = strings.TrimSpace(j.Name)
	j.Comment = strings.TrimSpace(j.Comment)
	j.Target.Dir = strings.Trim(strings.ReplaceAll(j.Target.Dir, "\\", "/"), "/")

	cleaned := make([]string, 0, len(j.Source.Paths))
	seen := map[string]bool{}
	for _, p := range j.Source.Paths {
		p = strings.TrimSpace(p)
		if p == "" || seen[p] {
			continue
		}
		seen[p] = true
		cleaned = append(cleaned, p)
	}
	j.Source.Paths = cleaned
	j.Source.Include = trimList(j.Source.Include)
	j.Source.Exclude = trimList(j.Source.Exclude)

	if j.Options.Compression == "" {
		j.Options.Compression = config.CompressionGzip
	}
	if j.Options.GzipLevel <= 0 || j.Options.GzipLevel > 9 {
		j.Options.GzipLevel = 6
	}
	j.Options.TempDir = strings.TrimSpace(j.Options.TempDir)
	if j.Schedule.Mode == "" {
		j.Schedule.Mode = config.ScheduleManual
	}
	j.Schedule.Cron = strings.TrimSpace(j.Schedule.Cron)
	j.Schedule.Interval = strings.TrimSpace(j.Schedule.Interval)
	if j.Retention.Keep < 0 {
		j.Retention.Keep = 0
	}
}

func trimList(in []string) []string {
	out := make([]string, 0, len(in))
	for _, s := range in {
		s = strings.TrimSpace(s)
		if s != "" {
			out = append(out, s)
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func (s *Server) handleCreateJob(w http.ResponseWriter, r *http.Request) {
	var job config.Job
	if !decodeJSON(w, r, &job) {
		return
	}
	normaliseJob(&job)
	if err := validateJob(&job); err != nil {
		writeError(w, http.StatusBadRequest, "invalid", err.Error())
		return
	}

	now := time.Now()
	job.ID = cryptoutil.RandomHex(6)
	job.CreatedAt = now
	job.UpdatedAt = now

	if _, err := s.store.Update(func(c *config.Config) error {
		c.Jobs = append(c.Jobs, job)
		return nil
	}); err != nil {
		writeError(w, http.StatusBadRequest, "create_failed", err.Error())
		return
	}
	s.reloadScheduler()
	writeJSON(w, http.StatusCreated, job)
}

func validateJob(j *config.Job) error {
	if j.Name == "" {
		return errors.New("任务名称不能为空")
	}
	if len(j.Source.Paths) == 0 {
		return errors.New("至少需要一个源路径")
	}
	for _, p := range j.Source.Paths {
		if !strings.HasPrefix(p, "/") {
			return errors.New("源路径必须是绝对路径：" + p)
		}
	}
	if j.Target.ProfileID == "" {
		return errors.New("必须选择一个 WebDAV 配置")
	}
	if j.Options.Compression != config.CompressionGzip && j.Options.Compression != config.CompressionNone {
		return errors.New("压缩方式无效")
	}
	switch j.Schedule.Mode {
	case config.ScheduleCron:
		if j.Schedule.Cron == "" {
			return errors.New("请填写 cron 表达式")
		}
	case config.ScheduleInterval:
		d, err := time.ParseDuration(j.Schedule.Interval)
		if err != nil {
			return errors.New("时间间隔格式无效，例如 6h、30m、24h")
		}
		if d < time.Minute {
			return errors.New("时间间隔不能小于 1 分钟")
		}
	case config.ScheduleManual:
	default:
		return errors.New("调度方式无效")
	}
	return nil
}

func (s *Server) handleUpdateJob(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var job config.Job
	if !decodeJSON(w, r, &job) {
		return
	}
	normaliseJob(&job)
	if err := validateJob(&job); err != nil {
		writeError(w, http.StatusBadRequest, "invalid", err.Error())
		return
	}

	var result *config.Job
	_, err := s.store.Update(func(c *config.Config) error {
		existing := c.FindJob(id)
		if existing == nil {
			return errors.New("任务不存在")
		}
		job.ID = existing.ID
		job.CreatedAt = existing.CreatedAt
		job.UpdatedAt = time.Now()
		job.LastRunAt = existing.LastRunAt
		job.LastStatus = existing.LastStatus
		*existing = job
		cp := job
		result = &cp
		return nil
	})
	if err != nil {
		status := http.StatusBadRequest
		if err.Error() == "任务不存在" {
			status = http.StatusNotFound
		}
		writeError(w, status, "update_failed", err.Error())
		return
	}
	s.reloadScheduler()
	writeJSON(w, http.StatusOK, result)
}

func (s *Server) handleDeleteJob(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if s.runner.Running(id) {
		s.runner.Cancel(id)
	}
	_, err := s.store.Update(func(c *config.Config) error {
		idx := -1
		for i := range c.Jobs {
			if c.Jobs[i].ID == id {
				idx = i
				break
			}
		}
		if idx < 0 {
			return errors.New("任务不存在")
		}
		c.Jobs = append(c.Jobs[:idx], c.Jobs[idx+1:]...)
		return nil
	})
	if err != nil {
		status := http.StatusBadRequest
		if err.Error() == "任务不存在" {
			status = http.StatusNotFound
		}
		writeError(w, status, "delete_failed", err.Error())
		return
	}
	s.reloadScheduler()
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) handleRunJob(w http.ResponseWriter, r *http.Request) {
	jobID := r.PathValue("id")
	cfg := s.store.Snapshot()
	if cfg.FindJob(jobID) == nil {
		writeError(w, http.StatusNotFound, "not_found", "任务不存在")
		return
	}
	if s.runner.Running(jobID) {
		writeError(w, http.StatusConflict, "already_running", "该任务正在执行中")
		return
	}

	// Run in the background: a large backup can take many minutes and must not
	// be tied to the lifetime of this HTTP request. Progress is observed by
	// polling /api/runs.
	go func() {
		if _, err := s.runner.Backup(context.Background(), jobID, engine.TriggerManual); err != nil {
			s.logf("manual backup of job %s could not start: %v", jobID, err)
		}
	}()

	writeJSON(w, http.StatusAccepted, map[string]any{
		"started": true,
		"job_id":  jobID,
	})
}

func (s *Server) handleCancelJob(w http.ResponseWriter, r *http.Request) {
	jobID := r.PathValue("id")
	if !s.runner.Cancel(jobID) {
		writeError(w, http.StatusConflict, "not_running", "该任务当前没有在执行")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// ---------------------------------------------------------------------------
// Runs
// ---------------------------------------------------------------------------

func (s *Server) handleListRuns(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	limit := 50
	if v, err := strconv.Atoi(q.Get("limit")); err == nil && v > 0 && v <= 500 {
		limit = v
	}
	runs := s.runner.History().List(engine.ListOptions{
		JobID: q.Get("job_id"),
		Type:  q.Get("type"),
		Limit: limit,
	})
	if runs == nil {
		runs = []*engine.Run{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"runs": runs})
}

func (s *Server) handleGetRun(w http.ResponseWriter, r *http.Request) {
	run, ok := s.runner.History().Get(r.PathValue("id"))
	if !ok {
		writeError(w, http.StatusNotFound, "not_found", "执行记录不存在")
		return
	}
	writeJSON(w, http.StatusOK, run)
}

func (s *Server) handleDeleteRun(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if _, ok := s.runner.History().Get(id); !ok {
		writeError(w, http.StatusNotFound, "not_found", "执行记录不存在")
		return
	}
	if run, _ := s.runner.History().Get(id); run != nil && run.Status == config.StatusRunning {
		writeError(w, http.StatusConflict, "still_running", "该记录仍在执行中，无法删除")
		return
	}
	if err := s.runner.History().Delete(id); err != nil {
		writeError(w, http.StatusInternalServerError, "delete_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// handleRunLog returns an increment of the run log for live tailing.
func (s *Server) handleRunLog(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	offset := int64(0)
	if v, err := strconv.ParseInt(r.URL.Query().Get("offset"), 10, 64); err == nil && v >= 0 {
		offset = v
	}
	text, size, err := s.runner.History().ReadLog(id, offset, 256<<10)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "read_failed", err.Error())
		return
	}
	run, _ := s.runner.History().Get(id)
	status := ""
	if run != nil {
		status = run.Status
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"id":       id,
		"offset":   offset,
		"size":     size,
		"text":     text,
		"running":  status == config.StatusRunning,
		"finished": run != nil && run.Finished(),
	})
}

// ---------------------------------------------------------------------------
// Archives, preview, restore
// ---------------------------------------------------------------------------

func (s *Server) handleListArchives(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	profileID := q.Get("profile_id")
	dir := q.Get("dir")
	jobID := q.Get("job_id")

	// When only a job is given, derive the profile and directory from it.
	if profileID == "" && jobID != "" {
		cfg := s.store.Snapshot()
		if job := cfg.FindJob(jobID); job != nil {
			profileID = job.Target.ProfileID
			if dir == "" {
				dir = job.Target.Dir
			}
		}
	}
	if profileID == "" {
		writeError(w, http.StatusBadRequest, "missing_params", "需要指定 profile_id 或 job_id")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
	defer cancel()

	items, err := s.runner.ListArchives(ctx, profileID, dir, jobID)
	if err != nil {
		writeError(w, http.StatusBadGateway, "list_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"archives": items})
}

type previewRequest struct {
	ProfileID  string `json:"profile_id"`
	JobID      string `json:"job_id"`
	RemotePath string `json:"remote_path"`
}

func (s *Server) handlePreview(w http.ResponseWriter, r *http.Request) {
	var req previewRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	cfg := s.store.Snapshot()
	profileID := req.ProfileID
	if profileID == "" && req.JobID != "" {
		if job := cfg.FindJob(req.JobID); job != nil {
			profileID = job.Target.ProfileID
		}
	}
	if profileID == "" || req.RemotePath == "" {
		writeError(w, http.StatusBadRequest, "missing_params", "需要 profile_id/job_id 与 remote_path")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Minute)
	defer cancel()

	preview, err := s.runner.Preview(ctx, profileID, req.RemotePath, cfg.TempDir)
	if err != nil {
		writeError(w, http.StatusBadGateway, "preview_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, preview)
}

type restoreRequest struct {
	JobID           string `json:"job_id"`
	ProfileID       string `json:"profile_id"`
	RemotePath      string `json:"remote_path"`
	DestDir         string `json:"dest_dir"`
	Overwrite       bool   `json:"overwrite"`
	DryRun          bool   `json:"dry_run"`
	StripComponents int    `json:"strip_components"`
}

func (s *Server) handleRestore(w http.ResponseWriter, r *http.Request) {
	var req restoreRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.DestDir == "" {
		writeError(w, http.StatusBadRequest, "invalid", "必须指定恢复目标目录")
		return
	}
	if !strings.HasPrefix(req.DestDir, "/") {
		writeError(w, http.StatusBadRequest, "invalid", "恢复目标目录必须是绝对路径")
		return
	}
	if req.StripComponents < 0 {
		req.StripComponents = 0
	}

	// Guard against an accidental destructive restore. Extracting onto / is
	// legitimate for a full system recovery, but it must be deliberate.
	if !req.DryRun {
		if _, err := os.Stat(req.DestDir); err != nil {
			writeError(w, http.StatusBadRequest, "invalid",
				"目标目录不存在："+req.DestDir+"（请先创建，或用试运行确认后手动创建）")
			return
		}
	}

	run, err := s.runner.Restore(context.Background(), engine.RestoreRequest{
		JobID:           req.JobID,
		ProfileID:       req.ProfileID,
		RemotePath:      req.RemotePath,
		DestDir:         req.DestDir,
		Overwrite:       req.Overwrite,
		DryRun:          req.DryRun,
		StripComponents: req.StripComponents,
		Trigger:         engine.TriggerManual,
	})
	if err != nil {
		writeError(w, http.StatusBadRequest, "restore_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{
		"started": true,
		"run":     run,
	})
}

// ---------------------------------------------------------------------------
// Local filesystem browsing
// ---------------------------------------------------------------------------

func (s *Server) handleFSList(w http.ResponseWriter, r *http.Request) {
	dir := r.URL.Query().Get("path")
	if dir == "" {
		dir = "/"
	}
	if !strings.HasPrefix(dir, "/") {
		writeError(w, http.StatusBadRequest, "invalid", "路径必须是绝对路径")
		return
	}
	dir = filepath.Clean(dir)
	showHidden := r.URL.Query().Get("hidden") == "1"

	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()

	entries, err := engine.BrowseLocal(ctx, dir, showHidden)
	if err != nil {
		writeError(w, http.StatusBadRequest, "list_failed", err.Error())
		return
	}
	if entries == nil {
		entries = []engine.LocalEntry{}
	}

	parent := filepath.Dir(dir)
	if parent == dir {
		parent = ""
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"path":    dir,
		"parent":  parent,
		"entries": entries,
	})
}

// handleFSUsage reports free space for a path, used to warn before a big backup.
func (s *Server) handleFSUsage(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Query().Get("path")
	if path == "" {
		path = s.store.Snapshot().DataDir
	}
	writeJSON(w, http.StatusOK, s.diskFor(path))
}

func (s *Server) reloadScheduler() {
	if s.sched == nil {
		return
	}
	if err := s.sched.Reload(); err != nil {
		s.logf("scheduler reload failed: %v", err)
	}
}
