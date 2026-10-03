// Package api exposes the HTTP surface: a JSON API under /api plus the
// embedded single-page console.
package api

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"runtime"
	"strings"
	"time"

	"github.com/qzrsa/qzrs-webdav-backup/internal/config"
	"github.com/qzrsa/qzrs-webdav-backup/internal/engine"
	"github.com/qzrsa/qzrs-webdav-backup/internal/scheduler"
)

// Version is stamped at build time with -ldflags.
var Version = "dev"

// MaxBodyBytes caps every JSON request body.
const MaxBodyBytes = 4 << 20

// Deps are the collaborators a Server needs.
type Deps struct {
	Store      *config.Store
	Runner     *engine.Runner
	Scheduler  *scheduler.Scheduler
	Web        fs.FS
	Logf       func(string, ...any)
	TrustProxy bool
	StartedAt  time.Time
}

// Server holds the HTTP handlers.
type Server struct {
	store      *config.Store
	runner     *engine.Runner
	sched      *scheduler.Scheduler
	web        fs.FS
	webETags   map[string]string
	logf       func(string, ...any)
	sessions   *sessionStore
	limiter    *loginLimiter
	trustProxy bool
	startedAt  time.Time
}

// New builds a Server.
func New(d Deps) *Server {
	if d.Logf == nil {
		d.Logf = func(string, ...any) {}
	}
	if d.StartedAt.IsZero() {
		d.StartedAt = time.Now()
	}
	ttl := time.Duration(d.Store.Snapshot().SessionTTLHours) * time.Hour
	return &Server{
		store:      d.Store,
		runner:     d.Runner,
		sched:      d.Scheduler,
		web:        d.Web,
		webETags:   buildAssetETags(d.Web),
		logf:       d.Logf,
		sessions:   newSessionStore(ttl),
		limiter:    newLoginLimiter(),
		trustProxy: d.TrustProxy,
		startedAt:  d.StartedAt,
	}
}

// buildAssetETags 预先算好每个静态资源的内容哈希，用做强校验的 ETag。
//
// 为什么不能用长缓存：app.js / style.css 的文件名里没有内容指纹，若给它们设
// max-age，升级二进制后浏览器会继续用旧版脚本去配新接口 —— 典型症状就是控制台
// 停在「正在载入控制台…」或行为错乱。embed.FS 的文件 ModTime 是零值，
// http.ServeContent 无法据此做 Last-Modified 协商，所以这里改用内容哈希。
func buildAssetETags(web fs.FS) map[string]string {
	out := map[string]string{}
	if web == nil {
		return out
	}
	_ = fs.WalkDir(web, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil //nolint:nilerr // 单个条目读不到就跳过，不影响其它资源
		}
		b, rerr := fs.ReadFile(web, p)
		if rerr != nil {
			return nil
		}
		sum := sha256.Sum256(b)
		out["/"+strings.TrimPrefix(p, "/")] = `"` + hex.EncodeToString(sum[:8]) + `"`
		return nil
	})
	return out
}

