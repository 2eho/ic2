#!/usr/bin/env bash
# Atomic frontend+Go update for ic2 (127.0.0.1:25692).
# Optional git pull/tag → bak live dist+binary → npm/go rebuild →
# atomic swap → start.sh --restart → rollback to last-good on health fail.
# Never touches infinite-canvas-ic / studio / edit / canvas / comfy / cloudflared / Clash.
set -euo pipefail

ROOT=/home/box/ic2
DIST="$ROOT/web/dist"
BIN="$ROOT/bin/ic-server"
RUN="$ROOT/run"
BAK_DIR="$RUN/dist-bak"
LAST_GOOD="$BAK_DIR/last-good"
LAST_GOOD_BIN="$BAK_DIR/ic-server-last-good"
HOST=127.0.0.1
PORT=25692
HEALTH_URL="http://${HOST}:${PORT}/healthz"
START_SH="$ROOT/start.sh"
UPDATE_LOG="$ROOT/log/update.log"
TS="$(date -u +%Y%m%dT%H%M%SZ)"
STAGED="$RUN/dist-staged-$TS"
PRE_BAK="$RUN/dist-pre-$TS"
BIN_STAGED="$RUN/ic-server-staged-$TS"
BIN_PRE="$RUN/ic-server-pre-$TS"

export PATH="/home/box/.local/bin:/home/box/go/go1.25.0/bin:${PATH}"
export GOPATH="${GOPATH:-/home/box/go/gopath}"
export GOCACHE="${GOCACHE:-/home/box/go/cache}"
export CGO_ENABLED=0

mkdir -p "$BAK_DIR" "$ROOT/log" "$RUN" "$ROOT/bin"

log() { echo "[$(date -u +%Y-%m-%dT%H:%M:%SZ)] $*" | tee -a "$UPDATE_LOG"; }

http_ok() {
  local code
  code=$(curl -sS -o /dev/null -w '%{http_code}' --connect-timeout 2 --max-time 5 "$HEALTH_URL" || echo 000)
  [[ "$code" == "200" ]]
}

restore_tree() {
  local src="$1"
  [[ -d "$src" && -f "$src/index.html" ]] || return 1
  local tmp="$RUN/dist-restore-$TS"
  rm -rf "$tmp"
  cp -a "$src" "$tmp"
  rm -rf "$DIST"
  mv "$tmp" "$DIST"
}

restore_bin() {
  local src="$1"
  [[ -x "$src" ]] || return 1
  cp -a "$src" "$BIN"
}

rollback() {
  local reason="$1"
  log "ROLLBACK: $reason"
  if restore_tree "$LAST_GOOD"; then
    restore_bin "$LAST_GOOD_BIN" || true
    "$START_SH" --restart || true
    if http_ok; then
      log "rollback → last-good OK"
      return 0
    fi
  fi
  if restore_tree "$PRE_BAK"; then
    restore_bin "$BIN_PRE" || true
    "$START_SH" --restart || true
    if http_ok; then
      log "rollback → pre-update bak OK"
      return 0
    fi
  fi
  log "rollback FAILED (no usable bak)"
  return 1
}

MODE="${1:-rebuild}"
REF="${2:-}"

cd "$ROOT"

case "$MODE" in
  rebuild|web) log "mode=rebuild (no git)" ;;
  pull)
    log "mode=pull ref=${REF:-HEAD}"
    git fetch --tags --prune origin
    if [[ -n "$REF" ]]; then
      git checkout --force "$REF"
    else
      BRANCH=$(git rev-parse --abbrev-ref HEAD)
      git pull --ff-only origin "$BRANCH" || log "ff-only pull skipped (local diverged/dirty ok)"
    fi
    ;;
  tag)
    [[ -n "$REF" ]] || { echo "usage: $0 tag <tag>" >&2; exit 2; }
    log "mode=tag ref=$REF"
    git fetch --tags --prune origin
    git checkout --force "$REF"
    ;;
  *)
    echo "usage: $0 [rebuild|pull|tag] [ref]" >&2
    echo "  rebuild       npm+go rebuild, atomic dist/bin swap, restart" >&2
    echo "  pull [branch] git pull/ff then rebuild" >&2
    echo "  tag <tag>     git checkout tag then rebuild" >&2
    exit 2
    ;;
