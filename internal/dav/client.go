// Package dav is a small, dependency-free WebDAV client covering exactly the
// operations a backup tool needs: PROPFIND, MKCOL, PUT, GET and DELETE.
//
// It is deliberately permissive about XML namespaces. Real-world servers
// (Nextcloud, Alist, CloudDrive2, Synology, JianguoYun, plain nginx-dav) emit
// responses with varying namespace prefixes and occasional namespace bugs, so
// every element is matched on its local name only.
package dav

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path"
	"strconv"
	"strings"
	"time"
)

// Resource is one entry returned by PROPFIND.
type Resource struct {
	Href        string    `json:"href"`
	Path        string    `json:"path"`
	Name        string    `json:"name"`
	IsDir       bool      `json:"is_dir"`
	Size        int64     `json:"size"`
	Modified    time.Time `json:"modified"`
	ContentType string    `json:"content_type,omitempty"`
	ETag        string    `json:"etag,omitempty"`
}

// Config describes how to reach a WebDAV endpoint.
type Config struct {
	BaseURL     string
	Username    string
	Password    string
	InsecureTLS bool
	// Timeout bounds metadata operations and also acts as the maximum time
	// allowed to wait for response headers on a bulk transfer. It does not
	// cap the duration of the transfer body itself, so multi-gigabyte uploads
	// are not killed mid-flight.
	Timeout time.Duration
	// UserAgent identifies the client to the server.
	UserAgent string
}

// Client is a WebDAV client bound to one endpoint.
type Client struct {
	base      *url.URL
	username  string
	password  string
	hc        *http.Client
	userAgent string
	timeout   time.Duration
}

// ErrNotFound is returned by Stat when the remote path does not exist.
var ErrNotFound = errors.New("dav: remote path not found")

// New builds a client. It validates the base URL but performs no I/O.
func New(cfg Config) (*Client, error) {
	raw := strings.TrimSpace(cfg.BaseURL)
	if raw == "" {
		return nil, errors.New("dav: base url is empty")
	}
	if !strings.Contains(raw, "://") {
		raw = "https://" + raw
	}
	u, err := url.Parse(raw)
	if err != nil {
		return nil, fmt.Errorf("dav: parse base url %q: %w", cfg.BaseURL, err)
	}
	if u.Host == "" {
		return nil, fmt.Errorf("dav: base url %q has no host", cfg.BaseURL)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return nil, fmt.Errorf("dav: unsupported scheme %q", u.Scheme)
	}
	// Normalise: keep any path prefix the user supplied (e.g. /dav/backups),
	// strip a trailing slash so Join behaves predictably.
	if !strings.HasSuffix(u.Path, "/") {
		u.Path += "/"
	}

	timeout := cfg.Timeout
	if timeout <= 0 {
		timeout = 120 * time.Second
	}
	ua := cfg.UserAgent
	if ua == "" {
		ua = "qzrs-webdav-backup/1.0"
	}

	transport := &http.Transport{
		Proxy: http.ProxyFromEnvironment,
		DialContext: (&net.Dialer{
			Timeout:   15 * time.Second,
			KeepAlive: 30 * time.Second,
		}).DialContext,
		MaxIdleConns:          8,
		MaxIdleConnsPerHost:   4,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   15 * time.Second,
		ExpectContinueTimeout: 3 * time.Second,
		// Waiting for the first byte of a response (including a 100-continue
		// acknowledgement) must not hang forever.
		ResponseHeaderTimeout: timeout,
		ForceAttemptHTTP2:     true,
	}
	if cfg.InsecureTLS {
		transport.TLSClientConfig = &tls.Config{InsecureSkipVerify: true} //nolint:gosec // opt-in per profile
	}

	hc := &http.Client{
		Transport: transport,
		// No overall timeout: large transfers are bounded by the context.
		Timeout: 0,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 6 {
				return errors.New("dav: too many redirects")
			}
			// Go downgrades non-GET methods to GET on 301/302/303, which
			// silently turns an upload into a download of the error page.
			// Stop instead and let our own logic decide.
			if req.Response != nil {
				switch req.Response.StatusCode {
				case http.StatusMovedPermanently, http.StatusFound, http.StatusSeeOther:
					if req.Method != http.MethodGet && req.Method != http.MethodHead {
						return http.ErrUseLastResponse
					}
				}
			}
			return nil
		},
	}

	return &Client{
		base:      u,
		username:  cfg.Username,
		password:  cfg.Password,
		hc:        hc,
		userAgent: ua,
		timeout:   timeout,
	}, nil
}

