#!/usr/bin/env python3
"""在设备上执行任意命令，用于日常调试。

凭据只从环境变量读取，绝不写进仓库：
    ROUTER_HOST / ROUTER_USER / ROUTER_PASSWORD

用法：
    ROUTER_PASSWORD=xxx python scripts/dev_run.py "/etc/init.d/qzrs-webdav-backup restart"
    ROUTER_PASSWORD=xxx python scripts/dev_run.py --json "cat /etc/config/qzrs-webdav-backup"
    ROUTER_PASSWORD=xxx python scripts/dev_run.py --file cmds.txt

说明：本机 PowerShell/前台 stdout 可能不回显，所以默认把结果同时写入
scripts/console_dev.txt，便于用 Read 回读。
"""
from __future__ import annotations

import argparse
import os
import sys
import time

import paramiko

HERE = os.path.dirname(os.path.abspath(__file__))

DEFAULT_HOST = os.environ.get("ROUTER_HOST", "192.168.11.1")
DEFAULT_USER = os.environ.get("ROUTER_USER", "root")
DEFAULT_PASSWORD = os.environ.get("ROUTER_PASSWORD", "")


def run(c, cmd, timeout=180):
    """执行单条命令，返回 (stdout, stderr, exit_status)。"""
    _, out, err = c.exec_command(cmd, timeout=timeout)
    o = out.read().decode("utf-8", "replace")
    e = err.read().decode("utf-8", "replace")
    st = out.channel.recv_exit_status()
    return o, e, st


def main() -> int:
    ap = argparse.ArgumentParser()
    ap.add_argument("command", nargs="?", help="要执行的命令")
    ap.add_argument("--file", help="从文件逐行读取命令（忽略空行与 # 开头）")
    ap.add_argument("--host", default=DEFAULT_HOST)
    ap.add_argument("--user", default=DEFAULT_USER)
    ap.add_argument("--password", default=DEFAULT_PASSWORD)
    ap.add_argument("--port", type=int, default=22)
    ap.add_argument("--timeout", type=int, default=180)
    ap.add_argument("--sleep", type=float, default=0.0,
                    help="每条命令之间的间隔秒数")
    ap.add_argument("--out", default=os.path.join(HERE, "console_dev.txt"))
    args = ap.parse_args()

    if not args.password:
        print("缺少设备密码：请设置环境变量 ROUTER_PASSWORD，或传 --password。",
              file=sys.stderr)
        return 2

    cmds = []
    if args.file:
        with open(args.file, "r", encoding="utf-8") as fh:
            for line in fh:
                line = line.rstrip("\n")
                if not line.strip() or line.lstrip().startswith("#"):
                    continue
                cmds.append(line)
    if args.command:
        cmds.append(args.command)
    if not cmds:
        print("没有要执行的命令。", file=sys.stderr)
        return 2

    lines = []
    c = paramiko.SSHClient()
    c.set_missing_host_key_policy(paramiko.AutoAddPolicy())
    c.connect(args.host, port=args.port, username=args.user,
              password=args.password, timeout=15, banner_timeout=15,
              auth_timeout=15, look_for_keys=False, allow_agent=False)
    try:
        for i, cmd in enumerate(cmds):
            lines.append("$ %s" % cmd)
            o, e, st = run(c, cmd, timeout=args.timeout)
            if o.strip():
                lines.append(o.rstrip("\n"))
            if e.strip():
                lines.append("[stderr] " + e.rstrip("\n"))
            lines.append("[exit] %d" % st)
            lines.append("")
            if args.sleep and i != len(cmds) - 1:
                time.sleep(args.sleep)
    finally:
        c.close()

    text = "\n".join(lines)
    print(text)
    try:
        with open(args.out, "w", encoding="utf-8", newline="\n") as fh:
            fh.write(text + "\n")
    except OSError as exc:  # 落盘失败不应影响主要结果
        print("(写结果文件失败：%s)" % exc, file=sys.stderr)
    return 0


if __name__ == "__main__":
    sys.exit(main())
