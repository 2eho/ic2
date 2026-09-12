#!/usr/bin/env bash
# Idempotent ic2 (context-flow.cloud/ic rewrite) local stack.
# Web+API: 127.0.0.1:25692  |  update-api: 127.0.0.1:25693
# Does not touch infinite-canvas-ic / studio-1 / edit / canvas / chatgpt2api / comfy / VLESS.
set -euo pipefail

ROOT=/home/box/ic2
DIST="$ROOT/web/dist"
BIN="$ROOT/bin/ic-server"
LOGDIR="$ROOT/logs"
RUN="$ROOT/run"
PID_WEB="$ROOT/ic2-web.pid"
LOG_WEB="$LOGDIR/ic2-web.log"
HOST=127.0.0.1
WEB_PORT=25692
DATA_DIR="$ROOT/data"

export PATH="/home/box/.local/bin:/home/box/go/go1.25.0/bin:${PATH}"

# Load .env (secrets + IC_*); do not echo values
if [[ -f "$ROOT/.env" ]]; then
  set -a
  # shellcheck disable=SC1091
  source "$ROOT/.env"
  set +a
fi

export IC_LISTEN="${IC_LISTEN:-127.0.0.1:25692}"
export IC_STATIC_DIR="${IC_STATIC_DIR:-$DIST}"
export IC_BLOB_FS_ROOT="${IC_BLOB_FS_ROOT:-$DATA_DIR/assets}"
export IC_DB_DSN="${IC_DB_DSN:-file:$DATA_DIR/ic.db?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)}"
export IC_SESSION_COOKIE_SECURE="${IC_SESSION_COOKIE_SECURE:-true}"

mkdir -p "$LOGDIR" "$ROOT/releases" "$RUN" "$DATA_DIR/assets"

port_up() { ss -tln | grep -q "127.0.0.1:${1} "; }
alive() { [[ -n "${1:-}" ]] && kill -0 "$1" 2>/dev/null; }

health_http() {
  local path="${1:-/healthz}"
  local code
  code=$(curl -sS -o /dev/null -w '%{http_code}' --connect-timeout 1 --max-time 3 "http://${HOST}:${WEB_PORT}${path}" 2>/dev/null || echo 000)
  [[ "$code" == "200" ]]
}

stop_one() {
  local pidfile=$1 name=$2 port=$3
  local pid
  pid=$(cat "$pidfile" 2>/dev/null || true)
  if alive "$pid"; then
    kill "$pid" 2>/dev/null || true
    for _ in $(seq 1 40); do
      alive "$pid" || break
      sleep 0.1
    done
    alive "$pid" && kill -9 "$pid" 2>/dev/null || true
  fi
  if port_up "$port"; then
    local p
    p=$(ss -tlnp 2>/dev/null | awk -v p="127.0.0.1:${port}" '$0 ~ p {print}' | grep -oE 'pid=[0-9]+' | cut -d= -f2 | sort -u)
    for x in $p; do kill "$x" 2>/dev/null || true; done
    sleep 0.2
  fi
  rm -f "$pidfile"
  echo "stopped $name"
}

refresh_runtime_config() {
  if [[ -x "$ROOT/bin/inject-update-widget.py" ]]; then
    python3 "$ROOT/bin/inject-update-widget.py" >/dev/null 2>&1 || true
  fi
}

start_web() {
  if port_up "$WEB_PORT"; then
    refresh_runtime_config || true
    echo "web already listening on ${HOST}:${WEB_PORT}"
    return 0
  fi
  old=$(cat "$PID_WEB" 2>/dev/null || true)
  if alive "$old"; then
    refresh_runtime_config || true
    echo "web already running pid=$old"
    return 0
  fi
  if [[ ! -x "$BIN" ]]; then
    echo "missing $BIN — run update.sh or: (cd $ROOT && go build -o $BIN ./cmd/ic-server)" >&2
    return 1
  fi
  if [[ ! -f "$DIST/index.html" ]]; then
    echo "missing $DIST/index.html — run update.sh or: (cd $ROOT/web && npm run build)" >&2
    return 1
  fi
  if [[ -z "${IC_SECRET_KEY:-}" ]]; then
    echo "IC_SECRET_KEY missing — set in $ROOT/.env" >&2
    return 1
  fi
  refresh_runtime_config
  cd "$ROOT"
  nohup "$BIN" >>"$LOG_WEB" 2>&1 &
  echo $! >"$PID_WEB"
  echo $! >"$RUN/ic2.pid"
  for _ in $(seq 1 80); do
    if port_up "$WEB_PORT" && health_http /healthz; then
      echo "started web pid=$(cat "$PID_WEB") http://${HOST}:${WEB_PORT}/"
      return 0
    fi
    sleep 0.15
  done
  if port_up "$WEB_PORT"; then
    echo "started web pid=$(cat "$PID_WEB") (port up, healthz not yet 200)"
    return 0
  fi
  echo "web failed to listen on ${HOST}:${WEB_PORT}" >&2
  tail -40 "$LOG_WEB" >&2 || true
  return 1
}

FORCE_RESTART=0
for a in "$@"; do
  if [[ "$a" == "--restart" || "$a" == "restart" ]]; then FORCE_RESTART=1; fi
done

if [[ "$FORCE_RESTART" == "1" ]]; then
  stop_one "$PID_WEB" web "$WEB_PORT"
  rm -f "$RUN/ic2.pid"
  echo "restart: cleared web"
fi

start_web

# Overlay update-api sidecar (127.0.0.1:25693). Idempotent; do not bounce web.
if [[ -x "$ROOT/overlay/start-update-api.sh" ]]; then
  "$ROOT/overlay/start-update-api.sh" >/dev/null 2>&1 || true
fi
