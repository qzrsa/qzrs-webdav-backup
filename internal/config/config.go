// Package config holds the persisted application state: server settings, WebDAV
// connection profiles and backup jobs. A single JSON document is the source of
// truth; it is written atomically (temp file + rename) and never left half
// updated.
package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/qzrsa/qzrs-webdav-backup/internal/cryptoutil"
)

// Compression algorithms supported by the archive writer.
const (
	CompressionGzip = "gzip"
	CompressionNone = "none"
)

// Schedule modes.
const (
	ScheduleManual   = "manual"
	ScheduleCron     = "cron"
	ScheduleInterval = "interval"
)

// Run statuses (shared with the engine package).
const (
	StatusRunning = "running"
	StatusSuccess = "success"
	StatusFailed  = "failed"
	StatusPartial = "partial"
)

// Profile is a remote WebDAV endpoint. The password is stored AES-GCM sealed;
// the plaintext is never written to disk and never returned by the API.
type Profile struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	URL         string `json:"url"`
	Username    string `json:"username"`
	PasswordEnc string `json:"password_enc,omitempty"`
	InsecureTLS bool   `json:"insecure_tls"`
	TimeoutSec  int    `json:"timeout_sec"`
}

// HasPassword reports whether a password is stored for this profile.
func (p Profile) HasPassword() bool { return p.PasswordEnc != "" }

// Source describes what to back up.
type Source struct {
	Paths          []string `json:"paths"`
	Include        []string `json:"include,omitempty"`
	Exclude        []string `json:"exclude,omitempty"`
	MaxFileSizeMB  int      `json:"max_file_size_mb,omitempty"`
	FollowSymlinks bool     `json:"follow_symlinks,omitempty"`
	OneFileSystem  bool     `json:"one_file_system,omitempty"`
}

// Target describes where the archive goes.
type Target struct {
	ProfileID string `json:"profile_id"`
	Dir       string `json:"dir"`
}

// Schedule controls automatic execution.
type Schedule struct {
	Mode     string `json:"mode"`
	Cron     string `json:"cron,omitempty"`
	Interval string `json:"interval,omitempty"`
}

// Retention controls pruning of old archives.
type Retention struct {
	Keep int `json:"keep"`
}

// Options tune the archive writer.
type Options struct {
	Compression   string `json:"compression"`
	GzipLevel     int    `json:"gzip_level,omitempty"`
	TempDir       string `json:"temp_dir,omitempty"`
	ExcludeCaches bool   `json:"exclude_caches,omitempty"`
	// Stream uploads the archive as it is produced instead of staging it on
	// local disk first. It saves space but requires the WebDAV server to accept
	// a PUT without Content-Length (chunked transfer encoding).
	Stream bool `json:"stream,omitempty"`
}

// Job is a single backup task.
type Job struct {
	ID         string     `json:"id"`
	Name       string     `json:"name"`
	Comment    string     `json:"comment,omitempty"`
	Enabled    bool       `json:"enabled"`
	Source     Source     `json:"source"`
	Target     Target     `json:"target"`
	Schedule   Schedule   `json:"schedule"`
	Retention  Retention  `json:"retention"`
	Options    Options    `json:"options"`
	CreatedAt  time.Time  `json:"created_at"`
	UpdatedAt  time.Time  `json:"updated_at"`
	LastRunAt  *time.Time `json:"last_run_at,omitempty"`
	LastStatus string     `json:"last_status,omitempty"`
}

// Admin holds the single console account.
type Admin struct {
	Username     string `json:"username"`
	PasswordHash string `json:"password_hash"`
}

// Config is the whole persisted state.
type Config struct {
	Version         int           `json:"version"`
	Listen          string        `json:"listen"`
	DataDir         string        `json:"data_dir"`
	SecretKey       string        `json:"secret_key"`
	TempDir         string        `json:"temp_dir,omitempty"`
	SessionTTLHours int           `json:"session_ttl_hours"`
	HistoryKeep     int           `json:"history_keep"`
	MaxConcurrent   int           `json:"max_concurrent"`
	TrustedProxy    bool          `json:"trusted_proxy"`
	Notify          *NotifyConfig `json:"notify,omitempty"`
	Admin           Admin         `json:"admin"`
	Profiles        []Profile     `json:"profiles"`
	Jobs            []Job         `json:"jobs"`
}

// NotifyConfig describes the optional webhook fired when a task finishes.
// Four payload dialects are supported so the operator can point this at the
// push service they already use without a transformer in between.
type NotifyConfig struct {
	// URL is the endpoint. For telegram it must be the full
	// https://api.telegram.org/bot<token>/sendMessage; for bark it is the
	// personal push base (https://api.day.app/<key>); for gotify it is the
	// server base (the app token goes in ChatID) — an URL that already
	// carries /message?token=... is posted to verbatim; json/wecom are
	// posted to verbatim.
	URL       string `json:"url,omitempty"`
	Format    string `json:"format,omitempty"` // "", "json", "bark", "wecom", "telegram", "gotify"
	ChatID    string `json:"chat_id,omitempty"`
	OnSuccess bool   `json:"on_success"`
	OnFailure bool   `json:"on_failure"`
}

