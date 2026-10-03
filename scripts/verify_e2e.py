#!/usr/bin/env python
# -*- coding: utf-8 -*-
"""qzrs-webdav-backup 端到端验证。

在一台真实的 OpenWrt 设备上完整走一遍：全新安装 → 连接测试 → 创建任务 →
真实备份 → 归档浏览 → 预览 → 试运行恢复 → 正式恢复 → 校验还原内容。

远端 WebDAV 端点由本脚本临时部署的 davd（testdav 的独立构建）提供，
用完即清理，不影响设备上已有的服务。

脚本会先卸载旧实例（含数据目录）再全新安装，因此会生成新的管理员密码。为了不把
操作者手里的密码换掉，安装前的 config.json 会被另存并在收尾时自动还原；确实想要
一个新密码时加 --reset-admin。

用法：
    python verify_e2e.py [--host 192.168.11.1] [--password xxx]
                         [--version 1.0.2] [--keep] [--reset-admin] [--out out_e2e.txt]
"""
import argparse
import http.cookiejar
import json
import os
import re
import sys
import tarfile
import time
import urllib.error
import urllib.request

import paramiko

# 设备凭据只从环境变量读取，绝不写进仓库。
DEFAULT_HOST = os.environ.get("ROUTER_HOST", "192.168.11.1")
DEFAULT_USER = os.environ.get("ROUTER_USER", "root")
DEFAULT_PASSWORD = os.environ.get("ROUTER_PASSWORD", "")

HERE = os.path.dirname(os.path.abspath(__file__))
PROJECT = os.path.dirname(HERE)

DAV_PORT = 8099
DAV_USER = "davuser"
DAV_PASS = "davpass-x9"
DAV_ROOT = "/tmp/davroot"
# 安装前的 config.json 另存位置，收尾时用它把管理员密码还原。
SAVED_CONFIG = "/tmp/wdb-cfg-save.json"
CONSOLE_PORT = 8787


# --------------------------------------------------------------------------- SSH

def connect(host, user, password):
    c = paramiko.SSHClient()
    c.set_missing_host_key_policy(paramiko.AutoAddPolicy())
    c.connect(host, 22, user, password=password, timeout=10,
              allow_agent=False, look_for_keys=False)
    return c


def run(c, cmd, timeout=180):
    stdin, stdout, stderr = c.exec_command(cmd, timeout=timeout, get_pty=False)
    out = stdout.read().decode("utf-8", "replace")
    err = stderr.read().decode("utf-8", "replace")
    return out, err, stdout.channel.recv_exit_status()


# --------------------------------------------------------------------------- HTTP

class Console:
    """Minimal console client. Proxies are disabled explicitly: this host sets
    http_proxy, which would otherwise swallow LAN requests."""

    def __init__(self, host, port):
        self.base = "http://%s:%d" % (host, port)
        self.jar = http.cookiejar.CookieJar()
        self.opener = urllib.request.build_opener(
            urllib.request.ProxyHandler({}),
            urllib.request.HTTPCookieProcessor(self.jar),
        )

    def call(self, method, path, body=None, timeout=300):
        data = None
        headers = {"X-Requested-With": "qzrs-webdav-backup"}
        if body is not None:
            data = json.dumps(body).encode("utf-8")
            headers["Content-Type"] = "application/json"
        req = urllib.request.Request(self.base + path, data=data,
                                     headers=headers, method=method)
        try:
            with self.opener.open(req, timeout=timeout) as resp:
                raw = resp.read().decode("utf-8", "replace")
                return resp.status, (json.loads(raw) if raw.strip() else None)
        except urllib.error.HTTPError as e:
            raw = e.read().decode("utf-8", "replace")
            try:
                parsed = json.loads(raw)
            except ValueError:
                parsed = {"raw": raw[:400]}
            return e.code, parsed


# --------------------------------------------------------------------------- report

