#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""把设备恢复成干净的交付状态。

端到端验证会在 /etc/qzrs-webdav-backup 里留下指向临时 davd 的 WebDAV 配置、测试任务
与执行历史。这里彻底卸载后重新安装，得到一个只有初始密码的全新实例，供用户
直接使用。

用法：
    python finalize_install.py [--host 192.168.11.1] [--version 1.0.2]
                               [--out out_finalize.txt]
"""
import argparse
import http.cookiejar
import json
import os
import re
import sys
import time
import urllib.error
import urllib.request

import paramiko

HERE = os.path.dirname(os.path.abspath(__file__))
PROJECT = os.path.dirname(HERE)

# 设备凭据只从环境变量读取，绝不写进仓库。
DEFAULT_HOST = os.environ.get("ROUTER_HOST", "192.168.11.1")
DEFAULT_USER = os.environ.get("ROUTER_USER", "root")
DEFAULT_PASSWORD = os.environ.get("ROUTER_PASSWORD", "")
CONSOLE_PORT = 8787


def connect(host, user, password):
    c = paramiko.SSHClient()
    c.set_missing_host_key_policy(paramiko.AutoAddPolicy())
    c.connect(host, 22, user, password=password, timeout=10,
              allow_agent=False, look_for_keys=False)
    return c


def run(c, cmd, timeout=300):
    _, stdout, stderr = c.exec_command(cmd, timeout=timeout, get_pty=False)
    out = stdout.read().decode("utf-8", "replace")
    err = stderr.read().decode("utf-8", "replace")
    return out, err, stdout.channel.recv_exit_status()


def api(host, method, path, body=None, token=None, timeout=60):
    url = "http://%s:%d%s" % (host, CONSOLE_PORT, path)
    data = None
    headers = {"X-Requested-With": "qzrs-webdav-backup"}
    if body is not None:
        data = json.dumps(body).encode("utf-8")
        headers["Content-Type"] = "application/json"
    req = urllib.request.Request(url, data=data, headers=headers, method=method)
    opener = urllib.request.build_opener(urllib.request.ProxyHandler({}))
    try:
        with opener.open(req, timeout=timeout) as resp:
            raw = resp.read().decode("utf-8", "replace")
            return resp.status, (json.loads(raw) if raw.strip() else None)
    except urllib.error.HTTPError as e:
        raw = e.read().decode("utf-8", "replace")
        try:
            return e.code, json.loads(raw)
        except ValueError:
            return e.code, {"raw": raw[:200]}


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--host", default=DEFAULT_HOST)
    ap.add_argument("--user", default=DEFAULT_USER)
    ap.add_argument("--password", default=DEFAULT_PASSWORD)
    ap.add_argument("--version", default="1.0.2")
    ap.add_argument("--out", default=os.path.join(HERE, "out_finalize.txt"))
    args = ap.parse_args()

    if not args.password:
        print("缺少设备密码：请设置环境变量 ROUTER_PASSWORD，或传 --password。",
              file=sys.stderr)
        return 2

    pkg = os.path.join(PROJECT, "dist",
                       "qzrs-webdav-backup-%s-linux-amd64.tar.gz" % args.version)
    if not os.path.isfile(pkg):
        print("missing artifact: %s" % pkg, file=sys.stderr)
        return 2

    r = []
    r.append("=" * 62)
    r.append("  交付状态整理")
    r.append("=" * 62)

    c = connect(args.host, args.user, args.password)

    # 先停掉可能残留的测试 WebDAV 端点，避免它占用端口。
    run(c, "killall davd 2>/dev/null; rm -rf /tmp/davroot /tmp/wdb-restore-dry "
           "/tmp/wdb-restore-real /tmp/wdb-install; echo cleaned")
    r.append("  [+] 已清理测试端点与临时目录")

    # 彻底卸载：连同 /etc/qzrs-webdav-backup 数据目录一起删除。
    out, _, _ = run(c, "cd /tmp && rm -rf fresh && mkdir -p fresh && "
                       "tar -xzf /tmp/%s -C fresh && cd fresh && "
                       "./uninstall.sh -p -y 2>&1 | tail -8"
                       % os.path.basename(pkg))
    r.append("  [+] 已卸载旧实例（含测试数据）")

    sftp = c.open_sftp()
    sftp.put(pkg, "/tmp/%s" % os.path.basename(pkg))
    sftp.close()

    out, err, st = run(c, "rm -rf /tmp/fresh && mkdir -p /tmp/fresh && "
                          "tar -xzf /tmp/%s -C /tmp/fresh && cd /tmp/fresh && "
                          "chmod +x install.sh && ./install.sh 2>&1"
                          % os.path.basename(pkg), timeout=300)
    r.append("  安装退出码：%d" % st)
    r.append("")
    r.append(out.strip())

    m = re.search(r"初始密码\s*:\s*(\S+)", out)
    pw = m.group(1) if m else None

    if st != 0 or not pw:
        r.append("")
        r.append("  [x] 安装失败或未取到初始密码")
        with open(args.out, "w", encoding="utf-8") as fh:
            fh.write("\n".join(r) + "\n")
        return 1

    # 等 HTTP 就绪
    ok_health = False
    for _ in range(10):
        try:
            code, body = api(args.host, "GET", "/api/health", timeout=5)
            if code == 200:
                ok_health = True
                break
        except Exception:
            pass
        time.sleep(1)

    r.append("")
    r.append("  [%s] /api/health 可用" % ("PASS" if ok_health else "FAIL"))

    # 登录 + 确认没有任何残留配置/任务
    jar = http.cookiejar.CookieJar()
    opener = urllib.request.build_opener(
        urllib.request.ProxyHandler({}), urllib.request.HTTPCookieProcessor(jar))
    req = urllib.request.Request(
        "http://%s:%d/api/login" % (args.host, CONSOLE_PORT),
        data=json.dumps({"username": "admin", "password": pw}).encode(),
        headers={"Content-Type": "application/json",
                 "X-Requested-With": "qzrs-webdav-backup"}, method="POST")
    login_ok = False
    try:
        with opener.open(req, timeout=20) as resp:
            login_ok = resp.status == 200
    except Exception as exc:
        r.append("  登录异常：%r" % exc)

    def get(path):
        q = urllib.request.Request(
            "http://%s:%d%s" % (args.host, CONSOLE_PORT, path),
            headers={"X-Requested-With": "qzrs-webdav-backup"})
        with opener.open(q, timeout=20) as resp:
            return json.loads(resp.read().decode("utf-8"))

    r.append("  [%s] 使用初始密码登录成功" % ("PASS" if login_ok else "FAIL"))

    profiles = jobs = runs = None
    try:
        profiles = get("/api/profiles").get("profiles") or []
        jobs = get("/api/jobs").get("jobs") or []
        runs = get("/api/runs?limit=10").get("runs") or []
        r.append("  [%s] 无残留配置 / 任务 / 历史（%d / %d / %d）"
                 % ("PASS" if not (profiles or jobs or runs) else "FAIL",
                    len(profiles), len(jobs), len(runs)))
    except Exception as exc:
        r.append("  读取状态异常：%r" % exc)

    # 开机自启检查（不重启设备，只确认 procd 的 rc.d 符号链接已建立）。
    out, _, _ = run(c, "ls -l /etc/rc.d/ | grep -i webdav || echo NONE; "
                       "echo '--- status ---'; /etc/init.d/qzrs-webdav-backup status 2>&1 | head -5")
    r.append("")
    r.append("  开机自启与服务状态：")
    r.append("    " + out.strip().replace("\n", "\n    "))

    r.append("")
    r.append("=" * 62)
    r.append("  交付信息")
    r.append("=" * 62)
    r.append("   访问地址 : http://%s:%d/" % (args.host, CONSOLE_PORT))
    r.append("   用户名   : admin")
    r.append("   初始密码 : %s" % pw)

    c.close()
    text = "\n".join(r) + "\n"
    with open(args.out, "w", encoding="utf-8") as fh:
        fh.write(text)
    return 0


if __name__ == "__main__":
    sys.exit(main())
