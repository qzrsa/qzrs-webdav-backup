#!/bin/sh
#
# qzrs-webdav-backup — 卸载脚本
#
# 默认只移除程序与服务，保留配置与备份数据（安全默认）。
# 使用 -p 可连同数据目录一起删除。
#
# 用法：
#   ./uninstall.sh          仅卸载程序，保留 /etc/qzrs-webdav-backup
#   ./uninstall.sh -p       连同数据目录一并删除（不可恢复）
#   ./uninstall.sh -y       跳过确认

set -e

INIT_NAME="qzrs-webdav-backup"
BIN_DST="/usr/bin/$INIT_NAME"
INIT_DST="/etc/init.d/$INIT_NAME"
UCI_CFG="/etc/config/$INIT_NAME"

DATA_DIR=""
PURGE=0
ASSUME_YES=0

usage() {
    sed -n '2,14p' "$0" | sed 's/^# \{0,1\}//'
    exit 0
}

while [ $# -gt 0 ]; do
    case "$1" in
        -p|--purge) PURGE=1; shift ;;
        -y|--yes) ASSUME_YES=1; shift ;;
        -h|--help) usage ;;
        *) echo "未知参数：$1" >&2; usage ;;
    esac
done

[ "$(id -u)" = "0" ] || { echo "请以 root 身份运行" >&2; exit 1; }

DATA_DIR=$(uci -q get "$INIT_NAME.main.data_dir" 2>/dev/null || echo "")
[ -n "$DATA_DIR" ] || DATA_DIR="/etc/qzrs-webdav-backup"

echo ""
echo "==============================================="
echo " qzrs-webdav-backup 卸载程序"
echo "==============================================="
echo ""
echo "  将执行以下操作："
echo "    - 停止并禁用服务"
echo "    - 删除 $BIN_DST"
echo "    - 删除 $INIT_DST"
echo "    - 删除 $UCI_CFG"
if [ "$PURGE" -eq 1 ]; then
    echo "    - 删除数据目录 $DATA_DIR  （包含所有配置与执行历史）"
else
    echo "    - 保留数据目录 $DATA_DIR"
fi
echo ""

if [ "$ASSUME_YES" -ne 1 ]; then
    printf "确认继续？[y/N] "
    read -r answer
    case "$answer" in
        y|Y|yes|YES) ;;
        *) echo "已取消"; exit 0 ;;
    esac
fi

echo ""

# 停止服务
if [ -x "$INIT_DST" ]; then
    "$INIT_DST" stop >/dev/null 2>&1 || true
fi
if pgrep -f "$BIN_DST" >/dev/null 2>&1; then
    killall "$INIT_NAME" >/dev/null 2>&1 || true
    sleep 1
fi
[ -x "$INIT_DST" ] && "$INIT_DST" disable >/dev/null 2>&1 || true
echo "  [+] 服务已停止并禁用"

# 删除文件
rm -f "$BIN_DST" && echo "  [+] 已删除 $BIN_DST"
rm -f "$INIT_DST" && echo "  [+] 已删除 $INIT_DST"

if [ -f "$UCI_CFG" ]; then
    rm -f "$UCI_CFG"
    uci -q commit "$INIT_NAME" 2>/dev/null || true
    echo "  [+] 已删除 $UCI_CFG"
fi

# 清理遗留的备份文件
rm -f "$BIN_DST.bak" 2>/dev/null || true

# 清理更名前（webdav-backup）的残留，避免旧服务脚本继续挂在开机自启里。
LEGACY_INIT="/etc/init.d/webdav-backup"
LEGACY_BIN="/usr/bin/webdav-backup"
LEGACY_UCI="/etc/config/webdav-backup"
LEGACY_DATA="/etc/webdav-backup"
if [ -x "$LEGACY_INIT" ]; then
    "$LEGACY_INIT" stop >/dev/null 2>&1 || true
    "$LEGACY_INIT" disable >/dev/null 2>&1 || true
    rm -f "$LEGACY_INIT" "$LEGACY_BIN" "$LEGACY_UCI"
    echo "  [+] 已清理旧版残留（webdav-backup 程序与服务）"
fi
if [ "$PURGE" -eq 1 ] && [ -d "$LEGACY_DATA" ] && [ ! -d "$DATA_DIR" ]; then
    rm -rf "$LEGACY_DATA"
    echo "  [+] 已删除旧版数据目录 $LEGACY_DATA"
fi

if [ "$PURGE" -eq 1 ]; then
    if [ -d "$DATA_DIR" ]; then
        rm -rf "$DATA_DIR"
        echo "  [+] 已删除数据目录 $DATA_DIR"
    fi
    echo ""
    echo " 卸载完成，所有数据已清除。"
else
    echo ""
    echo " 卸载完成。"
    echo ""
    echo " 数据目录仍然保留：$DATA_DIR"
    echo "   配置：$DATA_DIR/config.json"
    echo "   历史：$DATA_DIR/state/"
    echo ""
    echo " 如需彻底删除，请执行：rm -rf $DATA_DIR"
fi
echo ""
