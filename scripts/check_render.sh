#!/bin/sh
#
# 用无头浏览器真实渲染控制台，检查页面有没有卡在「正在载入控制台…」。
#
# 为什么需要单独这一步：服务端的端到端测试只打 API，从不渲染页面。前端一旦
# 解析失败（语法不被浏览器支持、MIME 不对、资源 404、缓存错配），所有 API 测试
# 依然全绿，而用户打开页面只能看到一个静止的加载提示 —— 2026-10-02 就踩了这个坑。
#
# 用法：
#   sh scripts/check_render.sh [URL]
#   （默认 http://192.168.11.1:8787/）
#
# 退出码非 0 表示渲染有问题。
set -e

URL=${1:-http://192.168.11.1:8787/}
OUT_DIR="${TMPDIR:-/tmp}/wdb-render"
mkdir -p "$OUT_DIR"

CHROME=""
for c in \
  "/c/Program Files/Google/Chrome/Application/chrome.exe" \
  "/c/Program Files (x86)/Google/Chrome/Application/chrome.exe" \
  "/c/Program Files (x86)/Microsoft/Edge/Application/msedge.exe" \
  "/c/Program Files/Microsoft/Edge/Application/msedge.exe" \
  "$(command -v google-chrome || true)" \
  "$(command -v chromium || true)" \
  "$(command -v chromium-browser || true)"; do
  if [ -n "$c" ] && [ -f "$c" ]; then CHROME="$c"; break; fi
done

if [ -z "$CHROME" ]; then
  echo "  [!] 未找到 Chrome/Edge，跳过渲染检查（不影响其它检查）"
  exit 0
fi

echo "  渲染目标：$URL"
"$CHROME" --headless=new --disable-gpu --no-sandbox --no-proxy-server \
    --virtual-time-budget=8000 --enable-logging=stderr --log-level=0 \
    --dump-dom "$URL" > "$OUT_DIR/dom.html" 2> "$OUT_DIR/console.log" || true

DOM_BYTES=$(wc -c < "$OUT_DIR/dom.html" | tr -d ' ')
fail=0

# 1) #boot 元素必须已从 DOM 中移除 —— 说明 app.js 执行到了 boot()。
#    不能只检查 hidden 属性：样式若覆盖了 [hidden] 的默认 display:none，
#    属性在而元素照样显示（上下两截同屏显示的事故就是这么漏检的）。
if ! grep -q '<div id="boot"' "$OUT_DIR/dom.html"; then
  echo "  [+] 启动提示已从页面移除（app.js 已执行）"
elif grep -q 'id="boot"[^>]*hidden' "$OUT_DIR/dom.html"; then
  echo "  [!] #boot 仍以 hidden 方式存在（浏览器缓存的旧版 app.js？）"
  fail=1
else
  echo "  [x] 页面仍停在启动提示：app.js 没有执行完"
  fail=1
fi

# 2) 必须真的渲染出界面
if grep -q 'id="login-form"' "$OUT_DIR/dom.html"; then
  echo "  [+] 已渲染登录表单"
elif grep -q 'class="topbar"' "$OUT_DIR/dom.html"; then
  echo "  [+] 已渲染控制台主界面（带会话）"
else
  echo "  [x] #app 容器没有渲染出内容"
  fail=1
fi

# 3) 不应触发启动兜底。
# 判据：#boot 元素的**内容**是否被替换成了兜底文案。不能在整份 DOM 里搜
# 「正在载入控制台」—— 内联 <script> 的源码（含注释）会一起被 dump 出来，
# 直接搜它永远命中，是假阳性；所以只看 <div id="boot" 标签之后几行的内容。
if grep -A3 '<div id="boot"' "$OUT_DIR/dom.html" | grep -q '未能启动'; then
  echo "  [x] 触发了启动兜底：#boot 的内容已被替换"
  fail=1
else
  echo "  [+] 启动兜底未被触发"
fi

# 4) 控制台不应有未捕获错误
if grep -qiE 'Uncaught|SyntaxError|Refused to execute|ERR_' "$OUT_DIR/console.log"; then
  echo "  [x] 浏览器控制台有错误："
  grep -iE 'Uncaught|SyntaxError|Refused to execute|ERR_' "$OUT_DIR/console.log" \
    | head -5 | sed 's/^/      /'
  fail=1
fi

if [ "$fail" -eq 0 ]; then
  echo "  [+] 渲染检查通过（DOM ${DOM_BYTES} 字节）"
  exit 0
fi

echo "  [x] 渲染检查失败（DOM ${DOM_BYTES} 字节）"
echo "      产物：$OUT_DIR/dom.html 与 $OUT_DIR/console.log"
exit 1