// BaseURL returns the normalised endpoint URL.
func (c *Client) BaseURL() string { return c.base.String() }

// resolve turns a remote path (relative to the base URL, always written with
// forward slashes) into an absolute URL, percent-encoding each segment. The
// result addresses a *file*: no trailing slash is appended.
func (c *Client) resolve(remote string) string { return c.buildURL(remote, false) }

// resolveDir is resolve() for a collection. A collection URL must keep its
// trailing slash: nginx/Apache DAV location blocks, and several consumer cloud
// drives, answer a slash-less collection with 301 — and Go cannot replay a
// PROPFIND across a redirect (it would be silently downgraded to GET) — or with
// a flat 405, which used to surface to the operator as an unexplainable
// "PROPFIND /: HTTP 405 Method Not Allowed".
func (c *Client) resolveDir(remote string) string { return c.buildURL(remote, true) }

func (c *Client) buildURL(remote string, dir bool) string {
	remote = strings.TrimSpace(remote)
	remote = strings.ReplaceAll(remote, "\\", "/")
	remote = strings.TrimPrefix(remote, "/")

	// Start from the configured prefix (e.g. "/dav"), already percent-encoded.
	// New() guarantees a trailing slash on base.Path, so trimming it here is
	// always safe and keeps the join unambiguous.
	basePath := strings.Trim(c.base.EscapedPath(), "/")

	var segs []string
	if basePath != "" {
		segs = append(segs, basePath)
	}
	for _, s := range strings.Split(remote, "/") {
		// ".." is dropped rather than resolved: a remote path must never be
		// able to climb out of the configured prefix.
		if s == "" || s == "." || s == ".." {
			continue
		}
		segs = append(segs, url.PathEscape(s))
	}

	joined := "/" + strings.Join(segs, "/")
	if dir && joined != "/" {
		joined += "/"
	}

	out := c.base.Scheme + "://" + c.base.Host + joined
	if q := c.base.RawQuery; q != "" {
		out += "?" + q
	}
	return out
}

// hrefToPath converts a href from a PROPFIND response into a path relative to
// the base URL. Returns ok=false when the href lies outside the base prefix.
func (c *Client) hrefToPath(href string) (string, bool) {
	h := strings.TrimSpace(href)
	if h == "" {
		return "", false
	}
	if u, err := url.Parse(h); err == nil && u.Path != "" {
		h = u.Path
	}
	h = strings.TrimSuffix(h, "/")
	// Percent-decode so callers see real filenames.
	if dec, err := url.PathUnescape(h); err == nil {
		h = dec
	}
	basePath := strings.TrimSuffix(c.base.Path, "/")
	if basePath != "" {
		if !strings.HasPrefix(h, basePath) {
			return "", false
		}
		h = strings.TrimPrefix(h, basePath)
	}
	return strings.Trim(h, "/"), true
}

