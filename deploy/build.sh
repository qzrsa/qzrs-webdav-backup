#!/bin/sh
#
# 构建脚本 — 在开发机上交叉编译各平台版本并打包。
#
# 用法：
#   sh build.sh                    # 构建全部平台
#   sh build.sh linux/amd64        # 只构建指定平台
#
# 产物位于 dist/ 目录。

set -e

# Resolve the project root so relative paths (deploy/, web/, dist/) work
# no matter where the script is invoked from.
SCRIPT_DIR=$(cd "$(dirname "$0")" && pwd)
cd "$SCRIPT_DIR/.."

VERSION=${VERSION:-1.1.3}
OUT_DIR="dist"
LDFLAGS="-s -w -X main.Version=$VERSION"

# 目标平台：OpenWrt 常见的架构都覆盖到。
#   linux/amd64  x86-64 软路由、N100、J4125、大多数 NAS
#   linux/arm64  aarch64（树莓派、R2S/R4S、ARM64 NAS）
#   linux/arm    armv7（部分老设备）
#   linux/mipsle 小端 MIPS（MT7621 等，softfloat）
#   linux/mips   大端 MIPS（softfloat）
TARGETS="linux/amd64 linux/arm64 linux/arm linux/mipsle linux/mips"

# 让 mips 版本使用软件浮点，这是 OpenWrt 路由器上的标准 ABI。
MIPSFLAGS="GOMIPS=softfloat"
GOARM=7

# 确保 go.sh 存在
if [ ! -f "D:/WorkBuddy/.tools/go.sh" ]; then
    echo "[x] 找不到 Go 构建封装脚本 D:/WorkBuddy/.tools/go.sh" >&2
    exit 1
fi

GO="sh D:/WorkBuddy/.tools/go.sh"

echo ""
echo "构建 qzrs-webdav-backup v$VERSION"
echo "-------------------------------------------"

if [ -n "$1" ]; then
    TARGETS="$1"
fi

mkdir -p "$OUT_DIR"

# 测试先行：编译产物不应该在测试失败的情况下发布。
echo "[1/5] 运行测试…"
$GO test ./... >/dev/null || { echo "[x] 测试失败，已中止构建" >&2; exit 1; }
echo "      测试通过"

echo "[2/5] 静态检查…"
$GO vet ./... || { echo "[x] go vet 报告问题，已中止构建" >&2; exit 1; }
echo "      未发现问题"

# 前端自检：Go 侧测试完全不渲染页面，前端语法/资源引用出错时它们照样全绿，
# 而用户打开控制台只能看到静止的「正在载入控制台…」。
echo "[3/5] 前端自检…"
NODE_BIN=$(command -v node 2>/dev/null || true)
if [ -z "$NODE_BIN" ] && [ -x "D:/Program Files/nodejs/node.exe" ]; then
    NODE_BIN="D:/Program Files/nodejs/node.exe"
fi
if [ -n "$NODE_BIN" ] && [ -f scripts/check_frontend.js ]; then
    "$NODE_BIN" scripts/check_frontend.js || { echo "[x] 前端自检失败，已中止构建" >&2; exit 1; }
else
    echo "      [!] 未找到 node，跳过前端自检"
fi

echo "[4/5] 交叉编译…"
for target in $TARGETS; do
    os=${target%/*}
    arch=${target#*/}

    name="qzrs-webdav-backup-$VERSION-$os-$arch"
    case "$arch" in
        arm) name="$name-v7" ;;
    esac

    echo "      $os/$arch → $OUT_DIR/$name"

    env GOOS="$os" GOARCH="$arch" GOARM="$GOARM" $MIPSFLAGS \
        $GO build -trimpath -ldflags "$LDFLAGS" -o "$OUT_DIR/$name" . \
        || { echo "[x] 构建 $target 失败" >&2; exit 1; }
done

echo "[5/5] 打包发布文件…"
for target in $TARGETS; do
    os=${target%/*}
    arch=${target#*/}

    bin="qzrs-webdav-backup-$VERSION-$os-$arch"
    case "$arch" in
        arm) bin="$bin-v7" ;;
    esac

    pkg="$OUT_DIR/qzrs-webdav-backup-$VERSION-$os-$arch"
    case "$arch" in
        arm) pkg="$pkg-v7" ;;
    esac

    stage="$OUT_DIR/.stage-$os-$arch"
    rm -rf "$stage"
    mkdir -p "$stage"

    cp "$OUT_DIR/$bin" "$stage/qzrs-webdav-backup"
    cp deploy/qzrs-webdav-backup.init "$stage/qzrs-webdav-backup.init"
    cp deploy/install.sh "$stage/install.sh"
    cp deploy/uninstall.sh "$stage/uninstall.sh"
    cp README.md "$stage/README.md" 2>/dev/null || true

    # 必须用 scripts/pack.py 打包：Windows/MSYS 下 chmod 与 tar --mode 都无法
    # 把执行位写进 tar 头（tar 里 qzrs-webdav-backup 仍是 0644），会导致路由器上解包
    # 后二进制不可执行。详见 scripts/pack.py 顶部注释。
    PY=$(command -v python3 || command -v python || true)
    if [ -n "$PY" ]; then
        "$PY" "$SCRIPT_DIR/../scripts/pack.py" \
            --stage "$stage" \
            --out "$OUT_DIR/$(basename "$pkg").tar.gz" \
            --bin-name qzrs-webdav-backup
    else
        echo "  [!] 未找到 python，回退到 tar 打包；二进制执行位可能丢失" >&2
        chmod 0755 "$stage/qzrs-webdav-backup" "$stage/install.sh" "$stage/uninstall.sh"
        ( cd "$stage" && tar -czf "../$(basename "$pkg").tar.gz" . )
    fi
    rm -rf "$stage"

    echo "      $OUT_DIR/$(basename "$pkg").tar.gz"
done

# 单独发布纯二进制（配合 opkg 或手动部署时使用）
echo ""
echo "构建完成，产物清单："
echo "-------------------------------------------"
ls -lh "$OUT_DIR"/qzrs-webdav-backup-*.tar.gz 2>/dev/null | awk '{printf "  %-8s %s\n", $5, $9}'
echo ""
echo "在路由器上部署（以 x86-64 为例）："
echo "  scp $OUT_DIR/qzrs-webdav-backup-$VERSION-linux-amd64.tar.gz root@192.168.11.1:/tmp/"
echo "  ssh root@192.168.11.1"
echo "  cd /tmp && tar -xzf qzrs-webdav-backup-$VERSION-linux-amd64.tar.gz -C wdb && cd wdb"
echo "  ./install.sh"
echo ""
