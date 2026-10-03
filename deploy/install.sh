#!/bin/sh
#
# qzrs-webdav-backup — OpenWrt 安装脚本
#
# 用法：
#   把本脚本与对应架构的 qzrs-webdav-backup 二进制放在同一目录，然后在路由器上执行：
#       chmod +x install.sh qzrs-webdav-backup
#       ./install.sh
#
# 可选参数：
#       -d <目录>   数据目录（默认 /etc/qzrs-webdav-backup）
#       -p <端口>   监听端口（默认 8787）
#       -b <地址>   监听地址（默认 0.0.0.0）
#       -n          仅安装文件，不启动服务
#       -f          覆盖已存在的二进制与 init 脚本

set -e

BIN_NAME="qzrs-webdav-backup"
INIT_NAME="qzrs-webdav-backup"

BIN_DST="/usr/bin/$BIN_NAME"
INIT_DST="/etc/init.d/$INIT_NAME"
UCI_CFG="/etc/config/$INIT_NAME"

DATA_DIR="/etc/qzrs-webdav-backup"
LISTEN_HOST="0.0.0.0"
LISTEN_PORT="8787"
AUTO_START=1
FORCE=0

# 旧版（更名为 qzrs-webdav-backup 之前）的程序名与路径，安装时自动迁移。
OLD_NAME="webdav-backup"
OLD_BIN="/usr/bin/$OLD_NAME"
OLD_INIT="/etc/init.d/$OLD_NAME"
OLD_UCI="/etc/config/$OLD_NAME"
OLD_DATA="/etc/webdav-backup"

usage() {
    sed -n '2,18p' "$0" | sed 's/^# \{0,1\}//'
    exit 0
}

while [ $# -gt 0 ]; do
    case "$1" in
        -d) DATA_DIR="$2"; shift 2 ;;
        -p) LISTEN_PORT="$2"; shift 2 ;;
        -b) LISTEN_HOST="$2"; shift 2 ;;
        -n) AUTO_START=0; shift ;;
        -f) FORCE=1; shift ;;
        -h|--help) usage ;;
        *) echo "未知参数：$1" >&2; usage ;;
    esac
done

info()  { echo "  [*] $*"; }
ok()    { echo "  [+] $*"; }
warn()  { echo "  [!] $*" >&2; }
die()   { echo "  [x] $*" >&2; exit 1; }

SCRIPT_DIR=$(cd "$(dirname "$0")" && pwd)
BIN_SRC="$SCRIPT_DIR/$BIN_NAME"
INIT_SRC="$SCRIPT_DIR/$INIT_NAME.init"

echo ""
echo "==============================================="
echo " qzrs-webdav-backup 安装程序"
echo "==============================================="
echo ""

# ---------------------------------------------------------------- 前置检查

[ "$(id -u)" = "0" ] || die "请以 root 身份运行（OpenWrt 上直接使用 root）"

[ -f "$BIN_SRC" ] || die "找不到二进制文件：$BIN_SRC
请把 $BIN_NAME 与本脚本放在同一目录。"

[ -f "$INIT_SRC" ] || die "找不到 init 脚本：$INIT_SRC"

# 经 tar/zip 解包或 Windows 侧中转后，执行位可能丢失。这里先自行修复，
# 否则下面执行 -version 会以 Permission denied 失败，并被误报为架构不匹配。
if [ ! -x "$BIN_SRC" ]; then
    chmod +x "$BIN_SRC" 2>/dev/null || true
fi

if ! "$BIN_SRC" -version >/dev/null 2>&1; then
    if [ ! -x "$BIN_SRC" ]; then
        die "二进制没有执行权限，且自动修复失败：$BIN_SRC
请手动执行 chmod +x \"$BIN_SRC\" 后重试。"
    fi
    _err=$("$BIN_SRC" -version 2>&1 | head -n 1)
    case "$_err" in
        *"Exec format error"*|*"cannot execute binary file"*)
            die "二进制格式与当前设备不匹配（$_err）。
当前设备架构：$(uname -m)
请使用对应架构的版本（linux-amd64 / linux-arm64 / linux-mipsle 等）。" ;;
        *)
            die "二进制执行失败：$_err
当前设备架构：$(uname -m)" ;;
    esac
fi

ARCH=$(uname -m)
BIN_VER=$("$BIN_SRC" -version 2>/dev/null | awk '{print $2}')
ok "系统架构 $ARCH，二进制版本 $BIN_VER"