// Handler returns the fully wired HTTP handler.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()

	// --- public endpoints -------------------------------------------------
	mux.HandleFunc("GET /api/health", s.handleHealth)
	mux.HandleFunc("POST /api/login", s.requireJSON(s.handleLogin))
	mux.HandleFunc("POST /api/logout", s.requireJSON(s.handleLogout))
	mux.HandleFunc("GET /api/me", s.handleMe)

	// --- authenticated endpoints ------------------------------------------
	auth := func(pattern string, h http.HandlerFunc) {
		mux.HandleFunc(pattern, s.requireJSON(s.requireAuth(h)))
	}

	auth("GET /api/status", s.handleStatus)
	auth("GET /api/settings", s.handleGetSettings)
	auth("PUT /api/settings", s.handleUpdateSettings)
	auth("POST /api/password", s.handleChangePassword)

	auth("GET /api/profiles", s.handleListProfiles)
	auth("POST /api/profiles", s.handleCreateProfile)
	auth("GET /api/profiles/{id}", s.handleGetProfile)
	auth("PUT /api/profiles/{id}", s.handleUpdateProfile)
	auth("DELETE /api/profiles/{id}", s.handleDeleteProfile)
	auth("POST /api/profiles/{id}/test", s.handleTestProfile)

	auth("GET /api/jobs", s.handleListJobs)
	auth("POST /api/jobs", s.handleCreateJob)
	auth("GET /api/jobs/{id}", s.handleGetJob)
	auth("PUT /api/jobs/{id}", s.handleUpdateJob)
	auth("DELETE /api/jobs/{id}", s.handleDeleteJob)
	auth("POST /api/jobs/{id}/run", s.handleRunJob)
	auth("POST /api/jobs/{id}/cancel", s.handleCancelJob)

	auth("GET /api/runs", s.handleListRuns)
	auth("GET /api/runs/{id}", s.handleGetRun)
	auth("DELETE /api/runs/{id}", s.handleDeleteRun)
	auth("DELETE /api/runs", s.handleClearRuns)
	auth("GET /api/runs/{id}/log", s.handleRunLog)

	auth("GET /api/archives", s.handleListArchives)
	auth("POST /api/preview", s.handlePreview)
	auth("POST /api/restore", s.handleRestore)

	auth("GET /api/fs/list", s.handleFSList)
	auth("GET /api/fs/usage", s.handleFSUsage)

	auth("POST /api/schedule/validate", s.handleValidateSchedule)

	// --- console ----------------------------------------------------------
	mux.Handle("GET /", s.webHandler())

	return s.withRecovery(s.withLogging(mux))
}

func (s *Server) webHandler() http.Handler {
	fileServer := http.FileServer(http.FS(s.web))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/") {
			writeError(w, http.StatusNotFound, "not_found", "no such API endpoint")
			return
		}
		// 外壳永不缓存；静态资源改走 ETag 协商：内容没变返回 304，一变立刻生效。
		// 原来给资源设的是 public, max-age=3600，但文件名没有内容指纹，升级二进制
		// 之后浏览器会继续用旧脚本去配新接口（见 buildAssetETags 的注释）。
		name := r.URL.Path
		if name == "/" {
			name = "/index.html"
		}
		if etag, ok := s.webETags[name]; ok && name != "/index.html" {
			w.Header().Set("Cache-Control", "no-cache")
			w.Header().Set("ETag", etag)
		} else {
			w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
		}
		// Security headers: the console is a self-contained SPA, so a strict
		// policy costs nothing.
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Referrer-Policy", "no-referrer")
		fileServer.ServeHTTP(w, r)
	})
}

// ---------------------------------------------------------------------------
// Middleware
// ---------------------------------------------------------------------------

func (s *Server) withLogging(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rec, r)
		// Only log API traffic and failures; static asset noise is unhelpful on
		// a router with a small log ring buffer.
		if strings.HasPrefix(r.URL.Path, "/api/") || rec.status >= 400 {
			s.logf("%s %s %d %s", r.Method, r.URL.Path, rec.status, time.Since(start).Round(time.Millisecond))
		}
	})
}

func (s *Server) withRecovery(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if rec := recover(); rec != nil {
				buf := make([]byte, 8192)
				n := runtime.Stack(buf, false)
				s.logf("PANIC on %s %s: %v\n%s", r.Method, r.URL.Path, rec, buf[:n])
				writeError(w, http.StatusInternalServerError, "panic", "内部错误，请查看服务日志")
			}
		}()
		next.ServeHTTP(w, r)
	})
}

type statusRecorder struct {
	http.ResponseWriter
	status  int
	written bool
}

func (r *statusRecorder) WriteHeader(code int) {
	if !r.written {
		r.status = code
		r.written = true
	}
	r.ResponseWriter.WriteHeader(code)
}

func (r *statusRecorder) Write(b []byte) (int, error) {
	r.written = true
	return r.ResponseWriter.Write(b)
}

