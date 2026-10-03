#!/bin/sh
#
# 提交并推送到 GitHub 私有仓库（qzrsa/qzrs-webdav-backup）。
#
# 用法：
#   sh scripts/git-push.sh "提交说明"     # 提交全部改动并推送
#   sh scripts/git-push.sh                # 用带时间戳的默认说明
#
# 为什么需要这个脚本而不是直接 git push：
#   1) 本机 WorkBuddy PortableGit 的 system 级 gitconfig 使凭据 helper 在受限会话
#      里可能直接挂死（无输出直到被 kill）。这里改用「token 内联 URL + 清空 helper
#      链」的推送形式，退出码可信、输出完整。
#   2) 本机 push 的退出码历史上出现过「空输出 + exit 128 但实际推上去了」和
#      「空输出 + 未推送」两种相反结果，所以脚本最后一定会打印远端 SHA，
#      以 SHA 是否等于本地 HEAD 作为唯一判据。
#
# 注意：本脚本需要凭据管理器可达，在沙箱受限的会话中请以管理员/非沙箱方式执行。
set -e

cd "$(dirname "$0")/.."
# 只存 host+path：token URL 会自己补上 https:// 前缀。若这里带上 scheme，
# 会拼成 https://x-access-token:xxx@https://github.com/... 并报
# "CONNECT tunnel failed, response 502"。
REPO="github.com/qzrsa/qzrs-webdav-backup.git"

MSG=${1:-"更新：$(date '+%Y-%m-%d %H:%M')"}

if [ -n "$(git status --porcelain)" ]; then
    git add -A
    git commit -q -m "$MSG"
    echo "[+] 已提交：$MSG"
else
    echo "[*] 工作区干净，无需提交"
fi

TOKEN=$(printf 'protocol=https\nhost=github.com\n\n' | git credential fill 2>/dev/null \
        | grep '^password=' | cut -d= -f2- || true)

if [ -z "$TOKEN" ]; then
    echo "[x] 取不到 GitHub 凭据（凭据管理器不可达）。" >&2
    echo "    请在不受限的终端执行一次 git push 完成认证后重试。" >&2
    exit 1
fi

git -c credential.helper= \
    -c "credential.https://github.com.helper=" \
    -c "credential.https://gist.github.com.helper=" \
    push "https://x-access-token:$TOKEN@$REPO" HEAD:main

LOCAL=$(git rev-parse HEAD)
REMOTE=$(curl -s -H "Authorization: Bearer $TOKEN" \
    "https://api.github.com/repos/qzrsa/qzrs-webdav-backup/commits/main" \
    | grep -m1 '"sha"' | cut -d'"' -f4 || true)

echo ""
echo "  本地 HEAD : $LOCAL"
echo "  远端 main : $REMOTE"
if [ "$LOCAL" = "$REMOTE" ]; then
    echo "  [+] 推送已确认"
else
    echo "  [!] 远端 SHA 与本地不一致，请检查（本地 tracking ref 在本机不可信，以 API 为准）"
    exit 1
fi
