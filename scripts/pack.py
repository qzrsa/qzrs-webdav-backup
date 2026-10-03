#!/usr/bin/env python3
"""打包发布 tar.gz —— 显式写入可执行位。

背景（本机实测结论，勿删）：
在 Windows/MSYS 环境下，无论用 `chmod +x` 还是 `tar --mode=0755`，编译产物
进入 tar 头时 mode 仍为 0644。原因是 MSYS 无法把 Unix 权限位落到 NTFS 上，
tar 读到的 st_mode 就是 0644。后果：路由器上 `tar -xzf` 解包后二进制没有执行
位，install.sh 执行 `./qzrs-webdav-backup -version` 报 Permission denied，并被误
判为"架构不匹配"。

绕开办法：tar 头的 mode 字段是纯整数，与文件系统无关。用 tarfile 构造
TarInfo 并显式赋值 mode，产出的包在任何解包工具下都带正确权限。

用法：
    python pack.py --stage <目录> --out <输出.tar.gz> [--bin-name qzrs-webdav-backup]
"""

import argparse
import os
import sys
import tarfile

# 需要 0755 的成员；其余一律 0644。
EXEC_SUFFIXES = (".sh", ".init")


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--stage", required=True, help="待打包的暂存目录")
    ap.add_argument("--out", required=True, help="输出的 .tar.gz 路径")
    ap.add_argument("--bin-name", default="qzrs-webdav-backup",
                    help="主二进制文件名（会被赋予执行位）")
    args = ap.parse_args()

    if not os.path.isdir(args.stage):
        print("[x] 暂存目录不存在：%s" % args.stage, file=sys.stderr)
        return 1

    names = sorted(os.listdir(args.stage))
    if not names:
        print("[x] 暂存目录为空：%s" % args.stage, file=sys.stderr)
        return 1

    out_dir = os.path.dirname(os.path.abspath(args.out))
    if out_dir:
        os.makedirs(out_dir, exist_ok=True)

    packed = []
    with tarfile.open(args.out, "w:gz") as tf:
        for name in names:
            src = os.path.join(args.stage, name)
            if not os.path.isfile(src):
                continue
            executable = (
                name == args.bin_name
                or name.endswith(EXEC_SUFFIXES)
            )
            ti = tf.gettarinfo(src, arcname=name)
            ti.mode = 0o755 if executable else 0o644
            # 归档内统一为 root:root，与 OpenWrt 上以 root 安装一致。
            ti.uid = 0
            ti.gid = 0
            ti.uname = "root"
            ti.gname = "root"
            with open(src, "rb") as fh:
                tf.addfile(ti, fh)
            packed.append((name, ti.mode))

    for name, mode in packed:
        print("      %-28s %s" % (name, oct(mode)))
    print(args.out)
    return 0


if __name__ == "__main__":
    sys.exit(main())