// EffectiveFormat returns the payload dialect, defaulting to generic JSON.
func (n *NotifyConfig) EffectiveFormat() string {
	if n.Format == "" {
		return "json"
	}
	return n.Format
}

// Defaults returns a config pre-filled with sane router-friendly values.
// The caller is responsible for the admin credentials and secret key.
func Defaults(dataDir string) *Config {
	return &Config{
		Version:         1,
		Listen:          "0.0.0.0:8787",
		DataDir:         dataDir,
		SessionTTLHours: 72,
		HistoryKeep:     200,
		MaxConcurrent:   1,
		Notify:          &NotifyConfig{OnFailure: true},
		Admin:           Admin{Username: "admin"},
		Profiles:        []Profile{},
		Jobs:            []Job{},
	}
}

// Validate reports the first structural problem it finds.
func (c *Config) Validate() error {
	if c.Listen == "" {
		return errors.New("listen address must not be empty")
	}
	if c.DataDir == "" {
		return errors.New("data_dir must not be empty")
	}
	if c.Admin.Username == "" {
		return errors.New("admin username must not be empty")
	}
	if c.SessionTTLHours <= 0 {
		c.SessionTTLHours = 72
	}
	if n := c.Notify; n != nil && n.URL != "" {
		switch n.Format {
		case "", "json", "bark", "wecom", "telegram", "gotify":
		default:
			return errors.New("notify.format must be one of: json, bark, wecom, telegram, gotify")
		}
		if n.Format == "telegram" && strings.TrimSpace(n.ChatID) == "" {
			return errors.New("notify.chat_id is required for the telegram format")
		}
		if u, err := url.Parse(strings.TrimSpace(n.URL)); err != nil ||
			(u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			return errors.New("notify.url must be a valid http(s) address")
		}
	}
	if c.HistoryKeep <= 0 {
		c.HistoryKeep = 200
	}
	if c.MaxConcurrent <= 0 {
		c.MaxConcurrent = 1
	}
	ids := map[string]bool{}
	for i := range c.Profiles {
		p := &c.Profiles[i]
		if p.ID == "" {
			return fmt.Errorf("profile #%d has no id", i+1)
		}
		if ids["p"+p.ID] {
			return fmt.Errorf("duplicate profile id %q", p.ID)
		}
		ids["p"+p.ID] = true
		if strings.TrimSpace(p.URL) == "" {
			return fmt.Errorf("profile %q has an empty url", p.Name)
		}
		if !strings.HasPrefix(p.URL, "http://") && !strings.HasPrefix(p.URL, "https://") {
			return fmt.Errorf("profile %q url must start with http:// or https://", p.Name)
		}
		if p.TimeoutSec <= 0 {
			p.TimeoutSec = 120
		}
	}
	for i := range c.Jobs {
		j := &c.Jobs[i]
		if j.ID == "" {
			return fmt.Errorf("job #%d has no id", i+1)
		}
		if ids["j"+j.ID] {
			return fmt.Errorf("duplicate job id %q", j.ID)
		}
		ids["j"+j.ID] = true
		if len(j.Source.Paths) == 0 {
			return fmt.Errorf("job %q has no source path", j.Name)
		}
		if j.Source.Paths[0] == "" {
			return fmt.Errorf("job %q has an empty source path", j.Name)
		}
		if j.Target.ProfileID == "" {
			return fmt.Errorf("job %q has no target profile", j.Name)
		}
		if c.FindProfile(j.Target.ProfileID) == nil {
			return fmt.Errorf("job %q references unknown profile %q", j.Name, j.Target.ProfileID)
		}
		switch j.Schedule.Mode {
		case "", ScheduleManual:
			j.Schedule.Mode = ScheduleManual
		case ScheduleCron:
			if strings.TrimSpace(j.Schedule.Cron) == "" {
				return fmt.Errorf("job %q has cron mode but no expression", j.Name)
			}
		case ScheduleInterval:
			if _, err := time.ParseDuration(j.Schedule.Interval); err != nil {
				return fmt.Errorf("job %q has invalid interval %q: %w", j.Name, j.Schedule.Interval, err)
			}
		default:
			return fmt.Errorf("job %q has unknown schedule mode %q", j.Name, j.Schedule.Mode)
		}
		if j.Options.Compression == "" {
			j.Options.Compression = CompressionGzip
		}
		if j.Options.GzipLevel <= 0 || j.Options.GzipLevel > 9 {
			j.Options.GzipLevel = 6
		}
		if j.Retention.Keep < 0 {
			j.Retention.Keep = 0
		}
	}
	return nil
}

// FindProfile returns a pointer to the profile with the given id, or nil.
// The returned pointer aliases internal storage; callers must not mutate it.
func (c *Config) FindProfile(id string) *Profile {
	for i := range c.Profiles {
		if c.Profiles[i].ID == id {
			return &c.Profiles[i]
		}
	}
	return nil
}

