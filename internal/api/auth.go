package api

import (
	"crypto/sha256"
	"encoding/hex"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/qzrsa/qzrs-webdav-backup/internal/config"
	"github.com/qzrsa/qzrs-webdav-backup/internal/cryptoutil"
)

const (
	sessionCookieName = "wdb_session"
	maxLoginFailures  = 8
	loginLockout      = 5 * time.Minute
	loginWindow       = 10 * time.Minute
)

type session struct {
	Username string
	Created  time.Time
	Expires  time.Time
	ClientIP string
}

// sessionStore keeps sessions in memory. A restart invalidates every session,
// which is an acceptable trade-off for a single-user appliance and avoids
// persisting credentials material to disk.
type sessionStore struct {
	mu       sync.Mutex
	sessions map[string]session
	ttl      time.Duration
}

func newSessionStore(ttl time.Duration) *sessionStore {
	if ttl <= 0 {
		ttl = 72 * time.Hour
	}
	s := &sessionStore{sessions: map[string]session{}, ttl: ttl}
	go s.janitor()
	return s
}

func (s *sessionStore) janitor() {
	t := time.NewTicker(15 * time.Minute)
	defer t.Stop()
	for range t.C {
		now := time.Now()
		s.mu.Lock()
		for tok, sess := range s.sessions {
			if now.After(sess.Expires) {
				delete(s.sessions, tok)
			}
		}
		s.mu.Unlock()
	}
}

func (s *sessionStore) create(username, ip string) (string, time.Time) {
	token := cryptoutil.RandomToken(32)
	expires := time.Now().Add(s.ttl)
	s.mu.Lock()
	s.sessions[token] = session{Username: username, Created: time.Now(), Expires: expires, ClientIP: ip}
	s.mu.Unlock()
	return token, expires
}

func (s *sessionStore) get(token string) (session, bool) {
	if token == "" {
		return session{}, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	sess, ok := s.sessions[token]
	if !ok {
		return session{}, false
	}
	if time.Now().After(sess.Expires) {
		delete(s.sessions, token)
		return session{}, false
	}
	return sess, true
}

func (s *sessionStore) drop(token string) {
	s.mu.Lock()
	delete(s.sessions, token)
	s.mu.Unlock()
}

// dropAllExcept invalidates every session except the given token. Used after a
// password change so other devices are logged out.
func (s *sessionStore) dropAllExcept(token string) {
	s.mu.Lock()
	for tok := range s.sessions {
		if tok != token {
			delete(s.sessions, tok)
		}
	}
	s.mu.Unlock()
}

// loginLimiter throttles password guessing per client address.
type loginLimiter struct {
	mu       sync.Mutex
	attempts map[string]*attemptRecord
}

type attemptRecord struct {
	count     int
	first     time.Time
	lockedTil time.Time
}

func newLoginLimiter() *loginLimiter {
	return &loginLimiter{attempts: map[string]*attemptRecord{}}
}

func (l *loginLimiter) key(r *http.Request) string {
	// Only the TCP peer is trusted here: X-Forwarded-For is attacker-controlled
	// unless an operator explicitly enabled proxy trust.
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

func (l *loginLimiter) locked(key string) (time.Duration, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	rec, ok := l.attempts[key]
	if !ok {
		return 0, false
	}
	if time.Now().Before(rec.lockedTil) {
		return time.Until(rec.lockedTil), true
	}
	return 0, false
}

func (l *loginLimiter) fail(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := time.Now()
	rec, ok := l.attempts[key]
	if !ok || now.Sub(rec.first) > loginWindow {
		rec = &attemptRecord{first: now}
		l.attempts[key] = rec
	}
	rec.count++
	if rec.count >= maxLoginFailures {
		rec.lockedTil = now.Add(loginLockout)
		rec.count = 0
		rec.first = now
	}
}

func (l *loginLimiter) succeed(key string) {
	l.mu.Lock()
	delete(l.attempts, key)
	l.mu.Unlock()
}

// ---------------------------------------------------------------------------
// Middleware
// ---------------------------------------------------------------------------

type ctxKey string

const ctxSession ctxKey = "session"

func (s *Server) clientIP(r *http.Request) string {
	if s.trustProxy {
		if fwd := r.Header.Get("X-Forwarded-For"); fwd != "" {
			if i := strings.IndexByte(fwd, ','); i > 0 {
				return strings.TrimSpace(fwd[:i])
			}
			return strings.TrimSpace(fwd)
		}
		if real := r.Header.Get("X-Real-IP"); real != "" {
			return strings.TrimSpace(real)
		}
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

func (s *Server) currentSession(r *http.Request) (session, bool) {
	c, err := r.Cookie(sessionCookieName)
	if err != nil {
		return session{}, false
	}
	return s.sessions.get(c.Value)
}

// requireAuth wraps a handler so it only runs for authenticated callers.
func (s *Server) requireAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if _, ok := s.currentSession(r); !ok {
			writeError(w, http.StatusUnauthorized, "unauthorized", "请先登录")
			return
		}
		next(w, r)
	}
}

// requireJSON rejects state-changing requests that are not AJAX JSON calls.
// Combined with SameSite=Lax cookies this blocks cross-site request forgery.
func (s *Server) requireJSON(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet, http.MethodHead, http.MethodOptions:
			next(w, r)
			return
		}
		ct := r.Header.Get("Content-Type")
		if ct != "" && !strings.HasPrefix(ct, "application/json") {
			writeError(w, http.StatusUnsupportedMediaType, "bad_content_type",
				"请求必须是 application/json")
			return
		}
		if r.Header.Get("X-Requested-With") != "qzrs-webdav-backup" {
			writeError(w, http.StatusForbidden, "csrf", "缺少 X-Requested-With 头")
			return
		}
		next(w, r)
	}
}

