#!/usr/bin/env bash
# 拉取上游参考仓库并抽取「契约面」变化，产出 upstream/change-report.json。
# 设计说明：docs/design/12-legacy-and-upstream.md
#
# 只做事实抽取，不改重写代码。变更非空时以退出码 1 结束，用于触发 Issue。
set -euo pipefail

UPSTREAM_REPO="${UPSTREAM_REPO:-https://github.com/basketikun/infinite-canvas.git}"
UPSTREAM_REF="${UPSTREAM_REF:-main}"
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
WORK="$ROOT/upstream"
CLONE="$WORK/infinite-canvas"
SNAPSHOT="$WORK/snapshot.json"
REPORT="$WORK/change-report.json"

mkdir -p "$WORK"

if [ ! -d "$CLONE/.git" ]; then
  git clone --depth 1 --branch "$UPSTREAM_REF" "$UPSTREAM_REPO" "$CLONE"
else
  git -C "$CLONE" fetch --depth 1 origin "$UPSTREAM_REF"
  git -C "$CLONE" reset --hard FETCH_HEAD
fi

UP_VERSION="$(cat "$CLONE/VERSION" 2>/dev/null || echo unknown)"
UP_COMMIT="$(git -C "$CLONE" rev-parse HEAD)"

PREV_VERSION=""
PREV_COMMIT=""
if [ -f "$SNAPSHOT" ]; then
  PREV_VERSION="$(node -e "process.stdout.write(require('$SNAPSHOT').version||'')" 2>/dev/null || true)"
  PREV_COMMIT="$(node -e "process.stdout.write(require('$SNAPSHOT').commit||'')" 2>/dev/null || true)"
fi

if [ "$UP_VERSION" = "$PREV_VERSION" ] && [ "$UP_COMMIT" = "$PREV_COMMIT" ]; then
  echo "upstream unchanged: $UP_VERSION ($UP_COMMIT)"
  exit 0
fi

echo "upstream changed: ${PREV_VERSION:-none} ($PREV_COMMIT) -> $UP_VERSION ($UP_COMMIT)"

VERSION="$UP_VERSION" COMMIT="$UP_COMMIT" PREV_VERSION="$PREV_VERSION" PREV_COMMIT="$PREV_COMMIT" \
  REPORT_PATH="$REPORT" CLONE_DIR="$CLONE" node "$ROOT/scripts/report-upstream.mjs"

node -e "
const fs=require('fs');
const p='$SNAPSHOT';
const prev=fs.existsSync(p)?JSON.parse(fs.readFileSync(p,'utf8')):{};
fs.writeFileSync(p, JSON.stringify({...prev, repo:'$UPSTREAM_REPO', version:'$UP_VERSION', commit:'$UP_COMMIT', analyzedAt:new Date().toISOString()}, null, 2));
"

# 变更非空 → 退出码 1，让 CI 建 Issue 提醒处理。
EXIT_CODE=0
if [ "${UP_VERSION}" != "${PREV_VERSION}" ]; then
  EXIT_CODE=1
fi
echo "report written to $REPORT (exit=$EXIT_CODE)"
exit "$EXIT_CODE"