# ---------------------------------------------------------------- 停止旧服务

if [ -x "$INIT_DST" ]; then
    info "检测到已安装的服务，正在停止…"
    "$INIT_DST" stop >/dev/null 2>&1 || true
    # Give the process a moment to release the listening socket.
    sleep 1
fi

if pgrep -f "$BIN_DST" >/dev/null 2>&1; then
    info "结束残留进程…"
    killall "$BIN_NAME" >/dev/null 2>&1 || true
    sleep 1
fi

if [ -x "$OLD_INIT" ]; then
    info "检测到旧版服务（$OLD_NAME），正在停止…"
    "$OLD_INIT" stop >/dev/null 2>&1 || true
    sleep 1
fi
if pgrep -f "$OLD_BIN" >/dev/null 2>&1; then
    info "结束旧版残留进程…"
    killall "$OLD_NAME" >/dev/null 2>&1 || true
    sleep 1
fi

# ---------------------------------------------------------------- 安装二进制

if [ -f "$BIN_DST" ] && [ "$FORCE" -ne 1 ]; then
    # Only skip when the content is actually identical.
    if cmp -s "$BIN_SRC" "$BIN_DST"; then
        ok "二进制已是最新，跳过复制"
    else
        info "更新二进制（旧版本备份为 $BIN_DST.bak）"
        cp -f "$BIN_DST" "$BIN_DST.bak" 2>/dev/null || true
        cp -f "$BIN_SRC" "$BIN_DST"
    fi
else
    info "安装二进制到 $BIN_DST"
    cp -f "$BIN_SRC" "$BIN_DST"
fi

chmod 0755 "$BIN_DST"
ok "二进制已就位：$BIN_DST"

# ---------------------------------------------------------------- 安装 init

NEED_INIT=0
if [ ! -f "$INIT_DST" ]; then
    NEED_INIT=1
elif ! cmp -s "$INIT_SRC" "$INIT_DST"; then
    NEED_INIT=1
fi

if [ "$NEED_INIT" -eq 1 ]; then
    info "安装服务脚本到 $INIT_DST"
    cp -f "$INIT_SRC" "$INIT_DST"
fi
chmod 0755 "$INIT_DST"
ok "服务脚本已就位"

# ---------------------------------------------------------------- UCI 配置

if [ ! -f "$UCI_CFG" ]; then
    info "创建 UCI 配置 $UCI_CFG"
    cat > "$UCI_CFG" <<EOF
config $INIT_NAME 'main'
	option enabled '1'
	option data_dir '$DATA_DIR'
	option listen '$LISTEN_HOST:$LISTEN_PORT'
	option log_file '$DATA_DIR/service.log'
	option log_max_kb '2048'
EOF
    chmod 0644 "$UCI_CFG"
    ok "UCI 配置已创建（可用 uci set 修改）"
else
    # Keep an existing configuration but make sure the data directory is set.
    current_dir=$(uci -q get "$INIT_NAME.main.data_dir" 2>/dev/null || echo "")
    if [ -z "$current_dir" ]; then
        uci set "$INIT_NAME.main.data_dir=$DATA_DIR" 2>/dev/null || true
        uci commit "$INIT_NAME" 2>/dev/null || true
        ok "已补全 UCI 配置中的数据目录"
    else
        DATA_DIR="$current_dir"
        ok "沿用已有的 UCI 配置（数据目录 $DATA_DIR）"
    fi
fi

# ---------------------------------------------------------------- 数据目录

# 从旧版升级：把旧数据目录整体搬过来，保留原有配置、任务与执行历史。
if [ ! -d "$DATA_DIR" ] && [ -d "$OLD_DATA" ] && [ "$DATA_DIR" = "/etc/qzrs-webdav-backup" ]; then
    info "迁移旧数据目录：$OLD_DATA → $DATA_DIR"
    mv "$OLD_DATA" "$DATA_DIR"
    # config.json 里存有旧的 data_dir；不改的话服务会把 state 写回旧路径，
    # 并在旧位置重新创建目录。统一指向新位置。
    if [ -f "$DATA_DIR/config.json" ]; then
        sed -i "s|\"$OLD_DATA\"|\"$DATA_DIR\"|g" "$DATA_DIR/config.json"
    fi
    ok "旧版数据已迁移"
fi

mkdir -p "$DATA_DIR"
chmod 700 "$DATA_DIR"
ok "数据目录：$DATA_DIR"

FRESH_INSTALL=0
if [ ! -f "$DATA_DIR/config.json" ]; then
    FRESH_INSTALL=1
