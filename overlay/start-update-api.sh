#!/bin/bash
# Idempotent: start ic2 overlay update-api sidecar on 127.0.0.1:25693
OVERLAY=/home/box/ic2/overlay
PIDFILE="$OVERLAY/update-api.pid"
LOG="$OVERLAY/update-api.log"

port_up() { ss -tln | grep -q "127.0.0.1:25693 "; }
alive() { [[ -n "${1:-}" ]] && kill -0 "$1" 2>/dev/null; }

if port_up; then
  echo "already listening on 127.0.0.1:25693"
  exit 0
fi

old=$(cat "$PIDFILE" 2>/dev/null || true)
if alive "$old"; then
  echo "already running pid=$old"
  exit 0
fi

nohup python3 "$OVERLAY/update-api.py" >> "$LOG" 2>&1 &
echo $! > "$PIDFILE"
for _ in 1 2 3 4 5 6 7 8 9 10; do
  if port_up; then
    echo "started ic2 update-api pid=$(cat "$PIDFILE")"
    exit 0
  fi
  sleep 0.1
done
echo "started ic2 update-api pid=$(cat "$PIDFILE") (port not yet up)"