func (c *Client) newRequest(ctx context.Context, method, remote string, body io.Reader) (*http.Request, error) {
	req, err := http.NewRequestWithContext(ctx, method, c.resolve(remote), body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", c.userAgent)
	if c.username != "" || c.password != "" {
		req.SetBasicAuth(c.username, c.password)
	}
	return req, nil
}

// maxMetaTimeout caps metadata operations independently of the profile
// timeout. Large uploads need a long ResponseHeaderTimeout (netdisk
// aggregators may take minutes before answering a PUT), but that value must
// not apply to PROPFIND and friends: with timeout_sec=3600 a hung server used
// to stall every metadata call for a full hour. Downloads and uploads do not
// go through metaContext and keep the full profile timeout.
const maxMetaTimeout = 2 * time.Minute

func (c *Client) metaContext(parent context.Context) (context.Context, context.CancelFunc) {
	if parent == nil {
		parent = context.Background()
	}
	t := c.timeout
	if t > maxMetaTimeout {
		t = maxMetaTimeout
	}
	return context.WithTimeout(parent, t)
}

// do executes a request and turns transport errors into readable messages.
func (c *Client) do(req *http.Request) (*http.Response, error) {
	resp, err := c.hc.Do(req)
	if err != nil {
		return nil, describeTransportError(req, err)
	}
	return resp, nil
}

func describeTransportError(req *http.Request, err error) error {
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		return fmt.Errorf("dav: request to %s timed out", req.URL.Host)
	case errors.Is(err, context.Canceled):
		return fmt.Errorf("dav: request cancelled")
	}
	var dnsErr *net.DNSError
	if errors.As(err, &dnsErr) {
		return fmt.Errorf("dav: cannot resolve host %q: %w", dnsErr.Name, err)
	}
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return fmt.Errorf("dav: connection to %s timed out", req.URL.Host)
	}
	var urlErr *url.Error
	if errors.As(err, &urlErr) {
		return fmt.Errorf("dav: %s %s failed: %v", req.Method, req.URL.Host, urlErr.Err)
	}
	return fmt.Errorf("dav: %s %s failed: %w", req.Method, req.URL.Host, err)
}

// ---------------------------------------------------------------------------
// PROPFIND
// ---------------------------------------------------------------------------

type msMultistatus struct {
	Responses []msResponse `xml:"response"`
}

type msResponse struct {
	Href     string       `xml:"href"`
	Propstat []msPropstat `xml:"propstat"`
}

type msPropstat struct {
	Status string `xml:"status"`
	Prop   msProp `xml:"prop"`
}

type msProp struct {
	DisplayName   string `xml:"displayname"`
	ContentLength string `xml:"getcontentlength"`
	LastModified  string `xml:"getlastmodified"`
	ContentType   string `xml:"getcontenttype"`
	ETag          string `xml:"getetag"`
	ResourceType  struct {
		Collection *struct{} `xml:"collection"`
	} `xml:"resourcetype"`
}

const propfindBody = `<?xml version="1.0" encoding="utf-8"?>
<D:propfind xmlns:D="DAV:">
  <D:prop>
    <D:displayname/>
    <D:getcontentlength/>
    <D:getlastmodified/>
    <D:getcontenttype/>
    <D:getetag/>
    <D:resourcetype/>
  </D:prop>
</D:propfind>`

// propfind issues a PROPFIND for remote. It tries the collection form of the
// URL first (trailing slash) and, only if the server objects, retries the plain
// file form. List() always targets a collection; Stat() targets either, and
// without the retry a Stat() on a *file* whose parent is a strict collection
// would come back as ErrNotFound.
//
// The op string in error messages always reports the path the caller asked for,
// not the URL form that happened to fail, so diagnostics stay stable.
func (c *Client) propfind(ctx context.Context, remote string, depth string) ([]Resource, error) {
	dir, dirErr := c.propfindAt(ctx, c.resolveDir(remote), remote, depth)
	if dirErr == nil {
		return dir, nil
	}

	// A collection URL without a trailing slash is rejected by some servers and
	// by method-filtering proxies. Retrying costs one round trip on failure and
	// saves the operator from guessing the URL shape.
	retry := errors.Is(dirErr, ErrNotFound)
	if !retry {
		var se *StatusError
		retry = errors.As(dirErr, &se) && se.collectionRetriable()
	}
	if retry {
		if plain, plainErr := c.propfindAt(ctx, c.resolve(remote), remote, depth); plainErr == nil {
			return plain, nil
		}
	}
	return nil, dirErr
}