class Report:
    def __init__(self):
        self.lines = []
        self.failures = []
        self.checks = 0

    def log(self, msg=""):
        self.lines.append(str(msg))

    def section(self, title):
        self.log("")
        self.log("=" * 74)
        self.log("  " + title)
        self.log("=" * 74)

    def check(self, label, ok, detail=""):
        self.checks += 1
        mark = "PASS" if ok else "FAIL"
        if not ok:
            self.failures.append(label)
        self.log("  [%s] %s%s" % (mark, label, ("  — " + str(detail)) if detail else ""))
        return ok

    def dump(self, path):
        with open(path, "w", encoding="utf-8") as fh:
            fh.write("\n".join(self.lines) + "\n")
        return os.path.getsize(path)


# --------------------------------------------------------------------------- main

def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--host", default=DEFAULT_HOST)
    ap.add_argument("--user", default=DEFAULT_USER)
    ap.add_argument("--password", default=DEFAULT_PASSWORD)
    ap.add_argument("--version", default="1.0.2")
    ap.add_argument("--keep", action="store_true",
                    help="保留 davd 与测试数据，便于手工排查")
    ap.add_argument("--reset-admin", action="store_true",
                    help="不还原安装前的 config.json（保留本次新生成的管理员密码）")
    ap.add_argument("--out", default=os.path.join(HERE, "out_e2e.txt"))
    args = ap.parse_args()

    if not args.password:
        print("缺少设备密码：请设置环境变量 ROUTER_PASSWORD，或传 --password。",
              file=sys.stderr)
        return 2

    r = Report()
    console = Console(args.host, CONSOLE_PORT)
    c = None

    pkg = os.path.join(PROJECT, "dist",
                       "qzrs-webdav-backup-%s-linux-amd64.tar.gz" % args.version)
    davd = os.path.join(PROJECT, "dist", "davd-linux-amd64")

    for p in (pkg, davd):
        if not os.path.isfile(p):
            print("missing artifact: %s" % p, file=sys.stderr)
            return 2

    # davd 必须是 Linux ELF。本机 go.sh 默认目标为宿主平台（windows/amd64），
    # 漏掉 GOOS=linux 会产出 PE 文件，在设备上表现为 "Exec format error"。
    with open(davd, "rb") as fh:
        magic = fh.read(4)
    if magic != b"\x7fELF":
        print("dist/davd-linux-amd64 不是 Linux ELF（magic=%r）。重建：\n"
              "  GOOS=linux GOARCH=amd64 sh D:/WorkBuddy/.tools/go.sh build "
              "-o dist/davd-linux-amd64 ./tools/davd" % magic, file=sys.stderr)
        return 2

    # 发布包内主二进制必须带执行位。Windows/MSYS 下 chmod 与 tar --mode 都写不进
    # tar 头，必须由 scripts/pack.py 显式设置 —— 这里做回归断言，防止再次回归。
    with tarfile.open(pkg) as tf:
        modes = dict((m.name.lstrip("./"), m.mode) for m in tf.getmembers())
    bin_mode = modes.get("qzrs-webdav-backup", 0)
    if not (bin_mode & 0o111):
        print("发布包内 qzrs-webdav-backup 权限为 %s，缺少执行位；"
              "请确认 deploy/build.sh 走的是 scripts/pack.py。"
              % oct(bin_mode), file=sys.stderr)
        return 2
    print("artifact ok: davd=ELF, pkg qzrs-webdav-backup mode=%s" % oct(bin_mode))

    try:
        r.section("0. 连接设备")
        c = connect(args.host, args.user, args.password)
        out, _, _ = run(c, "uname -m; cat /etc/openwrt_release | grep -i description")
        r.log(out.strip())
        r.check("SSH 连接成功", True)

        # ------------------------------------------------------------------
        r.section("1. 部署测试 WebDAV 端点 (davd)")
        sftp = c.open_sftp()
        sftp.put(davd, "/tmp/davd")
        sftp.close()
        out, err, _ = run(c,
            "chmod +x /tmp/davd && killall davd 2>/dev/null; "
            "rm -rf %s && mkdir -p %s && echo prepared" % (DAV_ROOT, DAV_ROOT))
        r.log(out.strip())

        # 端点模拟真实部署（OpenList 等）：DAV 服务挂在 /dav 前缀之下，
        # profile URL 因此写成 http://127.0.0.1:<port>/dav。
        run(c, "nohup /tmp/davd -listen 127.0.0.1:%d -root %s -user %s -pass %s "
               "-prefix /dav > /tmp/davd.log 2>&1 & echo launched"
            % (DAV_PORT, DAV_ROOT, DAV_USER, DAV_PASS))
        time.sleep(2)

        out, _, _ = run(c, "ps w | grep '[d]avd' | head -3; echo '--- log ---'; cat /tmp/davd.log")
        r.log(out.strip())
        r.check("davd 已启动", "davd" in out)

        out, _, _ = run(c,
            "wget -q -O - -T 4 --header='Authorization: Basic ' "
            "-S http://127.0.0.1:%d/ 2>&1 | head -20" % DAV_PORT)
        r.log(out.strip())

        # ------------------------------------------------------------------
        r.section("2. 全新安装 qzrs-webdav-backup")

        sftp = c.open_sftp()
        remote_pkg = "/tmp/%s" % os.path.basename(pkg)
        sftp.put(pkg, remote_pkg)
        sftp.close()
        r.log("uploaded %s" % os.path.basename(pkg))

        # 卸载旧实例：从刚上传的包里解出 uninstall.sh，不要依赖设备上可能残留的
        # /tmp/wdb-install。那个目录被清掉时 cd 会失败、卸载静默跳过，随后安装
        # 检测到已有服务便走"增量"分支而不打印初始密码，看起来像安装失败。
        #
        # 先用 -p 卸载会删掉数据目录，重新安装就会生成一个新的管理员密码。反复
        # 跑回归会把操作者手里的密码一次次换掉 —— 那正是最容易把人挡在门外的坑。
        # 所以这里先把 config.json（含密码哈希）另存，收尾时再放回去。
        out, _, _ = run(c,
            "if [ -f /etc/qzrs-webdav-backup/config.json ]; then "
            "cp /etc/qzrs-webdav-backup/config.json %s && echo SAVED; else echo NONE; fi"
            % SAVED_CONFIG)
        cfg_saved = "SAVED" in out
        r.log("安装前 config.json：%s" % ("已另存，收尾时还原" if cfg_saved else "不存在"))

        out, _, _ = run(c,
            "rm -rf /tmp/wdb-uninst && mkdir -p /tmp/wdb-uninst && "
            "tar -xzf %s -C /tmp/wdb-uninst && cd /tmp/wdb-uninst && "
            "chmod +x uninstall.sh && ./uninstall.sh -p -y 2>&1 | tail -15"
            % remote_pkg)
        r.log(out.strip())

        # Kill anything still holding the port before reinstalling.
        run(c, "killall qzrs-webdav-backup 2>/dev/null; sleep 1; "
               "rm -rf /tmp/wdb-install; echo cleaned")

        out, err, st = run(c,
            "mkdir -p /tmp/wdb-install && "
            "tar -xzf %s -C /tmp/wdb-install && " % remote_pkg +
            "cd /tmp/wdb-install && chmod +x install.sh && ./install.sh 2>&1",
            timeout=300)
        r.log("install exit=%d" % st)
        r.log(out.strip())
        if err.strip():
            r.log("[stderr] " + err.strip())

        r.check("安装脚本正常退出", st == 0, "exit=%d" % st)

        m = re.search(r"初始密码\s*:\s*(\S+)", out)
        initial_pw = m.group(1) if m else None
        r.check("安装时打印了初始密码", bool(initial_pw), initial_pw)

        out, _, _ = run(c, "ps w | grep '[w]ebdav-backup' | head -3")
        r.log(out.strip())
        r.check("服务进程存在", "qzrs-webdav-backup" in out)

        out, _, _ = run(c,
            "cat /proc/$(pidof qzrs-webdav-backup | awk '{print $1}')/status 2>/dev/null "
            "| grep -E 'VmRSS|VmSize|Threads'")
        r.log("内存占用：\n" + out.strip())
        m2 = re.search(r"VmRSS:\s*(\d+)\s*kB", out)
        if m2:
            rss_mb = int(m2.group(1)) / 1024.0
            r.check("常驻内存合理 (<80MB)", rss_mb < 80, "%.1f MB" % rss_mb)

        if not initial_pw:
            r.log("!! 未取到初始密码，后续 API 步骤无法进行")
            return finish(r, args, c, ok=False)

        # ------------------------------------------------------------------
        r.section("3. 登录控制台")
        st, body = console.call("POST", "/api/login",
                                {"username": "admin", "password": initial_pw},
                                timeout=30)
        r.check("登录成功", st == 200, "HTTP %d %s" % (st, body))
        if st != 200:
            return finish(r, args, c, ok=False)

        st, me = console.call("GET", "/api/me")
        r.check("会话有效", st == 200 and me and me.get("authenticated"), me)

        st, status = console.call("GET", "/api/status")
        r.check("读取系统状态", st == 200 and status and status.get("version") == args.version,
                status.get("version") if status else None)
        r.log("  platform=%s go=%s disk=%.0f%% free=%s" % (
            status.get("platform"), status.get("go_version"),
            status.get("disk", {}).get("used_percent", 0),
            status.get("disk", {}).get("free")))

        # ------------------------------------------------------------------
        r.section("4. 创建 WebDAV 配置")
        st, prof = console.call("POST", "/api/profiles", {
            "name": "本地测试端点",
            "url": "http://127.0.0.1:%d/dav" % DAV_PORT,
            "username": DAV_USER,
            "password": DAV_PASS,
            "timeout_sec": 30,
        })
        r.check("创建配置", st == 201 and prof and prof.get("id"), prof)
        if st != 201:
            return finish(r, args, c, ok=False)
        profile_id = prof["id"]

        st, test = console.call("POST", "/api/profiles/%s/test" % profile_id, {})
        r.check("连接测试通过", st == 200 and test and test.get("ok"), test)
        r.log("  %s" % json.dumps(test, ensure_ascii=False))

        # A wrong password must fail cleanly rather than crash.
        st, badtest = console.call("POST", "/api/profiles/%s/test" % profile_id,
                                   {"password": "definitely-wrong"})
        r.check("错误密码被正确拒绝",
                st == 200 and badtest and not badtest.get("ok"),
                badtest.get("error", "")[:120] if badtest else None)

        # ------------------------------------------------------------------
        r.section("5. 创建备份任务")
        st, job = console.call("POST", "/api/jobs", {
            "name": "路由器配置备份",
            "comment": "端到端验证用",
            "enabled": True,
            "source": {
                "paths": ["/etc/config", "/etc/openwrt_release", "/etc/dropbear"],
                "exclude": ["*.log"],
                "one_file_system": True,
                "follow_symlinks": False,
                "max_file_size_mb": 0,
            },
            "target": {"profile_id": profile_id, "dir": "router-backups"},
            "schedule": {"mode": "manual"},
            "retention": {"keep": 3},
            "options": {"compression": "gzip", "gzip_level": 6, "exclude_caches": True},
        })
        r.check("创建任务", st == 201 and job and job.get("id"), job)
        if st != 201:
            return finish(r, args, c, ok=False)
        job_id = job["id"]

        # ------------------------------------------------------------------
        r.section("6. 执行备份")
        t0 = time.time()
        st, start = console.call("POST", "/api/jobs/%s/run" % job_id, {})
        r.check("触发备份", st == 202, "HTTP %d %s" % (st, start))

        run_rec = None
        for _ in range(120):
            time.sleep(1)
            st, listing = console.call("GET", "/api/runs?job_id=%s&limit=1" % job_id)
            runs = (listing or {}).get("runs") or []
            if runs and runs[0].get("status") != "running":
                run_rec = runs[0]
                break
        elapsed = time.time() - t0

        if not run_rec:
            st, listing = console.call("GET", "/api/runs?limit=1")
            r.log("未在超时内完成，最新记录：%s" % json.dumps(listing, ensure_ascii=False)[:400])
            r.check("备份在 120 秒内完成", False, "%.1fs" % elapsed)
            return finish(r, args, c, ok=False)

        r.check("备份成功", run_rec.get("status") == "success",
                "status=%s message=%s" % (run_rec.get("status"), run_rec.get("message")))
        r.log("  用时 %.1fs，文件 %d 个，原始 %s → 归档 %s" % (
            elapsed, run_rec.get("file_count", 0),
            run_rec.get("raw_size"), run_rec.get("archive_size")))
        r.log("  远端路径：/%s" % run_rec.get("remote_path"))

        st, log = console.call("GET", "/api/runs/%s/log?offset=0" % run_rec["id"])
        r.log("")
        r.log("  ---------- 备份日志 ----------")
        for line in (log.get("text") or "").splitlines():
            r.log("  " + line)
        r.log("  ------------------------------")
        r.check("日志包含上传确认", "上传完成" in (log.get("text") or ""))

        # ------------------------------------------------------------------
        r.section("7. 远端归档验证")
        out, _, _ = run(c, "find %s -type f | head -20; echo '--- sizes ---'; "
                           "ls -la %s/dav/router-backups/ 2>&1" % (DAV_ROOT, DAV_ROOT))
        r.log(out.strip())
        r.check("远端存在归档文件", ".tar.gz" in out)

        st, archives = console.call("GET", "/api/archives?profile_id=%s&dir=router-backups" % profile_id)
        items = (archives or {}).get("archives") or []
        r.check("归档列表接口返回数据", len(items) >= 1, "%d 个" % len(items))
        if items:
            r.log("  %s (%s bytes)" % (items[0]["name"], items[0]["size"]))
        remote_path = items[0]["path"] if items else run_rec.get("remote_path")

        # ------------------------------------------------------------------
        r.section("8. 归档预览")
        st, preview = console.call("POST", "/api/preview",
                                   {"profile_id": profile_id, "remote_path": remote_path},
                                   timeout=120)
        ok = st == 200 and preview and preview.get("manifest")
        r.check("预览解析出清单", bool(ok), preview if not ok else "")
        if ok:
            mf = preview["manifest"]
            r.log("  tool=%s host=%s files=%s original=%s" % (
                mf.get("tool"), mf.get("hostname"), mf.get("file_count"), mf.get("total_bytes")))
            r.log("  roots=%s" % mf.get("roots"))
            r.check("清单记录了正确的源路径",
                    "/etc/config" in (mf.get("roots") or []), mf.get("roots"))
            r.check("预览列出了文件条目", len(preview.get("entries") or []) > 0,
                    "%d 项" % len(preview.get("entries") or []))

        # ------------------------------------------------------------------
        r.section("9. 试运行恢复（dry-run，不应写入任何文件）")
        dry_dest = "/tmp/wdb-restore-dry"
        run(c, "rm -rf %s && mkdir -p %s" % (dry_dest, dry_dest))
        st, dry = console.call("POST", "/api/restore", {
            "profile_id": profile_id,
            "remote_path": remote_path,
            "dest_dir": dry_dest,
            "overwrite": True,
            "dry_run": True,
            "strip_components": 0,
        })
        r.check("试运行任务被接受", st == 202, "HTTP %d %s" % (st, dry))

        dry_run = None
        for _ in range(60):
            time.sleep(1)
            st, listing = console.call("GET", "/api/runs?type=restore&limit=1")
            runs = (listing or {}).get("runs") or []
            if runs and runs[0].get("status") != "running":
                dry_run = runs[0]
                break
        r.check("试运行完成", bool(dry_run) and dry_run.get("status") == "success",
                dry_run.get("status") if dry_run else "timeout")

        out, _, _ = run(c, "find %s -type f | wc -l" % dry_dest)
        r.log("  试运行后目标目录文件数：%s" % out.strip())
        r.check("试运行没有写入任何文件", out.strip() == "0")

        # ------------------------------------------------------------------
        r.section("10. 正式恢复")
        dest = "/tmp/wdb-restore-real"
        run(c, "rm -rf %s && mkdir -p %s" % (dest, dest))
        st, real = console.call("POST", "/api/restore", {
            "profile_id": profile_id,
            "remote_path": remote_path,
            "dest_dir": dest,
            "overwrite": True,
            "dry_run": False,
            "strip_components": 0,
        })
        r.check("恢复任务被接受", st == 202, "HTTP %d %s" % (st, real))

        real_run = None
        for _ in range(60):
            time.sleep(1)
            st, listing = console.call("GET", "/api/runs?type=restore&limit=1")
            runs = (listing or {}).get("runs") or []
            if runs and runs[0].get("status") != "running":
                real_run = runs[0]
                break
        r.check("恢复成功", bool(real_run) and real_run.get("status") == "success",
                "status=%s msg=%s" % (real_run.get("status"), real_run.get("message")) if real_run else "timeout")

        if real_run:
            r.log("  恢复 %s 个文件 / %s 个目录，共 %s" % (
                real_run.get("file_count"), real_run.get("dir_count"), real_run.get("raw_size")))
            st, rlog = console.call("GET", "/api/runs/%s/log?offset=0" % real_run["id"])
            r.log("")
            r.log("  ---------- 恢复日志 ----------")
            for line in (rlog.get("text") or "").splitlines():
                r.log("  " + line)
            r.log("  ------------------------------")

        # ------------------------------------------------------------------
        r.section("11. 校验还原内容与原始文件一致")
        checks = [
            ("config/network", "/etc/config/network"),
            ("config/wireless", "/etc/config/wireless"),
            ("config/dhcp", "/etc/config/dhcp"),
        ]
        for name, orig in checks:
            out, _, _ = run(c,
                "R=$(find %s -path '*/etc/%s' -type f | head -1); "
                "if [ -z \"$R\" ]; then echo MISSING; else "
                "echo \"restored=$R\"; "
                "A=$(md5sum \"$R\" | awk '{print $1}'); "
                "B=$(md5sum %s 2>/dev/null | awk '{print $1}'); "
                "echo \"md5_restored=$A\"; echo \"md5_original=$B\"; "
                "[ \"$A\" = \"$B\" ] && echo MATCH || echo DIFFER; fi"
                % (dest, name, orig))
            r.log("  %s:\n%s" % (name, "\n".join("    " + l for l in out.strip().splitlines())))
            if "MISSING" in out:
                # 源文件在设备上可能本就不存在（x86 固件没有 /etc/config/wireless）。
                # 此时恢复结果必然也缺失，属预期；只有"原文件存在却没恢复出来"
                # 才算真失败，不能一概放过。
                _, _, orig_st = run(c, "test -f %s" % orig)
                if orig_st != 0:
                    r.log("  (skip) %s 在设备上不存在，跳过比对" % name)
                    continue
                r.check("%s 内容一致 (md5)" % name, False,
                        "原文件存在但恢复结果缺失：%s" % out.strip()[:200])
                continue
            r.check("%s 内容一致 (md5)" % name, "MATCH" in out, out.strip()[:200])

        out, _, _ = run(c, "find %s -type f | wc -l; du -sh %s" % (dest, dest))
        r.log("  恢复出的文件总数与体积：%s" % out.strip().replace("\n", " / "))

        # ------------------------------------------------------------------
        r.section("12. 保留策略与任务统计")
        st, jobs = console.call("GET", "/api/jobs")
        jl = (jobs or {}).get("jobs") or []
        r.check("任务列表可见", len(jl) == 1, jl[0].get("last_status") if jl else None)
        if jl:
            r.log("  last_status=%s last_run_at=%s" % (jl[0].get("last_status"), jl[0].get("last_run_at")))

        st, runs_all = console.call("GET", "/api/runs?limit=50")
        rl = (runs_all or {}).get("runs") or []
        r.log("  执行历史共 %d 条" % len(rl))
        r.check("执行历史包含备份与恢复", len(rl) >= 3, "%d 条" % len(rl))

        # ------------------------------------------------------------------
        r.section("13. 认证与安全边界")
        st, _ = console.call("POST", "/api/logout", {})
        r.check("登出成功", st == 200)
        st, body = console.call("GET", "/api/status")
        r.check("登出后无法访问受保护接口", st == 401, "HTTP %d" % st)

        fresh = Console(args.host, CONSOLE_PORT)
        st, body = fresh.call("GET", "/api/status")
        r.check("未认证的新客户端被拒绝", st == 401, "HTTP %d" % st)

        # CSRF guard: a state-changing request without the custom header.
        req = urllib.request.Request(
            fresh.base + "/api/login",
            data=json.dumps({"username": "admin", "password": initial_pw}).encode(),
            headers={"Content-Type": "application/json"}, method="POST")
        try:
            with fresh.opener.open(req, timeout=15) as resp:
                csrf_status = resp.status
        except urllib.error.HTTPError as e:
            csrf_status = e.code
        r.check("缺少自定义头的请求被 CSRF 防护拦截", csrf_status == 403, "HTTP %d" % csrf_status)

        st, body = fresh.call("POST", "/api/login",
                              {"username": "admin", "password": "wrong-password"})
        r.check("错误密码登录被拒绝", st == 401, "HTTP %d %s" % (st, body))
        st, body = fresh.call("POST", "/api/login",
                              {"username": "admin", "password": initial_pw})
        r.check("正确密码可重新登录", st == 200)

    except Exception as exc:  # noqa: BLE001 - report and continue to cleanup
        import traceback
        r.log("")
        r.log("!! 未捕获异常：" + traceback.format_exc())
        r.check("验证过程无异常", False, str(exc))
    finally:
        ok = finish(r, args, c)

    return 0 if ok else 1


