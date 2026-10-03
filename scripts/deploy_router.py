#!/usr/bin/env python
# -*- coding: utf-8 -*-
"""把 qzrs-webdav-backup 发布包部署到软路由并验证。

用法：
    python deploy_router.py [--target linux-amd64] [--version 1.0.2]
                            [--host 192.168.11.1] [--password xxx]
                            [--skip-build] [--verify-only]

流程：
    1. 上传 dist/qzrs-webdav-backup-<ver>-<target>.tar.gz 到路由器 /tmp
    2. 解压并通过 install.sh 安装
    3. 检测监听端口、拉取服务日志
    4. 用 HTTP 请求验证控制台可访问

输出写入 out.txt，调用方用 Read 工具读回（本机前台可能不回显 stdout）。
"""
import argparse
import os
import sys
import time

import paramiko

# 设备凭据只从环境变量读取，绝不写进仓库。
DEFAULT_HOST = os.environ.get("ROUTER_HOST", "192.168.11.1")
DEFAULT_USER = os.environ.get("ROUTER_USER", "root")
DEFAULT_PASSWORD = os.environ.get("ROUTER_PASSWORD", "")
DEFAULT_TARGET = "linux-amd64"
DEFAULT_VERSION = "1.0.2"

HERE = os.path.dirname(os.path.abspath(__file__))
PROJECT = os.path.dirname(HERE)


def connect(host, user, password):
    c = paramiko.SSHClient()
    c.set_missing_host_key_policy(paramiko.AutoAddPolicy())
    c.connect(host, 22, user, password=password,
              timeout=10, allow_agent=False, look_for_keys=False)
    return c


