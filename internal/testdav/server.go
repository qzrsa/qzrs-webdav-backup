// Package testdav implements a minimal, standards-shaped WebDAV server backed
// by a local directory. It exists so the backup and restore engines can be
// exercised end-to-end in tests without a real remote endpoint.
//
// It is intentionally strict in the ways real servers are: PUT requires a
// Content-Length, PROPFIND answers with a DAV: multistatus document, and
// requests without credentials are rejected with 401.
package testdav

import (
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Server is a WebDAV handler rooted at a directory.
type Server struct {
	Root string
	User string
	Pass string
	// RequireLength, when true, rejects chunked PUTs with 411. Real appliances
	// and consumer cloud drives frequently behave this way.
	RequireLength bool
	// ReadOnly rejects every mutating method with 403.
	ReadOnly bool
	// PathPrefix, when non-empty, makes the server answer under a URL prefix
	// (e.g. "/dav") the way a real deployment behind a DAV location block
	// does: incoming request paths carry the prefix and the hrefs inside
	// PROPFIND multistatus responses include the full prefixed path.
	PathPrefix string
	// Gate, when non-nil, blocks every request until the channel is closed.
	// Tests use it to pin a run in its claimed slot so assertions about
	// concurrency are deterministic instead of racing the request flow.
	Gate chan struct{}

	mu     sync.Mutex
	calls  map[string]int
	failOn map[string]int // method -> fail the Nth call with 500
}

// New creates a server rooted at dir.
func New(dir, user, pass string) *Server {
	return &Server{Root: dir, User: user, Pass: pass, calls: map[string]int{}, failOn: map[string]int{}}
}

// FailOnce makes the next request with the given method return HTTP 500.
func (s *Server) FailOnce(method string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := s.calls[method]
	s.failOn[method] = n + 1
}

// Calls returns how many times a method was invoked.
func (s *Server) Calls(method string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.calls[method]
}

func (s *Server) Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.Gate != nil {
			select {
			case <-s.Gate:
			case <-r.Context().Done():
				return
			}
		}
		s.mu.Lock()
		s.calls[r.Method]++
		shouldFail := s.failOn[r.Method] == s.calls[r.Method]
		s.mu.Unlock()
		if shouldFail {
			http.Error(w, "injected failure", http.StatusInternalServerError)
			return
		}

		if !s.authorised(r) {
			w.Header().Set("WWW-Authenticate", `Basic realm="webdav"`)
			http.Error(w, "unauthorised", http.StatusUnauthorized)
			return
		}
		if s.ReadOnly && r.Method != http.MethodGet && r.Method != http.MethodHead &&
			r.Method != "PROPFIND" && r.Method != http.MethodOptions {
			http.Error(w, "read-only", http.StatusForbidden)
			return
		}

		local, ok := s.localPath(r.URL.Path)
		if !ok {
			http.Error(w, "bad path", http.StatusBadRequest)
			return
		}

		switch r.Method {
		case http.MethodOptions:
			w.Header().Set("DAV", "1,2")
			w.Header().Set("Allow", "OPTIONS, GET, HEAD, PUT, DELETE, PROPFIND, MKCOL, MOVE")
			w.WriteHeader(http.StatusOK)
		case "PROPFIND":
			s.propfind(w, r, local)
		case http.MethodPut:
			s.put(w, r, local)
		case http.MethodGet:
			s.get(w, r, local, false)
		case http.MethodHead:
			s.get(w, r, local, true)
		case "MKCOL":
			s.mkcol(w, r, local)
		case http.MethodDelete:
			s.delete(w, r, local)
		case "MOVE":
			s.move(w, r, local)
		default:
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		}
	})
}

func (s *Server) authorised(r *http.Request) bool {
	if s.User == "" && s.Pass == "" {
		return true
	}
	user, pass, ok := r.BasicAuth()
	return ok && user == s.User && pass == s.Pass
}