def finish(r, args, c, ok=True):
    """Tear down test fixtures and write the report.

    提前返回的失败路径也会调用本函数，而 main 的出口还会再调一次 —— 必须幂等，
    否则第二次会对已关闭的连接发命令并污染报告。
    """
    if getattr(r, "_finished", False):
        return ok and not r.failures
    r._finished = True
    r.section("14. 清理")
    if c is not None:
        # 还原安装前的 config.json，把管理员密码换回操作者原本那一个。放在收尾
        # 而不是安装之后，是因为脚本自己的登录步骤依赖新装的初始密码。
        if not args.reset_admin:
            try:
                out, _, _ = run(c, "test -f %s && echo YES || echo NO" % SAVED_CONFIG)
                if "YES" in out:
                    run(c,
                        "cp %s /etc/qzrs-webdav-backup/config.json && "
                        "chmod 600 /etc/qzrs-webdav-backup/config.json && "
                        "/etc/init.d/qzrs-webdav-backup restart >/dev/null 2>&1 && "
                        "sleep 2 && echo restored" % SAVED_CONFIG)
                    r.log("已还原安装前的 config.json —— 管理员密码保持不变")
                else:
                    r.log("没有可还原的 config.json（安装前不存在）")
            except Exception as e:  # noqa: BLE001
                r.log("还原 config.json 时出错：%s" % e)
        else:
            r.log("--reset-admin 已指定，保留本次安装新生成的管理员密码")
        if not args.keep:
            try:
                run(c, "killall davd 2>/dev/null; rm -rf %s /tmp/davroot "
                       "/tmp/wdb-restore-dry /tmp/wdb-restore-real /tmp/davd.log" % DAV_ROOT)
                r.log("已清理测试 WebDAV 端点与临时目录")
            except Exception as e:  # noqa: BLE001
                r.log("清理时出错：%s" % e)
        else:
            r.log("--keep 已指定，保留 davd 与测试数据")
        try:
            c.close()
        except Exception:  # noqa: BLE001
            pass

    r.section("汇总")
    r.log("  检查项：%d，失败：%d" % (r.checks, len(r.failures)))
    if r.failures:
        for f in r.failures:
            r.log("    FAIL  " + f)
    else:
        r.log("  全部通过")

    size = r.dump(args.out)
    print("wrote %s (%d bytes), %d checks, %d failures"
          % (os.path.abspath(args.out), size, r.checks, len(r.failures)))
    return ok and not r.failures


if __name__ == "__main__":
    raise SystemExit(main())