func (c *Client) propfindAt(ctx context.Context, url, remote, depth string) ([]Resource, error) {
	req, err := http.NewRequestWithContext(ctx, "PROPFIND", url, strings.NewReader(propfindBody))
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", c.userAgent)
	if c.username != "" || c.password != "" {
		req.SetBasicAuth(c.username, c.password)
	}
	req.Header.Set("Content-Type", "application/xml; charset=utf-8")
	req.Header.Set("Depth", depth)

	resp, err := c.do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	switch resp.StatusCode {
	case http.StatusNotFound, http.StatusGone:
		return nil, ErrNotFound
	case http.StatusMultiStatus, http.StatusOK:
		// expected
	default:
		return nil, statusError("PROPFIND "+displayPath(remote), resp)
	}

	raw, err := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
	if err != nil {
		return nil, fmt.Errorf("dav: read PROPFIND response: %w", err)
	}
	var ms msMultistatus
	if err := xml.NewDecoder(bytes.NewReader(raw)).Decode(&ms); err != nil {
		return nil, fmt.Errorf("dav: parse PROPFIND response: %w", err)
	}

	out := make([]Resource, 0, len(ms.Responses))
	for _, r := range ms.Responses {
		p, ok := c.hrefToPath(r.Href)
		if !ok {
			continue
		}
		res := Resource{Href: r.Href, Path: p, Name: path.Base(p)}
		if p == "" {
			res.Name = ""
		}
		for _, ps := range r.Propstat {
			if ps.Status != "" && !strings.Contains(ps.Status, " 200 ") && !strings.Contains(ps.Status, " 200\n") {
				continue
			}
			if ps.Prop.DisplayName != "" {
				res.Name = ps.Prop.DisplayName
			}
			res.IsDir = ps.Prop.ResourceType.Collection != nil
			res.ContentType = ps.Prop.ContentType
			res.ETag = strings.Trim(ps.Prop.ETag, `"`)
			if n, err := strconv.ParseInt(strings.TrimSpace(ps.Prop.ContentLength), 10, 64); err == nil {
				res.Size = n
			}
			if t, err := parseHTTPDate(ps.Prop.LastModified); err == nil {
				res.Modified = t
			}
		}
		out = append(out, res)
	}
	return out, nil
}

// List returns the direct children of a remote collection. A missing directory
// yields ErrNotFound.
func (c *Client) List(parent context.Context, remote string) ([]Resource, error) {
	ctx, cancel := c.metaContext(parent)
	defer cancel()
	all, err := c.propfind(ctx, remote, "1")
	if err != nil {
		return nil, err
	}
	selfPath, _ := c.hrefToPath(c.resolve(remote))
	filtered := make([]Resource, 0, len(all))
	for _, r := range all {
		if r.Path == selfPath {
			continue // the collection itself
		}
		if r.Path == "" {
			continue
		}
		// Defensive: some servers ignore Depth and return grandchildren.
		if parentPath := strings.Trim(remote, "/"); parentPath != "" {
			rel := strings.TrimPrefix(r.Path, parentPath)
			if strings.Count(strings.Trim(rel, "/"), "/") > 0 {
				continue
			}
		} else if strings.Count(r.Path, "/") > 0 {
			continue
		}
		filtered = append(filtered, r)
	}
	return filtered, nil
}