// hasPrefix reports whether urlPath sits under the configured prefix.
// "/dav2" must not match a "/dav" prefix.
func (s *Server) hasPrefix(urlPath string) bool {
	if s.PathPrefix == "" {
		return true
	}
	return urlPath == s.PathPrefix || strings.HasPrefix(urlPath, s.PathPrefix+"/")
}

// localPath maps a URL path into the root, rejecting traversal.
func (s *Server) localPath(urlPath string) (string, bool) {
	if !s.hasPrefix(urlPath) {
		return "", false
	}
	urlPath = strings.TrimPrefix(urlPath, s.PathPrefix)
	if urlPath == "" {
		urlPath = "/"
	}
	clean := path.Clean("/" + strings.TrimPrefix(urlPath, "/"))
	if strings.Contains(clean, "..") {
		return "", false
	}
	full := filepath.Join(s.Root, filepath.FromSlash(clean))
	rel, err := filepath.Rel(s.Root, full)
	if err != nil || strings.HasPrefix(rel, "..") {
		return "", false
	}
	return full, true
}

func (s *Server) urlPath(local string) string {
	rel, err := filepath.Rel(s.Root, local)
	if err != nil || rel == "." {
		return s.PathPrefix + "/"
	}
	return s.PathPrefix + "/" + filepath.ToSlash(rel)
}

func (s *Server) propfind(w http.ResponseWriter, r *http.Request, local string) {
	st, err := os.Stat(local)
	if err != nil {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}

	depth := r.Header.Get("Depth")
	if depth == "" {
		depth = "1"
	}

	type resp struct {
		Href  string
		Props propSet
	}
	var responses []resp

	addOne := func(p string, info os.FileInfo) {
		responses = append(responses, resp{Href: s.urlPath(p), Props: buildProps(info, s.urlPath(p))})
	}
	addOne(local, st)

	if st.IsDir() && depth != "0" {
		entries, err := os.ReadDir(local)
		if err == nil {
			var names []string
			for _, e := range entries {
				names = append(names, e.Name())
			}
			sort.Strings(names)
			for _, name := range names {
				child := filepath.Join(local, name)
				ci, err := os.Stat(child)
				if err != nil {
					continue
				}
				addOne(child, ci)
			}
		}
	}

	w.Header().Set("Content-Type", "application/xml; charset=utf-8")
	w.WriteHeader(http.StatusMultiStatus)

	_, _ = io.WriteString(w, `<?xml version="1.0" encoding="utf-8"?>`+"\n")
	_, _ = io.WriteString(w, `<D:multistatus xmlns:D="DAV:">`+"\n")
	for _, rp := range responses {
		_, _ = io.WriteString(w, "  <D:response>\n")
		_, _ = fmt.Fprintf(w, "    <D:href>%s</D:href>\n", xmlEscape(rp.Href))
		_, _ = io.WriteString(w, "    <D:propstat>\n      <D:prop>\n")
		if rp.Props.IsDir {
			_, _ = io.WriteString(w, "        <D:resourcetype><D:collection/></D:resourcetype>\n")
		} else {
			_, _ = io.WriteString(w, "        <D:resourcetype/>\n")
		}
		_, _ = fmt.Fprintf(w, "        <D:displayname>%s</D:displayname>\n",
			xmlEscape(path.Base(strings.TrimSuffix(rp.Href, "/"))))
		_, _ = fmt.Fprintf(w, "        <D:getcontentlength>%d</D:getcontentlength>\n", rp.Props.Size)
		_, _ = fmt.Fprintf(w, "        <D:getlastmodified>%s</D:getlastmodified>\n",
			rp.Props.ModTime.UTC().Format(http.TimeFormat))
		_, _ = fmt.Fprintf(w, "        <D:getcontenttype>%s</D:getcontenttype>\n", xmlEscape(rp.Props.ContentType))
		_, _ = io.WriteString(w, "      </D:prop>\n      <D:status>HTTP/1.1 200 OK</D:status>\n    </D:propstat>\n")
		_, _ = io.WriteString(w, "  </D:response>\n")
	}
	_, _ = io.WriteString(w, `</D:multistatus>`+"\n")
}