// ---------------------------------------------------------------------------
// Response helpers
// ---------------------------------------------------------------------------

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	if v == nil {
		return
	}
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(v)
}

type apiError struct {
	Error   string `json:"error"`
	Message string `json:"message"`
}

func writeError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, apiError{Error: code, Message: message})
}

// decodeJSON reads a bounded JSON body, rejecting unknown fields so typos in
// client payloads surface as errors instead of being silently ignored.
func decodeJSON(w http.ResponseWriter, r *http.Request, dst any) bool {
	defer func() { _, _ = io.Copy(io.Discard, io.LimitReader(r.Body, 1<<20)) }()
	dec := json.NewDecoder(io.LimitReader(r.Body, MaxBodyBytes))
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			writeError(w, http.StatusRequestEntityTooLarge, "too_large", "请求体过大")
			return false
		}
		writeError(w, http.StatusBadRequest, "bad_json", "无法解析请求内容："+err.Error())
		return false
	}
	return true
}

// ---------------------------------------------------------------------------
// Health & status
// ---------------------------------------------------------------------------

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"status":  "ok",
		"version": Version,
		"uptime":  time.Since(s.startedAt).Round(time.Second).String(),
	})
}

type statusResponse struct {
	Version    string                   `json:"version"`
	StartedAt  time.Time                `json:"started_at"`
	UptimeSec  int64                    `json:"uptime_seconds"`
	Listen     string                   `json:"listen"`
	DataDir    string                   `json:"data_dir"`
	TempDir    string                   `json:"temp_dir"`
	GoVersion  string                   `json:"go_version"`
	Platform   string                   `json:"platform"`
	Jobs       jobCounts                `json:"jobs"`
	Running    []string                 `json:"running_jobs"`
	RecentRuns []*engine.Run            `json:"recent_runs"`
	Disk       *diskInfo                `json:"disk,omitempty"`
	Schedules  []scheduler.ScheduleInfo `json:"schedules"`
}

type jobCounts struct {
	Total   int `json:"total"`
	Enabled int `json:"enabled"`
}

type diskInfo struct {
	Path      string  `json:"path"`
	Total     uint64  `json:"total"`
	Used      uint64  `json:"used"`
	Free      uint64  `json:"free"`
	UsedPct   float64 `json:"used_percent"`
	Supported bool    `json:"supported"`
	Error     string  `json:"error,omitempty"`
}

func (s *Server) handleStatus(w http.ResponseWriter, r *http.Request) {
	cfg := s.store.Snapshot()

	resp := statusResponse{
		Version:    Version,
		StartedAt:  s.startedAt,
		UptimeSec:  int64(time.Since(s.startedAt).Seconds()),
		Listen:     cfg.Listen,
		DataDir:    cfg.DataDir,
		TempDir:    cfg.TempDir,
		GoVersion:  runtime.Version(),
		Platform:   runtime.GOOS + "/" + runtime.GOARCH,
		Running:    s.runner.RunningJobs(),
		RecentRuns: s.runner.History().List(engine.ListOptions{Limit: 10}),
		Schedules:  s.sched.Info(),
	}
	for i := range cfg.Jobs {
		resp.Jobs.Total++
		if cfg.Jobs[i].Enabled {
			resp.Jobs.Enabled++
		}
	}
	if resp.Running == nil {
		resp.Running = []string{}
	}
	if resp.RecentRuns == nil {
		resp.RecentRuns = []*engine.Run{}
	}

	resp.Disk = s.diskFor(cfg.DataDir)
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) diskFor(path string) *diskInfo {
	total, free, err := diskUsage(path)
	if err != nil {
		return &diskInfo{Path: path, Supported: false, Error: err.Error()}
	}
	used := total - free
	pct := 0.0
	if total > 0 {
		pct = float64(used) / float64(total) * 100
	}
	return &diskInfo{Path: path, Total: total, Used: used, Free: free, UsedPct: pct, Supported: true}
}

// ---------------------------------------------------------------------------
// Settings
// ---------------------------------------------------------------------------

