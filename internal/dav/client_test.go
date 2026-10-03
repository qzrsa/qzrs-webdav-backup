package dav

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"path"
	"strings"
	"testing"
	"time"
)

// ---------------------------------------------------------------------------
// URL construction
// ---------------------------------------------------------------------------

func TestBuildURL(t *testing.T) {
	cases := []struct {
		name   string
		base   string
		remote string
		dir    bool
		want   string
	}{
		{"root without prefix", "https://h", "", true, "https://h/"},
		{"root without prefix, file form", "https://h", "", false, "https://h/"},
		{"collection root keeps slash", "https://h/dav", "", true, "https://h/dav/"},
		{"file form drops slash", "https://h/dav", "", false, "https://h/dav"},
		{"trailing slash already there", "https://h/dav/", "", true, "https://h/dav/"},
		{"nested prefix file", "https://h/remote.php/dav/files/u",
			"a/b.tar.gz", false, "https://h/remote.php/dav/files/u/a/b.tar.gz"},
		{"nested prefix dir", "https://h/remote.php/dav/files/u",
			"backups", true, "https://h/remote.php/dav/files/u/backups/"},
		// Non-ASCII and spaces must be percent-encoded per segment.
		{"unicode and space", "https://h/dav", "备份 目录", true,
			"https://h/dav/%E5%A4%87%E4%BB%BD%20%E7%9B%AE%E5%BD%95/"},
		// A remote path must never climb out of the configured prefix.
		{"dot dot is dropped", "https://h/dav/", "../../etc/passwd", false,
			"https://h/dav/etc/passwd"},
		{"backslashes normalised", "https://h/dav", `logs\2026\a.tar.gz`, false,
			"https://h/dav/logs/2026/a.tar.gz"},
		{"leading slash ignored", "https://h/dav", "/logs/a.tar.gz", false,
			"https://h/dav/logs/a.tar.gz"},
		{"query string preserved", "https://h/dav?token=abc", "x", false,
			"https://h/dav/x?token=abc"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c, err := New(Config{BaseURL: tc.base})
			if err != nil {
				t.Fatalf("New(%q): %v", tc.base, err)
			}
			got := c.resolve(tc.remote)
			if tc.dir {
				got = c.resolveDir(tc.remote)
			}
			if got != tc.want {
				t.Errorf("got  %s\nwant %s", got, tc.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// Stub servers
// ---------------------------------------------------------------------------

func multistatus(hrefs ...string) string {
	var b strings.Builder
	b.WriteString(`<?xml version="1.0" encoding="utf-8"?><D:multistatus xmlns:D="DAV:">`)
	for _, h := range hrefs {
		name := path.Base(strings.TrimSuffix(h, "/"))
		b.WriteString(`<D:response><D:href>` + h + `</D:href><D:propstat><D:prop>`)
		b.WriteString(`<D:displayname>` + name + `</D:displayname>`)
		b.WriteString(`<D:getcontentlength>10</D:getcontentlength>`)
		b.WriteString(`<D:getlastmodified>Fri, 02 Oct 2026 07:00:00 GMT</D:getlastmodified>`)
		b.WriteString(`<D:resourcetype>`)
		if strings.HasSuffix(h, "/") {
			b.WriteString(`<D:collection/>`)
		}
		b.WriteString(`</D:resourcetype></D:prop><D:status>HTTP/1.1 200 OK</D:status>`)
		b.WriteString(`</D:propstat></D:response>`)
	}
	b.WriteString(`</D:multistatus>`)
	return b.String()
}

// newStrictCollectionServer mimics nginx/Apache DAV location blocks: the
// collection works only when addressed with a trailing slash. Without it the
// server answers 405 — which is precisely the failure operators report as
// "dav: PROPFIND /: HTTP 405 Method Not Allowed".
func newStrictCollectionServer(t *testing.T, root string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == root {
			w.Header().Set("Allow", "OPTIONS, GET, HEAD, PUT, DELETE, PROPFIND, MKCOL")
			http.Error(w, "405 Not Allowed", http.StatusMethodNotAllowed)
			return
		}
		switch r.Method {
		case http.MethodOptions:
			w.Header().Set("DAV", "1,2")
			w.WriteHeader(http.StatusOK)
		case "PROPFIND":
			w.Header().Set("Content-Type", "application/xml; charset=utf-8")
			w.WriteHeader(http.StatusMultiStatus)
			_, _ = io.WriteString(w, multistatus(root+"/", root+"/a.tar.gz"))
		default:
			http.Error(w, "nope", http.StatusMethodNotAllowed)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func newClient(t *testing.T, base string) *Client {
	t.Helper()
	c, err := New(Config{BaseURL: base, Timeout: 5 * time.Second})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return c
}

// ---------------------------------------------------------------------------
// Collection trailing slash regression
// ---------------------------------------------------------------------------

// Ping must survive a server that refuses the slash-less collection URL. Before
// the fix, Ping issued OPTIONS to ".../dav", got 405, fell back to Stat("") —
// which hit ".../dav" again and returned the 405, surfacing as
// "dav: PROPFIND /: HTTP 405 Method Not Allowed (server does not allow this
// method here)" even though the endpoint was perfectly usable.
func TestPingSucceedsAgainstStrictCollectionServer(t *testing.T) {
	srv := newStrictCollectionServer(t, "/dav")
	c := newClient(t, srv.URL+"/dav")

	if err := c.Ping(context.Background()); err != nil {
		t.Fatalf("Ping failed against a strict-but-working server: %v", err)
	}
}

func TestListSucceedsAgainstStrictCollectionServer(t *testing.T) {
	srv := newStrictCollectionServer(t, "/dav")
	c := newClient(t, srv.URL+"/dav")

	entries, err := c.List(context.Background(), "")
	if err != nil {
		t.Fatalf("List failed: %v", err)
	}
	if len(entries) != 1 || entries[0].Name != "a.tar.gz" {
		t.Fatalf("unexpected entries: %+v", entries)
	}
}

// Stat is used on files too, so the collection form must not be the only form
// tried: a server that 404s on "<file>/" has to be handled by falling back.
func TestStatOnFileFallsBackToPlainForm(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/dav/file.tar.gz":
			w.Header().Set("Content-Type", "application/xml; charset=utf-8")
			w.WriteHeader(http.StatusMultiStatus)
			_, _ = io.WriteString(w, multistatus("/dav/file.tar.gz"))
		case "/dav/file.tar.gz/":
			// A trailing slash on a real file is not a thing.
			http.Error(w, "not found", http.StatusNotFound)
		default:
			http.Error(w, "not found", http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)

	c := newClient(t, srv.URL+"/dav")
	res, err := c.Stat(context.Background(), "file.tar.gz")
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if res.Path != "file.tar.gz" || res.IsDir {
		t.Fatalf("unexpected resource: %+v", res)
	}
}

// ---------------------------------------------------------------------------
// Error reporting
// ---------------------------------------------------------------------------

func TestStatusErrorCarriesAllowAndHint(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Allow", "GET, HEAD")
		w.Header().Set("Server", "nginx")
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
	}))
	t.Cleanup(srv.Close)

	c := newClient(t, srv.URL+"/dav")
	_, err := c.List(context.Background(), "")
	if err == nil {
		t.Fatal("expected an error")
	}

	var se *StatusError
	if !errors.As(err, &se) {
		t.Fatalf("error is not a *StatusError: %T", err)
	}
	if se.Code != http.StatusMethodNotAllowed {
		t.Errorf("Code = %d, want 405", se.Code)
	}
	if se.Allow != "GET, HEAD" {
		t.Errorf("Allow = %q, want %q", se.Allow, "GET, HEAD")
	}
	if se.Server != "nginx" {
		t.Errorf("Server = %q, want nginx", se.Server)
	}
	msg := err.Error()
	for _, want := range []string{"GET, HEAD", "nginx", "WebDAV 根"} {
		if !strings.Contains(msg, want) {
			t.Errorf("message %q should mention %q", msg, want)
		}
	}
}

// When neither OPTIONS nor PROPFIND is accepted, both attempts must be visible:
// reporting only the PROPFIND half makes a proxy-side refusal look like the
// server disliking PROPFIND in particular.
func TestPingReportsBothAttemptsWhenNeitherWorks(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
	}))
	t.Cleanup(srv.Close)

	c := newClient(t, srv.URL+"/dav")
	err := c.Ping(context.Background())
	if err == nil {
		t.Fatal("expected Ping to fail")
	}
	msg := err.Error()
	if !strings.Contains(msg, "OPTIONS") {
		t.Errorf("message should mention the OPTIONS attempt: %q", msg)
	}
	if !strings.Contains(msg, "PROPFIND") {
		t.Errorf("message should mention the PROPFIND attempt: %q", msg)
	}
}

func TestStatusErrorRetriableClassification(t *testing.T) {
	for _, tc := range []struct {
		code int
		want bool
	}{
		{http.StatusMovedPermanently, true},
		{http.StatusFound, true},
		{http.StatusMethodNotAllowed, true},
		{http.StatusBadRequest, true},
		{http.StatusUnauthorized, false},
		{http.StatusForbidden, false},
		{http.StatusInternalServerError, false},
	} {
		se := &StatusError{Code: tc.code}
		if got := se.collectionRetriable(); got != tc.want {
			t.Errorf("code %d: collectionRetriable() = %v, want %v", tc.code, got, tc.want)
		}
	}
}

// ---------------------------------------------------------------------------
// Probe
// ---------------------------------------------------------------------------

func TestProbeOnStrictServerExplainsTrailingSlash(t *testing.T) {
	srv := newStrictCollectionServer(t, "/dav")
	c := newClient(t, srv.URL+"/dav")

	rep := c.Probe(context.Background())
	if !rep.OK {
		t.Fatalf("Probe should succeed, verdict: %s", rep.Verdict)
	}
	if !strings.Contains(rep.BaseURL, "/dav/") {
		t.Errorf("BaseURL should be the collection form, got %q", rep.BaseURL)
	}
	if len(rep.Steps) == 0 {
		t.Fatal("no steps recorded")
	}
}

func TestProbeVerdictOnProxyRefusal(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
	}))
	t.Cleanup(srv.Close)

	c := newClient(t, srv.URL+"/dav")
	rep := c.Probe(context.Background())
	if rep.OK {
		t.Fatal("Probe should report failure")
	}
	if !strings.Contains(rep.Verdict, "405") {
		t.Errorf("verdict should name the status: %q", rep.Verdict)
	}
}

func TestProbeVerdictOnUnauthorized(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("WWW-Authenticate", `Basic realm="x"`)
		http.Error(w, "unauthorised", http.StatusUnauthorized)
	}))
	t.Cleanup(srv.Close)

	c := newClient(t, srv.URL+"/dav")
	rep := c.Probe(context.Background())
	if rep.OK {
		t.Fatal("Probe should report failure")
	}
	if !strings.Contains(rep.Verdict, "应用") {
		t.Errorf("verdict should mention app passwords: %q", rep.Verdict)
	}
}