esac

# Snapshot live dist + binary BEFORE vite clobbers dist/.
if [[ -f "$DIST/index.html" ]]; then
  rm -rf "$PRE_BAK"
  cp -a "$DIST" "$PRE_BAK"
  log "pre-update bak → $PRE_BAK"
  if [[ ! -d "$LAST_GOOD" || ! -f "$LAST_GOOD/index.html" ]]; then
    cp -a "$DIST" "$LAST_GOOD"
    log "seeded last-good from live dist"
  fi
fi
if [[ -x "$BIN" ]]; then
  cp -a "$BIN" "$BIN_PRE"
  if [[ ! -x "$LAST_GOOD_BIN" ]]; then
    cp -a "$BIN" "$LAST_GOOD_BIN"
    log "seeded last-good binary"
  fi
fi

log "npm install + web build"
(
  cd "$ROOT/web"
  npm install --no-audit --no-fund
  npm run build
)
[[ -f "$DIST/index.html" ]] || {
  log "build failed: missing index.html"
  if [[ -d "$PRE_BAK" ]]; then
    rm -rf "$DIST"
    mv "$PRE_BAK" "$DIST"
  fi
  exit 1
}

log "go build ic-server"
(
  cd "$ROOT"
  VERSION=$(git describe --tags --always --dirty 2>/dev/null || echo dev)
  COMMIT=$(git rev-parse --short HEAD 2>/dev/null || echo none)
  DATE=$(date -u +%Y-%m-%dT%H:%M:%SZ)
  go build -trimpath -ldflags="-s -w -X github.com/context-flow/ic/internal/platform.Version=$VERSION -X github.com/context-flow/ic/internal/platform.Commit=$COMMIT -X github.com/context-flow/ic/internal/platform.Date=$DATE" -o "$BIN_STAGED" ./cmd/ic-server
)
[[ -x "$BIN_STAGED" ]] || {
  log "go build failed"
  if [[ -d "$PRE_BAK" ]]; then
    rm -rf "$DIST"
    mv "$PRE_BAK" "$DIST"
  fi
  exit 1
}

rm -rf "$STAGED"
mv "$DIST" "$STAGED"

if [[ -d "$PRE_BAK" ]]; then
  cp -a "$PRE_BAK" "$DIST"
fi

rm -rf "$DIST"
mv "$STAGED" "$DIST"
log "atomic dist swap complete → $DIST"

if [[ -x "$BIN" ]]; then
  mv -f "$BIN" "$RUN/ic-server-replaced-$TS" || true
fi
mv -f "$BIN_STAGED" "$BIN"
chmod +x "$BIN"
log "atomic bin swap complete → $BIN"

if [[ -x "$ROOT/bin/inject-update-widget.py" ]]; then
  python3 "$ROOT/bin/inject-update-widget.py" || true
fi
log "start.sh --restart"
"$START_SH" --restart

ok=0
for _ in $(seq 1 50); do
  if http_ok; then ok=1; break; fi
  sleep 0.25
done

if [[ "$ok" != "1" ]]; then
  rollback "ic2 healthz not 200 after update" || true
  exit 1
fi

if [[ -d "$LAST_GOOD" ]]; then
  rm -rf "$BAK_DIR/prev"
  mv "$LAST_GOOD" "$BAK_DIR/prev"
fi
cp -a "$DIST" "$LAST_GOOD"
if [[ -x "$BIN" ]]; then
  cp -a "$BIN" "$LAST_GOOD_BIN"
fi
"$START_SH" >/dev/null 2>&1 || true
rm -rf "$PRE_BAK" "$BIN_PRE"
git rev-parse HEAD > "$ROOT/overlay/deployed.sha" 2>/dev/null || true

log "update OK health=200"
echo "OK $HEALTH_URL"
