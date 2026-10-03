package dav

import (
	"fmt"
	"io"
	"net/http"
	"strings"
)

// StatusError is a non-2xx response from the WebDAV server. It keeps the
// headers that actually help when a request is refused (Allow, Server), because
// the body is very often an unhelpful proxy or CDN error page.
type StatusError struct {
	Op     string // e.g. "PROPFIND /backups"
	Code   int
	Status string
	Detail string // trimmed response body, if it carried anything readable
	Allow  string // Allow header, when the server sent one
	Server string // Server header — frequently reveals a proxy/CDN in the path
}

func (e *StatusError) Error() string {
	var b strings.Builder
	fmt.Fprintf(&b, "dav: %s: HTTP %d %s", e.Op, e.Code, e.Status)
	if e.Detail != "" {
		b.WriteString(": ")
		b.WriteString(e.Detail)
	}
	if h := e.hint(); h != "" {
		b.WriteString(" [")
		b.WriteString(h)
		b.WriteString("]")
	}
	return b.String()
}

// collectionRetriable reports whether the failure could plausibly be caused by
// using the wrong form (with or without a trailing slash) of a collection URL.
// 400 is included because some servers reject a body on a non-collection URL
// that way; 301/302/303 because Go refuses to replay PROPFIND across a redirect
// and hands us the redirect itself instead.
func (e *StatusError) collectionRetriable() bool {
	switch e.Code {
	case http.StatusMovedPermanently, http.StatusFound, http.StatusSeeOther,
		http.StatusMethodNotAllowed, http.StatusBadRequest:
		return true
	}
	return false
}

// hint renders operator-facing advice. The console shows dav errors verbatim,
// so this text is what the user actually reads.
func (e *StatusError) hint() string {
	var parts []string
	switch e.Code {
	case http.StatusUnauthorized:
		parts = append(parts, "检查用户名 / 密码；不少网盘要求填「应用专用密码」而不是登录密码")
	case http.StatusForbidden:
		parts = append(parts, "服务端拒绝访问该路径，检查目录权限，或路径是否被服务端限制")
	case http.StatusConflict:
		parts = append(parts, "父目录不存在：先在远端建好父目录，或让任务自动创建")
	case http.StatusInsufficientStorage:
		parts = append(parts, "远端配额已满或超出单文件上限")
	case http.StatusMethodNotAllowed:
		parts = append(parts, "该地址不接受这个 WebDAV 方法。"+
			"常见原因：① URL 没指向 WebDAV 根（如漏了 /dav、/webdav、/remote.php/dav/files/<用户>/）；"+
			"② 集合路径缺结尾斜杠；"+
			"③ 前面隔着只放行 GET/POST 的反向代理或 CDN")
		if e.Allow != "" {
			parts = append(parts, "服务端声明 Allow: "+e.Allow)
		}
	case http.StatusNotImplemented:
		parts = append(parts, "服务端未实现该方法，可能不是 WebDAV 服务")
	case http.StatusBadRequest:
		parts = append(parts, "服务端认为请求不合法：检查 URL 与远端路径是否含有异常字符")
	case http.StatusNotFound:
		parts = append(parts, "远端路径不存在")
	case http.StatusTooManyRequests:
		parts = append(parts, "被服务端限流，稍后重试或降低并发")
	}
	if e.Code != http.StatusMethodNotAllowed && e.Allow != "" {
		parts = append(parts, "服务端声明 Allow: "+e.Allow)
	}
	if e.Server != "" {
		parts = append(parts, "Server: "+e.Server)
	}
	return strings.Join(parts, "；")
}

// statusError converts a non-2xx response into an informative error.
func statusError(op string, resp *http.Response) error {
	return &StatusError{
		Op:     op,
		Code:   resp.StatusCode,
		Status: http.StatusText(resp.StatusCode),
		Detail: readDetail(resp),
		Allow:  resp.Header.Get("Allow"),
		Server: resp.Header.Get("Server"),
	}
}

// readDetail drains a bounded amount of the response body and turns it into a
// single line without markup, suitable for an error message. It must be called
// before the body is closed.
func readDetail(resp *http.Response) string {
	if resp == nil || resp.Body == nil {
		return ""
	}
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
	msg := strings.TrimSpace(string(body))
	// Strip XML/HTML noise so the operator sees the message, not markup.
	if strings.HasPrefix(msg, "<") {
		if i := strings.Index(msg, ">"); i >= 0 {
			if j := strings.LastIndex(msg, "<"); j > i {
				msg = strings.TrimSpace(msg[i+1 : j])
			}
		}
	}
	msg = strings.Join(strings.Fields(msg), " ")
	if len(msg) > 200 {
		msg = msg[:200] + "..."
	}
	return msg
}
