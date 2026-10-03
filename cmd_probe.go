package main

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/qzrsa/qzrs-webdav-backup/internal/dav"
)

// runDAVProbe implements -dav-probe: a read-only, step-by-step diagnosis of a
// WebDAV endpoint. It runs before the configuration store is loaded so it keeps
// working even when the state directory is missing or corrupt, and it never
// issues a mutating request — safe to run against a production account.
func runDAVProbe(rawURL, user, pass, dir string, insecure bool, timeout int) int {
	if pass == "" {
		pass = os.Getenv("WDB_DAV_PASSWORD")
	}
	if timeout <= 0 {
		timeout = 20
	}

	client, err := dav.New(dav.Config{
		BaseURL:     rawURL,
		Username:    user,
		Password:    pass,
		InsecureTLS: insecure,
		Timeout:     time.Duration(timeout) * time.Second,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "地址无效：%v\n", err)
		return 2
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(timeout)*time.Second)
	defer cancel()

	rep := client.Probe(ctx)

	sep := strings.Repeat("-", 64)
	fmt.Println(sep)
	fmt.Println("  qzrs-webdav-backup WebDAV 连接诊断（只读，不修改远端任何数据）")
	fmt.Println(sep)
	fmt.Printf("  输入地址 : %s\n", rawURL)
	fmt.Printf("  实际请求 : %s\n", rep.BaseURL)
	fmt.Printf("  用户名   : %s\n", orDash(rep.Username))
	fmt.Printf("  跳过证书 : %v\n", insecure)
	fmt.Println()

	for i, step := range rep.Steps {
		fmt.Printf("[%d/%d] %s\n", i+1, len(rep.Steps), step.Label)
		fmt.Print(indentLines(step.String(), "    "))
	}

	if dir != "" && rep.OK {
		clean := strings.Trim(strings.ReplaceAll(dir, "\\", "/"), "/")
		entries, err := client.List(ctx, clean)
		fmt.Printf("[附加] 列目录 /%s\n", clean)
		if err != nil {
			fmt.Printf("    → 失败：%v\n", err)
		} else {
			archives, totalBytes := 0, int64(0)
			for _, e := range entries {
				if !e.IsDir && (strings.HasSuffix(e.Name, ".tar.gz") || strings.HasSuffix(e.Name, ".tar")) {
					archives++
					totalBytes += e.Size
				}
			}
			fmt.Printf("    → 成功：%d 个条目，其中 %d 个归档（%.2f KiB）\n",
				len(entries), archives, float64(totalBytes)/1024)
		}
		fmt.Println()
	}

	fmt.Println(sep)
	if rep.OK {
		fmt.Println("  结论：可用于备份 ✓")
	} else {
		fmt.Println("  结论：无法使用 ✗")
	}
	for _, line := range wrapText(rep.Verdict, 58) {
		fmt.Println("  " + line)
	}
	fmt.Println(sep)
	if !rep.OK {
		return 1
	}
	return 0
}

func orDash(s string) string {
	if strings.TrimSpace(s) == "" {
		return "(未提供)"
	}
	return s
}

func indentLines(s, prefix string) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	for i, l := range lines {
		if l != "" {
			lines[i] = prefix + l
		}
	}
	return strings.Join(lines, "\n") + "\n"
}

// wrapText breaks a sentence into lines no wider than width terminal columns.
// CJK runs count as two columns, and breaks are preferred after spaces and
// punctuation so URLs and status codes are not sliced in half.
func wrapText(s string, width int) []string {
	if width < 8 {
		width = 8
	}
	var out []string
	var line []rune
	cols := 0

	flush := func() {
		if len(line) > 0 {
			out = append(out, strings.TrimRight(string(line), " "))
			line, cols = nil, 0
		}
	}

	for _, r := range s {
		if r == '\n' {
			flush()
			continue
		}
		line = append(line, r)
		cols += runeWidth(r)
		if cols < width {
			continue
		}
		// Prefer a break after a space or CJK punctuation; never search past
		// the halfway point, or a single long token would leave a stub.
		cut := len(line)
		seen := 0
		for i := range line {
			seen += runeWidth(line[i])
			if seen > width/2 && isBreakRune(line[i]) {
				cut = i + 1
			}
		}
		out = append(out, strings.TrimRight(string(line[:cut]), " "))
		rest := append([]rune(nil), line[cut:]...)
		line, cols = rest, 0
		for _, rr := range rest {
			cols += runeWidth(rr)
		}
	}
	flush()
	return out
}

func isBreakRune(r rune) bool {
	return r == ' ' || strings.ContainsRune("，。；：、）,;:", r)
}

// runeWidth is a cheap terminal-width estimate: enough to keep CJK guidance
// text aligned without pulling in a full wcwidth table.
func runeWidth(r rune) int {
	switch {
	case r == 0x200d || (r >= 0x0300 && r <= 0x036f):
		return 0 // combining marks
	case r >= 0x1100 && (r <= 0x115f || // Hangul Jamo
		r == 0x2329 || r == 0x232a ||
		(r >= 0x2e80 && r <= 0xa4cf && r != 0x303f) || // CJK radicals … Yi
		(r >= 0xac00 && r <= 0xd7a3) || // Hangul syllables
		(r >= 0xf900 && r <= 0xfaff) ||
		(r >= 0xfe30 && r <= 0xfe6f) ||
		(r >= 0xff00 && r <= 0xff60) ||
		(r >= 0xffe0 && r <= 0xffe6) ||
		(r >= 0x20000 && r <= 0x3fffd)):
		return 2
	}
	return 1
}