// FindJob returns a pointer to the job with the given id, or nil.
func (c *Config) FindJob(id string) *Job {
	for i := range c.Jobs {
		if c.Jobs[i].ID == id {
			return &c.Jobs[i]
		}
	}
	return nil
}

// SecretKeyBytes returns the AES key derived from the config secret.
func (c *Config) SecretKeyBytes() []byte { return cryptoutil.DeriveKey(c.SecretKey) }

// DecryptPassword unseals a profile password.
func (c *Config) DecryptPassword(p *Profile) (string, error) {
	if p.PasswordEnc == "" {
		return "", nil
	}
	pt, err := cryptoutil.Decrypt(c.SecretKeyBytes(), p.PasswordEnc)
	if err != nil {
		return "", err
	}
	return string(pt), nil
}

// SealPassword encrypts a plaintext password for storage in a Profile.
func (c *Config) SealPassword(plain string) (string, error) {
	if plain == "" {
		return "", nil
	}
	return cryptoutil.Encrypt(c.SecretKeyBytes(), []byte(plain))
}

// Store wraps a Config with locking and atomic persistence.
type Store struct {
	path string
	mu   sync.RWMutex
	cfg  *Config
}

// NewStore loads the config from path, creating a default one when absent.
// The returned bool reports whether a new config was created (i.e. first run).
func NewStore(path string) (*Store, bool, error) {
	s := &Store{path: path}
	data, err := os.ReadFile(path)
	switch {
	case errors.Is(err, os.ErrNotExist):
		dataDir := filepath.Dir(path)
		cfg := Defaults(dataDir)
		cfg.SecretKey = cryptoutil.RandomHex(32)
		if err := cfg.Validate(); err != nil {
			return nil, false, err
		}
		s.cfg = cfg
		if err := s.saveLocked(); err != nil {
			return nil, false, err
		}
		return s, true, nil
	case err != nil:
		return nil, false, fmt.Errorf("read config %s: %w", path, err)
	}

	cfg := Defaults(filepath.Dir(path))
	dec := json.NewDecoder(strings.NewReader(string(data)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(cfg); err != nil {
		// Retry permissively so that a config written by a slightly newer
		// version still loads instead of bricking the service on upgrade.
		cfg = Defaults(filepath.Dir(path))
		if err2 := json.Unmarshal(data, cfg); err2 != nil {
			return nil, false, fmt.Errorf("parse config %s: %w", path, err2)
		}
	}
	if cfg.SecretKey == "" {
		cfg.SecretKey = cryptoutil.RandomHex(32)
	}
	if err := cfg.Validate(); err != nil {
		return nil, false, fmt.Errorf("invalid config %s: %w", path, err)
	}
	s.cfg = cfg
	return s, false, nil
}

// Path returns the backing file path.
func (s *Store) Path() string { return s.path }

// Snapshot returns a deep copy of the current config. Safe to read without
// holding the lock afterwards.
func (s *Store) Snapshot() *Config {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return clone(s.cfg)
}

func clone(c *Config) *Config {
	raw, err := json.Marshal(c)
	if err != nil {
		// Config only contains plain JSON-compatible values, so this cannot
		// fail in practice; fall back to the live pointer rather than panic.
		return c
	}
	out := &Config{}
	if err := json.Unmarshal(raw, out); err != nil {
		return c
	}
	return out
}

// Update applies fn to the live config under a write lock and persists the
// result. If fn returns an error, or the mutated config fails validation,
// nothing is written and the in-memory state is rolled back.
func (s *Store) Update(fn func(*Config) error) (*Config, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	working := clone(s.cfg)
	if err := fn(working); err != nil {
		return nil, err
	}
	if err := working.Validate(); err != nil {
		return nil, err
	}
	previous := s.cfg
	s.cfg = working
	if err := s.saveLocked(); err != nil {
		s.cfg = previous
		return nil, err
	}
	return clone(working), nil
}

// saveLocked writes the config atomically. Caller must hold the write lock.
func (s *Store) saveLocked() error {
	dir := filepath.Dir(s.path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create config dir: %w", err)
	}
	raw, err := json.MarshalIndent(s.cfg, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal config: %w", err)
	}
	raw = append(raw, '\n')

	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o600); err != nil {
		return fmt.Errorf("write temp config: %w", err)
	}
	if err := os.Chmod(tmp, 0o600); err != nil {
		return fmt.Errorf("chmod temp config: %w", err)
	}
	// Keep one previous generation for manual recovery.
	if _, err := os.Stat(s.path); err == nil {
		_ = copyFile(s.path, s.path+".bak")
	}
	if err := os.Rename(tmp, s.path); err != nil {
		return fmt.Errorf("commit config: %w", err)
	}
	return nil
}

func copyFile(src, dst string) error {
	data, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	return os.WriteFile(dst, data, 0o600)
}

// SortJobs orders jobs by name for stable API output.
func (c *Config) SortJobs() {
	sort.SliceStable(c.Jobs, func(i, j int) bool {
		return strings.ToLower(c.Jobs[i].Name) < strings.ToLower(c.Jobs[j].Name)
	})
}