fi

# ---------------------------------------------------------------- 清理旧版残留

if [ -f "$OLD_INIT" ]; then
    "$OLD_INIT" disable >/dev/null 2>&1 || true
    rm -f "$OLD_INIT"
    ok "已移除旧版服务脚本：$OLD_INIT"
fi
if [ -f "$OLD_UCI" ]; then
    rm -f "$OLD_UCI"
    ok "已移除旧版 UCI 配置：$OLD_UCI"
fi
if [ -f "$OLD_BIN" ]; then
    rm -f "$OLD_BIN"
    ok "已移除旧版二进制：$OLD_BIN"
fi

# ---------------------------------------------------------------- 启动服务

if [ "$AUTO_START" -eq 1 ]; then
    "$INIT_DST" enable >/dev/null 2>&1 || warn "设置开机自启失败"
    ok "已设置为开机自启"

    info "启动服务…"
    if ! "$INIT_DST" start >/dev/null 2>&1; then
        "$INIT_DST" restart >/dev/null 2>&1 || true
    fi

    # Wait for the process to come up and (on a fresh install) write its
    # one-time banner into the log.
    i=0
    while [ $i -lt 8 ]; do
        sleep 1
        if pgrep -f "$BIN_DST" >/dev/null 2>&1; then
            break
        fi
        i=$((i + 1))
    done

    if pgrep -f "$BIN_DST" >/dev/null 2>&1; then
        ok "服务已启动"
    else
        warn "服务似乎没有启动，请检查日志：$DATA_DIR/service.log"
    fi
fi

# ---------------------------------------------------------------- 显示凭据

# 优先取 OpenWrt 的 LAN 地址再退化到默认路由源地址。`ip route get 1` 给的是默认
# 路由出口，在 PPPoE 拨号设备上是 WAN 地址（实测为 10.x），局域网内的浏览器
# 打不开它，而 uci 里的 lan 地址才是用户真正要访问的。
IP=$(uci -q get network.lan.ipaddr 2>/dev/null | cut -d/ -f1)
[ -n "$IP" ] || IP=$(ip route get 1 2>/dev/null | awk '{for(i=1;i<=NF;i++) if($i=="src") {print $(i+1); exit}}')
[ -n "$IP" ] || IP="<设备IP>"

EFFECTIVE_PORT="$LISTEN_PORT"
if [ "$AUTO_START" -eq 1 ] && [ -f "$DATA_DIR/config.json" ]; then
    cfg_listen=$(sed -n 's/.*"listen"[^"]*"\([^"]*\)".*/\1/p' "$DATA_DIR/config.json" 2>/dev/null | head -1)
    [ -n "$cfg_listen" ] && EFFECTIVE_PORT="${cfg_listen##*:}"
fi

echo ""
echo "==============================================="
if [ "$FRESH_INSTALL" -eq 1 ] && [ "$AUTO_START" -eq 1 ]; then
    PASSWORD=""
    i=0
    while [ $i -lt 6 ]; do
        PASSWORD=$(sed -n 's/.*密 *码: *//p' "$DATA_DIR/service.log" 2>/dev/null | head -1 | tr -d '\r')
        [ -n "$PASSWORD" ] && break
        sleep 1
        i=$((i + 1))
    done

    if [ -n "$PASSWORD" ]; then
        echo " 安装完成 — 登录信息"
        echo ""
        echo "   访问地址 : http://$IP:$EFFECTIVE_PORT/"
        echo "   用户名   : admin"
        echo "   初始密码 : $PASSWORD"
        echo ""
        echo " 初始密码为默认值 admin，登录后请立即在「设置」中修改。"
    else
        echo " 安装完成"
        echo ""
        echo "   访问地址 : http://$IP:$EFFECTIVE_PORT/"
        echo "   默认账号 : admin / admin（登录后请立即修改密码）"
        echo "   日志文件 : $DATA_DIR/service.log"
    fi
else
    echo " 安装完成"
    echo ""
    echo "   访问地址 : http://$IP:$EFFECTIVE_PORT/"
    echo "   数据目录 : $DATA_DIR"
    echo "   日志文件 : $DATA_DIR/service.log"
fi
echo "==============================================="
echo ""
echo " 常用命令："
echo "   $INIT_DST status     查看状态"
echo "   $INIT_DST restart    重启服务"
echo "   logread | grep qzrs-webdav-backup    查看系统日志"
echo ""
