package dav

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// ProbeStep is one diagnostic request and what came back.
type ProbeStep struct {
	Label    string
	Method   string
	URL      string
	Status   int
	OK       bool
	Detail   string
	Allow    string
	DAV      string
	Server   string
	Location string
}

// String renders the step for terminal output.
func (s ProbeStep) String() string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s %s\n", s.Method, s.URL)
	if s.Status == 0 {
		fmt.Fprintf(&b, "    → 请求未能完成：%s\n", s.Detail)
		return b.String()
	}
	fmt.Fprintf(&b, "    → HTTP %d %s", s.Status, http.StatusText(s.Status))
	if s.OK {
		b.WriteString("  ✓")
	}
	b.WriteString("\n")
	for _, h := range []struct{ k, v string }{
		{"Allow", s.Allow}, {"DAV", s.DAV}, {"Server", s.Server}, {"Location", s.Location},
	} {
		if h.v != "" {
			fmt.Fprintf(&b, "      %s: %s\n", h.k, h.v)
		}
	}
	if s.Detail != "" {
		fmt.Fprintf(&b, "      body: %s\n", s.Detail)
	}
	return b.String()
}

// ProbeReport is the outcome of a full connectivity diagnosis.
type ProbeReport struct {
	BaseURL  string // the normalised endpoint actually used
	Username string
	Steps    []ProbeStep
	OK       bool
	Verdict  string
}

// Probe walks an endpoint method by method and explains what it finds. It is
// read-only: no MKCOL, PUT or DELETE is ever issued, so it is safe to run
// against a production WebDAV account before saving a profile.
//
// Rationale: a bare "405 Method Not Allowed" is ambiguous — the URL may not be a
// DAV root, the collection may need a trailing slash, or a proxy in front may
// allowlist only GET/POST. Probing each form separately turns that guesswork
// into a definite answer.
func (c *Client) Probe(parent context.Context) *ProbeReport {
	ctx, cancel := c.metaContext(parent)
	defer cancel()

	rep := &ProbeReport{BaseURL: c.resolveDir(""), Username: c.username}

	root := c.resolveDir("")   // collection form, with trailing slash
	bare := c.resolve("")      // the same path without it

	// 1. OPTIONS. Cheap, and the DAV/Allow headers are the clearest signal of
	//    what the server is willing to do.
	opt := c.probeStep(ctx, "OPTIONS 集合根", http.MethodOptions, root, "", nil)
	rep.Steps = append(rep.Steps, opt)

	// 2. PROPFIND Depth:0 — the operation the backup tool actually needs.
	d0 := c.probeStep(ctx, "PROPFIND Depth:0 集合根", "PROPFIND", root, "0", strings.NewReader(propfindBody))
	rep.Steps = append(rep.Steps, d0)

	// 3. PROPFIND Depth:1 — used to enumerate archives for retention.
	d1 := c.probeStep(ctx, "PROPFIND Depth:1 集合根", "PROPFIND", root, "1", strings.NewReader(propfindBody))
	rep.Steps = append(rep.Steps, d1)

	// 4. Only when the collection form failed is the slash-less form worth
	//    showing: if that one succeeds, the trailing slash is the whole story.
	//    Skipped when the two forms are literally the same URL (a base with no
	//    path prefix resolves to "/" either way) so the report stays honest.
	if !d0.OK && bare != root {
		bare0 := c.probeStep(ctx, "PROPFIND Depth:0 无结尾斜杠", "PROPFIND", bare, "0", strings.NewReader(propfindBody))
		rep.Steps = append(rep.Steps, bare0)
	}

	rep.OK = d0.OK
	rep.Verdict = verdict(rep)
	return rep
}

func (c *Client) probeStep(ctx context.Context, label, method, url, depth string, body io.Reader) ProbeStep {
	step := ProbeStep{Label: label, Method: method, URL: url}

	req, err := http.NewRequestWithContext(ctx, method, url, body)
	if err != nil {
		step.Detail = err.Error()
		return step
	}
	req.Header.Set("User-Agent", c.userAgent)
	if c.username != "" || c.password != "" {
		req.SetBasicAuth(c.username, c.password)
	}
	if method == "PROPFIND" {
		req.Header.Set("Content-Type", "application/xml; charset=utf-8")
		req.Header.Set("Depth", depth)
	}

	resp, err := c.do(req)
	if err != nil {
		step.Detail = err.Error()
		return step
	}
	step.Status = resp.StatusCode
	step.Allow = resp.Header.Get("Allow")
	step.DAV = resp.Header.Get("DAV")
	step.Server = resp.Header.Get("Server")
	step.Location = resp.Header.Get("Location")
	step.OK = resp.StatusCode >= 200 && resp.StatusCode < 300
	if !step.OK {
		step.Detail = readDetail(resp)
	} else {
		drainClose(resp.Body)
		return step
	}
	drainClose(resp.Body)
	return step
}