// Stat returns metadata for a single remote path, or ErrNotFound.
func (c *Client) Stat(parent context.Context, remote string) (*Resource, error) {
	ctx, cancel := c.metaContext(parent)
	defer cancel()
	all, err := c.propfind(ctx, remote, "0")
	if err != nil {
		return nil, err
	}
	if len(all) == 0 {
		return nil, ErrNotFound
	}
	// Prefer the entry that matches the requested path.
	want := strings.Trim(remote, "/")
	for i := range all {
		if all[i].Path == want {
			return &all[i], nil
		}
	}
	return &all[0], nil
}

// Exists reports whether a remote path is present.
func (c *Client) Exists(ctx context.Context, remote string) (bool, error) {
	_, err := c.Stat(ctx, remote)
	if errors.Is(err, ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}

// Mkdir creates a single collection. A 405 (already exists) is not an error.
func (c *Client) Mkdir(parent context.Context, remote string) error {
	ctx, cancel := c.metaContext(parent)
	defer cancel()
	req, err := c.newRequest(ctx, "MKCOL", remote, nil)
	if err != nil {
		return err
	}
	resp, err := c.do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	switch resp.StatusCode {
	case http.StatusCreated, http.StatusOK, http.StatusNoContent,
		http.StatusMethodNotAllowed, http.StatusConflict:
		return nil
	default:
		// 301/302 without auto-follow: retry against the redirect target once.
		if loc := resp.Header.Get("Location"); loc != "" && resp.StatusCode/100 == 3 {
			return c.Mkdir(parent, loc)
		}
		return statusError("MKCOL "+displayPath(remote), resp)
	}
}

// MkdirAll creates a remote directory and every missing parent.
//
// It deliberately does not trust the MKCOL status code alone. Servers answer
// 409 (missing parent) or even 201 without actually applying the change in
// enough real-world configurations that a silent no-op here only surfaces much
// later as a 404 on the PUT — far away from the cause. Every level is therefore
// verified with a PROPFIND before moving on.
func (c *Client) MkdirAll(parent context.Context, remote string) error {
	remote = strings.Trim(strings.ReplaceAll(remote, "\\", "/"), "/")
	if remote == "" {
		return nil
	}
	segs := strings.Split(remote, "/")
	cur := ""
	for _, s := range segs {
		if s == "" {
			continue
		}
		if cur == "" {
			cur = s
		} else {
			cur = cur + "/" + s
		}
		if exists, err := c.Exists(parent, cur); err == nil && exists {
			continue
		}
		if err := c.Mkdir(parent, cur); err != nil {
			return err
		}
		exists, err := c.Exists(parent, cur)
		if err != nil {
			return fmt.Errorf("无法确认远端目录 /%s 是否创建成功：%w", cur, err)
		}
		if !exists {
			return fmt.Errorf("服务端对创建目录 /%s 返回成功，但随后查询不到该目录；"+
				"请检查 OpenList/网盘里这个存储驱动是否允许写入", cur)
		}
	}
	return nil
}

// ---------------------------------------------------------------------------
// PUT / GET
// ---------------------------------------------------------------------------

// uploader is implemented by bodies whose reader can be rewound, which lets us
// retry a request after a redirect or a transient failure.
type rewindable interface {
	io.Reader
	Seek(offset int64, whence int) (int64, error)
}

// Upload streams r to remotePath. size may be -1 when unknown, in which case
// the request uses chunked transfer encoding.
func (c *Client) Upload(ctx context.Context, remotePath string, r io.Reader, size int64) error {
	req, err := c.newRequest(ctx, http.MethodPut, remotePath, r)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/octet-stream")
	if size >= 0 {
		req.ContentLength = size
	}
	// Allow Go to replay the body if the transport retries (idle conn reuse).
	if seeker, ok := r.(rewindable); ok {
		req.GetBody = func() (io.ReadCloser, error) {
			if _, err := seeker.Seek(0, io.SeekStart); err != nil {
				return nil, err
			}
			return io.NopCloser(seeker), nil
		}
	}

	resp, err := c.do(req)
	if err != nil {
		return err
	}
	defer drainClose(resp.Body)

	switch resp.StatusCode {
	case http.StatusOK, http.StatusCreated, http.StatusNoContent, http.StatusAccepted:
		return nil
	case http.StatusLengthRequired:
		return fmt.Errorf("dav: server requires a Content-Length for PUT; " +
			"enable local staging for this job")
	default:
		return statusError("PUT "+displayPath(remotePath), resp)
	}
}

// UploadFile uploads a local file, reusing the same handle for retries.
func (c *Client) UploadFile(ctx context.Context, localPath, remotePath string) (int64, error) {
	f, err := os.Open(localPath)
	if err != nil {
		return 0, fmt.Errorf("open local file: %w", err)
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return 0, fmt.Errorf("stat local file: %w", err)
	}
	if err := c.Upload(ctx, remotePath, f, st.Size()); err != nil {
		return 0, err
	}
	return st.Size(), nil
}

// Download streams remotePath into w.
func (c *Client) Download(ctx context.Context, remotePath string, w io.Writer) error {
	req, err := c.newRequest(ctx, http.MethodGet, remotePath, nil)
	if err != nil {
		return err
	}
	resp, err := c.do(req)
	if err != nil {
		return err
	}
	defer drainClose(resp.Body)

	switch resp.StatusCode {
	case http.StatusOK, http.StatusPartialContent:
	default:
		if resp.StatusCode == http.StatusNotFound {
			return ErrNotFound
		}
		return statusError("GET "+displayPath(remotePath), resp)
	}
	if _, err := io.Copy(w, resp.Body); err != nil {
		return fmt.Errorf("dav: read %s: %w", displayPath(remotePath), err)
	}
	return nil
}

// DownloadToFile streams remotePath into a local file, creating parents.
func (c *Client) DownloadToFile(ctx context.Context, remotePath, localPath string) (int64, error) {
	if err := os.MkdirAll(path.Dir(localPath), 0o755); err != nil {
		return 0, fmt.Errorf("create local dir: %w", err)
	}
	f, err := os.OpenFile(localPath, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o644)
	if err != nil {
		return 0, fmt.Errorf("create local file: %w", err)
	}
	written, copyErr := func() (int64, error) {
		defer f.Close()
		if err := c.Download(ctx, remotePath, f); err != nil {
			return 0, err
		}
		st, err := f.Stat()
		if err != nil {
			return 0, err
		}
		return st.Size(), nil
	}()
	if copyErr != nil {
		_ = os.Remove(localPath) // do not leave a truncated archive behind
		return 0, copyErr
	}
	return written, nil
}

// ReadRange fetches the first n bytes of a remote file. Used to verify that an
// upload actually landed on the storage: cloud aggregators (OpenList + netdisk
// drivers) sometimes answer PUT with 201 while the transfer silently dies
// server-side, and the only way to tell is to read something back. A server
// that ignores the Range header answers 200 with the full body — the returned
// slice is capped at n bytes either way. A non-2xx answer (some servers refuse
// Range outright) is returned as an error so the caller can decide whether to
// skip the check instead of failing the backup.
func (c *Client) ReadRange(parent context.Context, remotePath string, n int64) ([]byte, error) {
	ctx, cancel := c.metaContext(parent)
	defer cancel()
	req, err := c.newRequest(ctx, http.MethodGet, remotePath, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Range", fmt.Sprintf("bytes=0-%d", n-1))
	resp, err := c.do(req)
	if err != nil {
		return nil, err
	}
	defer drainClose(resp.Body)

	switch resp.StatusCode {
	case http.StatusOK, http.StatusPartialContent:
	default:
		if resp.StatusCode == http.StatusNotFound {
			return nil, ErrNotFound
		}
		return nil, statusError("GET "+displayPath(remotePath), resp)
	}
	buf := make([]byte, n)
	got, readErr := io.ReadFull(io.LimitReader(resp.Body, n), buf)
	if readErr != nil && readErr != io.EOF && readErr != io.ErrUnexpectedEOF {
		return nil, fmt.Errorf("dav: read %s: %w", displayPath(remotePath), readErr)
	}
	return buf[:got], nil
}

// Delete removes a remote file or collection.
func (c *Client) Delete(parent context.Context, remotePath string) error {
	ctx, cancel := c.metaContext(parent)
	defer cancel()
	req, err := c.newRequest(ctx, http.MethodDelete, remotePath, nil)
	if err != nil {
		return err
	}
	resp, err := c.do(req)
	if err != nil {
		return err
	}
	defer drainClose(resp.Body)
	switch resp.StatusCode {
	case http.StatusOK, http.StatusNoContent, http.StatusAccepted, http.StatusNotFound:
		return nil
	default:
		return statusError("DELETE "+displayPath(remotePath), resp)
	}
}

// Ping verifies connectivity and credentials with an OPTIONS request, falling
// back to a Depth:0 PROPFIND when the server answers OPTIONS with 405.
func (c *Client) Ping(parent context.Context) error {
	ctx, cancel := c.metaContext(parent)
	defer cancel()

	// The base URL denotes a collection, so probe it as one. Sending OPTIONS to
	// the slash-less form is what makes nginx/Apache DAV location blocks answer
	// 301/405 in the first place.
	req, err := c.probeRequest(ctx, http.MethodOptions)
	if err != nil {
		return err
	}
	resp, err := c.do(req)
	if err != nil {
		return err
	}

	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		drainClose(resp.Body)
		return nil
	}
	if resp.StatusCode == http.StatusUnauthorized {
		defer drainClose(resp.Body)
		return statusError("OPTIONS", resp)
	}

	// Some servers do not implement OPTIONS at all, and proxies in front of a
	// perfectly good DAV endpoint routinely refuse it. So an OPTIONS failure
	// must never be reported on its own — always confirm with a real PROPFIND.
	optErr := statusError("OPTIONS", resp) // consumes the body
	drainClose(resp.Body)

	switch resp.StatusCode {
	case http.StatusMethodNotAllowed, http.StatusNotFound, http.StatusNotImplemented:
		if _, statErr := c.Stat(ctx, ""); statErr == nil {
			return nil
		} else {
			// Report both attempts. Previously only the PROPFIND error
			// surfaced, which made a proxy-side refusal look like the server
			// hating PROPFIND specifically.
			return fmt.Errorf("%w；改用 PROPFIND 探测同样失败：%v", optErr, statErr)
		}
	}
	return optErr
}

// probeRequest builds a metadata request against the base collection URL.
func (c *Client) probeRequest(ctx context.Context, method string) (*http.Request, error) {
	req, err := http.NewRequestWithContext(ctx, method, c.resolveDir(""), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", c.userAgent)
	if c.username != "" || c.password != "" {
		req.SetBasicAuth(c.username, c.password)
	}
	return req, nil
}

func drainClose(rc io.ReadCloser) {
	if rc == nil {
		return
	}
	_, _ = io.Copy(io.Discard, io.LimitReader(rc, 64<<10))
	_ = rc.Close()
}

func displayPath(p string) string {
	p = strings.TrimSpace(p)
	if p == "" {
		return "/"
	}
	return "/" + strings.Trim(p, "/")
}

func parseHTTPDate(s string) (time.Time, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return time.Time{}, errors.New("empty date")
	}
	for _, layout := range []string{http.TimeFormat, time.RFC1123, time.RFC1123Z,
		"Mon, 02 Jan 2006 15:04:05 GMT", "Mon, 2 Jan 2006 15:04:05 GMT",
		time.RFC3339, "2006-01-02T15:04:05Z", "2006-01-02 15:04:05"} {
		if t, err := time.Parse(layout, s); err == nil {
			return t, nil
		}
	}
	return time.Time{}, fmt.Errorf("unrecognised date %q", s)
}