type settingsResponse struct {
	Version         int    `json:"version"`
	Listen          string `json:"listen"`
	DataDir         string `json:"data_dir"`
	TempDir         string `json:"temp_dir"`
	SessionTTLHours int    `json:"session_ttl_hours"`
	HistoryKeep     int    `json:"history_keep"`
	MaxConcurrent   int    `json:"max_concurrent"`
	TrustedProxy    bool             `json:"trusted_proxy"`
	Username        string           `json:"username"`
	ConfigPath      string           `json:"config_path"`
	Notify          *config.NotifyConfig `json:"notify,omitempty"`
}

func (s *Server) handleGetSettings(w http.ResponseWriter, r *http.Request) {
	cfg := s.store.Snapshot()
	writeJSON(w, http.StatusOK, settingsResponse{
		Version:         cfg.Version,
		Listen:          cfg.Listen,
		DataDir:         cfg.DataDir,
		TempDir:         cfg.TempDir,
		SessionTTLHours: cfg.SessionTTLHours,
		HistoryKeep:     cfg.HistoryKeep,
		MaxConcurrent:   cfg.MaxConcurrent,
		TrustedProxy:    cfg.TrustedProxy,
		Username:        cfg.Admin.Username,
		ConfigPath:      s.store.Path(),
		Notify:          cfg.Notify,
	})
}

type settingsUpdate struct {
	TempDir         *string             `json:"temp_dir"`
	SessionTTLHours *int                `json:"session_ttl_hours"`
	HistoryKeep     *int                `json:"history_keep"`
	TrustedProxy    *bool               `json:"trusted_proxy"`
	Username        *string             `json:"username"`
	Notify          *config.NotifyConfig `json:"notify"`
}

func (s *Server) handleUpdateSettings(w http.ResponseWriter, r *http.Request) {
	var req settingsUpdate
	if !decodeJSON(w, r, &req) {
		return
	}
	// Listen and DataDir are intentionally not editable at runtime: changing
	// them requires a restart and a possibly different storage location.
	if _, err := s.store.Update(func(c *config.Config) error {
		if req.TempDir != nil {
			c.TempDir = strings.TrimSpace(*req.TempDir)
		}
		if req.SessionTTLHours != nil && *req.SessionTTLHours > 0 {
			c.SessionTTLHours = *req.SessionTTLHours
		}
		if req.HistoryKeep != nil && *req.HistoryKeep > 0 {
			c.HistoryKeep = *req.HistoryKeep
		}
		if req.TrustedProxy != nil {
			c.TrustedProxy = *req.TrustedProxy
		}
		if req.Username != nil && strings.TrimSpace(*req.Username) != "" {
			c.Admin.Username = strings.TrimSpace(*req.Username)
		}
		if req.Notify != nil {
			req.Notify.URL = strings.TrimSpace(req.Notify.URL)
			req.Notify.ChatID = strings.TrimSpace(req.Notify.ChatID)
			c.Notify = req.Notify
		}
		return nil
	}); err != nil {
		writeError(w, http.StatusBadRequest, "update_failed", err.Error())
		return
	}
	s.handleGetSettings(w, r)
}

// ---------------------------------------------------------------------------
// Scheduler helper endpoint
// ---------------------------------------------------------------------------

type scheduleValidateRequest struct {
	Mode     string `json:"mode"`
	Cron     string `json:"cron"`
	Interval string `json:"interval"`
}

func (s *Server) handleValidateSchedule(w http.ResponseWriter, r *http.Request) {
	var req scheduleValidateRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	expr := req.Cron
	if req.Mode == config.ScheduleInterval {
		expr = req.Interval
	}
	runs, err := scheduler.NextRunsFor(req.Mode, expr, 5)
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"valid": false, "error": err.Error()})
		return
	}
	formatted := make([]string, 0, len(runs))
	for _, t := range runs {
		formatted = append(formatted, t.Format("2006-01-02 15:04:05"))
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"valid":     true,
		"next_runs": formatted,
	})
}

var _ = fmt.Sprintf