def run(c, cmd, timeout=180):
    """Run a command, returning (stdout, stderr, exit_status)."""
    stdin, stdout, stderr = c.exec_command(cmd, timeout=timeout, get_pty=False)
    out = stdout.read().decode("utf-8", "replace")
    err = stderr.read().decode("utf-8", "replace")
    status = stdout.channel.recv_exit_status()
    return out, err, status


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--host", default=DEFAULT_HOST)
    ap.add_argument("--user", default=DEFAULT_USER)
    ap.add_argument("--password", default=DEFAULT_PASSWORD)
    ap.add_argument("--target", default=DEFAULT_TARGET)
    ap.add_argument("--version", default=DEFAULT_VERSION)
    ap.add_argument("--out", default=os.path.join(HERE, "out.txt"))
    ap.add_argument("--skip-build", action="store_true")
    ap.add_argument("--verify-only", action="store_true",
                    help="只做验证，不重新上传安装")
    ap.add_argument("--remote-dir", default="/tmp/wdb-install")
    args = ap.parse_args()

    if not args.password:
        print("缺少设备密码：请设置环境变量 ROUTER_PASSWORD，或传 --password。",
              file=sys.stderr)
        return 2

    pkg_name = "qzrs-webdav-backup-%s-%s.tar.gz" % (args.version, args.target)
    pkg_path = os.path.join(PROJECT, "dist", pkg_name)

    lines = []

    def log(msg=""):
        lines.append(msg)

    if not args.verify_only:
        if not os.path.isfile(pkg_path):
            print("missing package: %s" % pkg_path, file=sys.stderr)
            return 2
        size = os.path.getsize(pkg_path)
        log("local package: %s (%.2f MB)" % (pkg_name, size / 1024 / 1024))

    log("connecting to %s ..." % args.host)
    c = connect(args.host, args.user, args.password)
    log("connected")

    try:
        if not args.verify_only:
            # --- upload -------------------------------------------------
            run(c, "rm -rf %s && mkdir -p %s" % (args.remote_dir, args.remote_dir), timeout=60)
            sftp = c.open_sftp()
            remote_pkg = "/tmp/%s" % pkg_name
            log("uploading to %s ..." % remote_pkg)
            t0 = time.time()
            sftp.put(pkg_path, remote_pkg)
            elapsed = time.time() - t0
            log("upload done in %.1fs" % elapsed)
            remote_size = sftp.stat(remote_pkg).st_size
            log("remote size: %d bytes (match=%s)" % (remote_size, remote_size == size))
            sftp.close()

            if remote_size != size:
                log("!! size mismatch, aborting")
                raise SystemExit(3)

            # --- extract + install --------------------------------------
            out, err, st = run(
                c,
                "cd %s && tar -xzf %s && ls -la" % (args.remote_dir, remote_pkg),
                timeout=120)
            log("")
            log("=" * 70)
            log("$ extract")
            log("-" * 70)
            log(out.strip())
            if err.strip():
                log("[stderr] " + err.strip())

            out, err, st = run(
                c,
                "cd %s && chmod +x install.sh qzrs-webdav-backup && ./install.sh 2>&1"
                % args.remote_dir,
                timeout=300)
            log("")
            log("=" * 70)
            log("$ ./install.sh   (exit=%d)" % st)
            log("-" * 70)
            log(out.strip())
            if err.strip():
                log("[stderr] " + err.strip())

        # --- verify -----------------------------------------------------
        checks = [
            ("进程", "ps w | grep '[w]ebdav-backup' | head -5"),
            ("服务状态", "/etc/init.d/qzrs-webdav-backup status 2>&1"),
            ("监听端口", "netstat -lnp 2>/dev/null | grep -E '8787|webdav' || ss -lntp 2>/dev/null | grep 8787"),
            ("二进制", "ls -la /usr/bin/qzrs-webdav-backup && /usr/bin/qzrs-webdav-backup -version"),
            ("数据目录", "ls -la /etc/qzrs-webdav-backup/ 2>&1"),
            ("服务日志", "tail -40 /etc/qzrs-webdav-backup/service.log 2>&1"),
            ("系统日志", "logread 2>/dev/null | grep -i webdav | tail -20"),
            ("磁盘", "df -h / /etc /opt 2>/dev/null | head -8"),
            ("本地 HTTP", "wget -q -O - -T 5 http://127.0.0.1:8787/api/health 2>&1 || curl -s -m 5 http://127.0.0.1:8787/api/health 2>&1"),
            ("首页", "wget -q -O - -T 5 http://127.0.0.1:8787/ 2>&1 | head -c 400"),
            ("静态资源", "wget -q -O - -T 5 http://127.0.0.1:8787/app.js 2>&1 | wc -c"),
            ("样式表", "wget -q -O - -T 5 http://127.0.0.1:8787/style.css 2>&1 | wc -c"),
        ]

        for title, cmd in checks:
            out, err, st = run(c, cmd, timeout=45)
            log("")
            log("=" * 70)
            log("[%s] $ %s   (exit=%d)" % (title, cmd, st))
            log("-" * 70)
            log(out.strip()[:4000])
            if err.strip():
                log("[stderr] " + err.strip()[:1200])

        # --- end-to-end backup through the real HTTP API ----------------
        log("")
        log("=" * 70)
        log("[端到端] 通过 HTTP API 登录并读取状态")
        log("-" * 70)
        script = r'''
PW=$(sed -n 's/.*密 *码: *//p' /etc/qzrs-webdav-backup/service.log 2>/dev/null | head -1 | tr -d '\r')
if [ -z "$PW" ]; then echo "NO_PASSWORD_IN_LOG"; exit 0; fi
echo "password length: ${#PW}"

# Login, keeping the session cookie.
wget -q -O /tmp/login.json --header='Content-Type: application/json' \
     --header='X-Requested-With: qzrs-webdav-backup' \
     --post-data="{\"username\":\"admin\",\"password\":\"$PW\"}" \
     --save-cookies=/tmp/wdb.cookies --keep-session-cookies \
     http://127.0.0.1:8787/api/login 2>&1
echo "--- login response ---"
cat /tmp/login.json 2>/dev/null; echo

echo "--- /api/me ---"
wget -q -O - --load-cookies=/tmp/wdb.cookies http://127.0.0.1:8787/api/me 2>&1; echo

echo "--- /api/status ---"
wget -q -O - --load-cookies=/tmp/wdb.cookies http://127.0.0.1:8787/api/status 2>&1 | head -c 1200; echo

echo "--- /api/profiles ---"
wget -q -O - --load-cookies=/tmp/wdb.cookies http://127.0.0.1:8787/api/profiles 2>&1; echo

echo "--- /api/fs/list?path=/etc ---"
wget -q -O - --load-cookies=/tmp/wdb.cookies 'http://127.0.0.1:8787/api/fs/list?path=/etc' 2>&1 | head -c 500; echo

rm -f /tmp/login.json /tmp/wdb.cookies
'''
        out, err, st = run(c, script, timeout=120)
        log(out.strip())
        if err.strip():
            log("[stderr] " + err.strip()[:1200])

    finally:
        c.close()

    with open(args.out, "w", encoding="utf-8") as fh:
        fh.write("\n".join(lines))
    print("wrote %s (%d bytes)" % (os.path.abspath(args.out), os.path.getsize(args.out)))
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