type propSet struct {
	Size        int64
	ModTime     time.Time
	ContentType string
	IsDir       bool
}

func buildProps(info os.FileInfo, href string) propSet {
	ct := "application/octet-stream"
	if info.IsDir() {
		ct = "httpd/unix-directory"
	} else if strings.HasSuffix(href, ".gz") {
		ct = "application/gzip"
	}
	return propSet{Size: info.Size(), ModTime: info.ModTime(), ContentType: ct, IsDir: info.IsDir()}
}

func (s *Server) put(w http.ResponseWriter, r *http.Request, local string) {
	if s.RequireLength && r.ContentLength < 0 {
		http.Error(w, "length required", http.StatusLengthRequired)
		return
	}
	if err := os.MkdirAll(filepath.Dir(local), 0o755); err != nil {
		http.Error(w, err.Error(), http.StatusConflict)
		return
	}
	existed := false
	if _, err := os.Stat(local); err == nil {
		existed = true
	}
	f, err := os.Create(local)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	n, copyErr := io.Copy(f, r.Body)
	closeErr := f.Close()
	if copyErr != nil {
		http.Error(w, copyErr.Error(), http.StatusInternalServerError)
		return
	}
	if closeErr != nil {
		http.Error(w, closeErr.Error(), http.StatusInternalServerError)
		return
	}
	if r.ContentLength >= 0 && n != r.ContentLength {
		http.Error(w, "short body", http.StatusBadRequest)
		return
	}
	if existed {
		w.WriteHeader(http.StatusNoContent)
	} else {
		w.WriteHeader(http.StatusCreated)
	}
}

func (s *Server) get(w http.ResponseWriter, r *http.Request, local string, headOnly bool) {
	f, err := os.Open(local)
	if err != nil {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if st.IsDir() {
		http.Error(w, "is a collection", http.StatusMethodNotAllowed)
		return
	}
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Length", strconv.FormatInt(st.Size(), 10))
	w.Header().Set("Last-Modified", st.ModTime().UTC().Format(http.TimeFormat))
	if headOnly {
		w.WriteHeader(http.StatusOK)
		return
	}
	w.WriteHeader(http.StatusOK)
	_, _ = io.Copy(w, f)
}

func (s *Server) mkcol(w http.ResponseWriter, r *http.Request, local string) {
	if _, err := os.Stat(local); err == nil {
		http.Error(w, "already exists", http.StatusMethodNotAllowed)
		return
	}
	// A MKCOL with a missing parent must fail with 409, like real servers.
	if _, err := os.Stat(filepath.Dir(local)); err != nil {
		http.Error(w, "conflict", http.StatusConflict)
		return
	}
	if err := os.Mkdir(local, 0o755); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusCreated)
}

func (s *Server) delete(w http.ResponseWriter, r *http.Request, local string) {
	if _, err := os.Stat(local); err != nil {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	if err := os.RemoveAll(local); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) move(w http.ResponseWriter, r *http.Request, local string) {
	dest := r.Header.Get("Destination")
	if dest == "" {
		http.Error(w, "no destination", http.StatusBadRequest)
		return
	}
	if i := strings.Index(dest, "://"); i >= 0 {
		if j := strings.Index(dest[i+3:], "/"); j >= 0 {
			dest = dest[i+3+j:]
		}
	}
	target, ok := s.localPath(dest)
	if !ok {
		http.Error(w, "bad destination", http.StatusBadRequest)
		return
	}
	if _, err := os.Stat(local); err != nil {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	if strings.Contains(strings.ToLower(r.Header.Get("Overwrite")), "f") {
		_ = os.RemoveAll(target)
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		http.Error(w, err.Error(), http.StatusConflict)
		return
	}
	if err := os.Rename(local, target); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusCreated)
}

func xmlEscape(s string) string {
	var b strings.Builder
	_ = xml.EscapeText(&b, []byte(s))
	return b.String()
}