func (s *Server) setSessionCookie(w http.ResponseWriter, r *http.Request, token string, expires time.Time) {
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    token,
		Path:     "/",
		Expires:  expires,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   r.TLS != nil,
	})
}

func (s *Server) clearSessionCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	})
}

// ---------------------------------------------------------------------------
// Handlers
// ---------------------------------------------------------------------------

type loginRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	var req loginRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	key := s.limiter.key(r)
	if wait, locked := s.limiter.locked(key); locked {
		// Say how to get unstuck: the limiter is in-memory only, so a restart
		// clears it immediately and beats waiting.
		writeError(w, http.StatusTooManyRequests, "locked_out",
			"登录尝试过于频繁，请在 "+wait.Round(time.Second).String()+" 后重试"+
				"（锁定只存在于内存中，在设备上执行 /etc/init.d/qzrs-webdav-backup restart 可立即清除）")
		return
	}

	cfg := s.store.Snapshot()
	// Constant-ish time: always run the KDF so a wrong username and a wrong
	// password take similar time.
	userOK := subtleStringEqual(req.Username, cfg.Admin.Username)
	passOK := cryptoutil.VerifyPassword(cfg.Admin.PasswordHash, req.Password)

	if !userOK || !passOK {
		s.limiter.fail(key)
		s.logf("login failed from %s (user=%q)", s.clientIP(r), req.Username)
		// The username is a single fixed value, so naming it removes the most
		// common cause of a lockout: retrying with a guessed username. It is
		// already prefilled in the login form, so this leaks nothing new.
		writeError(w, http.StatusUnauthorized, "bad_credentials",
			"用户名或密码错误（用户名为 "+cfg.Admin.Username+
				"，密码可用 grep -A3 '已创建默认管理账号' /etc/qzrs-webdav-backup/service.log 在设备上找回）")
		return
	}

	s.limiter.succeed(key)
	token, expires := s.sessions.create(cfg.Admin.Username, s.clientIP(r))
	s.setSessionCookie(w, r, token, expires)
	s.logf("login succeeded from %s", s.clientIP(r))
	writeJSON(w, http.StatusOK, map[string]any{
		"username": cfg.Admin.Username,
		"expires":  expires,
	})
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(sessionCookieName); err == nil {
		s.sessions.drop(c.Value)
	}
	s.clearSessionCookie(w)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) handleMe(w http.ResponseWriter, r *http.Request) {
	sess, ok := s.currentSession(r)
	if !ok {
		writeJSON(w, http.StatusOK, map[string]any{"authenticated": false})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"authenticated": true,
		"username":      sess.Username,
		"expires":       sess.Expires,
	})
}

type passwordRequest struct {
	Current string `json:"current"`
	New     string `json:"new"`
}

func (s *Server) handleChangePassword(w http.ResponseWriter, r *http.Request) {
	var req passwordRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	cfg := s.store.Snapshot()
	if !cryptoutil.VerifyPassword(cfg.Admin.PasswordHash, req.Current) {
		writeError(w, http.StatusForbidden, "bad_password", "当前密码不正确")
		return
	}
	if len(req.New) < 5 {
		writeError(w, http.StatusBadRequest, "weak_password", "新密码至少需要 5 个字符")
		return
	}
	hash, err := cryptoutil.HashPassword(req.New)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "hash_failed", err.Error())
		return
	}
	if _, err := s.store.Update(func(c *config.Config) error {
		c.Admin.PasswordHash = hash
		return nil
	}); err != nil {
		writeError(w, http.StatusInternalServerError, "save_failed", err.Error())
		return
	}

	// Invalidate every other session.
	if c, err := r.Cookie(sessionCookieName); err == nil {
		s.sessions.dropAllExcept(c.Value)
	}
	s.logf("admin password changed")
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func subtleStringEqual(a, b string) bool {
	// Hash both sides to fixed-width digests so the comparison length does not
	// leak the configured username.
	ha := sha256.Sum256([]byte(a))
	hb := sha256.Sum256([]byte(b))
	return cryptoutil.EqualBytes(ha[:], hb[:])
}

var _ = hex.EncodeToString