// verdict turns the step results into one actionable sentence.
func verdict(rep *ProbeReport) string {
	var opt, d0, d1 ProbeStep
	for _, s := range rep.Steps {
		switch {
		case s.Method == http.MethodOptions:
			opt = s
		case strings.Contains(s.Label, "Depth:1"):
			d1 = s
		case strings.Contains(s.Label, "Depth:0") && !strings.Contains(s.Label, "无结尾斜杠") && d0.Status == 0:
			d0 = s
		}
	}

	if d0.OK {
		// Depth:0 is what Ping/Stat need, so the endpoint is usable — but List
		// (archive enumeration and retention) rides on Depth:1, and silently
		// reporting "fine" when that half is broken would hide a real defect.
		if !d1.OK {
			return "集合本身可用，但列目录（PROPFIND Depth:1）失败：" +
				strings.TrimRight(d1Detail(d1), ".。 ") +
				"。备份上传仍然可以完成，但控制台里的远端归档列表和保留策略可能不正常。"
		}
		if opt.Status == http.StatusMethodNotAllowed || opt.Status == http.StatusNotFound ||
			opt.Status == http.StatusNotImplemented {
			return "该地址可正常用作备份目标；服务端不支持 OPTIONS，但这不影响备份与恢复。" +
				"确认无误后可以直接保存这个配置。"
		}
		return "连接与认证均通过，该地址可正常用作备份目标。"
	}

	switch d0.Status {
	case 0:
		return "无法建立连接：" + strings.TrimRight(d0.Detail, ".。 ") + "。" +
			"检查地址是否可达、端口是否被防火墙挡住、证书是否有效" +
			"（自签证书可以勾选「跳过证书校验」）。"
	case http.StatusUnauthorized:
		return "认证失败（401）。用户名或密码不对；不少网盘要求填「应用专用密码」而不是登录密码，" +
			"例如坚果云需要在网页端生成应用密码。"
	case http.StatusForbidden:
		return "服务端拒绝访问（403）。该账号对这个目录没有权限，或这个路径被服务端限制。"
	case http.StatusNotFound:
		return "路径不存在（404）。URL 很可能没有指向 WebDAV 根目录：" +
			"对照服务端文档补上前缀，例如 /dav/、/webdav/、/remote.php/dav/files/<用户名>/。"
	case http.StatusMethodNotAllowed:
		if hasBareSuccess(rep) {
			return "集合路径必须带结尾斜杠：不带斜杠时服务端返回 405，带斜杠则正常。" +
				"请在 URL 末尾补上 /。"
		}
		return "服务端不接受 PROPFIND（405）。三种可能：① URL 指向的不是 WebDAV 根目录；" +
			"② 前面隔着只放行 GET/POST 的反向代理或 CDN（如 EdgeOne、Cloudflare 默认不转发 WebDAV 方法）；" +
			"③ 该服务本身不提供 WebDAV，只是一个普通网盘网页。"
	case http.StatusMovedPermanently, http.StatusFound, http.StatusSeeOther, http.StatusTemporaryRedirect:
		loc := ""
		for _, s := range rep.Steps {
			if s.Location != "" {
				loc = s.Location
				break
			}
		}
		if loc != "" {
			return "地址被重定向到 " + loc + "，请直接把 WebDAV 地址改成这个目标地址。"
		}
		return "地址被重定向，请使用服务端给出的最终地址。"
	case http.StatusNotImplemented:
		return "服务端未实现 PROPFIND（501），这个地址看起来不是 WebDAV 服务。"
	}
	if d0.Status >= 500 {
		return fmt.Sprintf("服务端内部错误（HTTP %d）。稍后重试；若持续出现，检查该 WebDAV 服务的运行状态。", d0.Status)
	}
	return fmt.Sprintf("探测失败：HTTP %d %s。%s", d0.Status, http.StatusText(d0.Status), d0.Detail)
}

func d1Detail(s ProbeStep) string {
	if s.Status == 0 {
		return s.Detail
	}
	if s.Detail != "" {
		return fmt.Sprintf("HTTP %d %s（%s）", s.Status, http.StatusText(s.Status), s.Detail)
	}
	return fmt.Sprintf("HTTP %d %s", s.Status, http.StatusText(s.Status))
}

func hasBareSuccess(rep *ProbeReport) bool {
	for _, s := range rep.Steps {
		if strings.Contains(s.Label, "无结尾斜杠") && s.OK {
			return true
		}
	}
	return false
}
