#!/bin/bash
set -euo pipefail
umask 077

test -s /run/secrets/vnc-password
mkdir -p "$HOME/chrome-migrated"
browser_pid=
service_pids=()

cleanup() {
    trap - EXIT TERM INT
    if [[ -n "$browser_pid" ]] && kill -0 "$browser_pid" 2>/dev/null; then
        kill -TERM "$browser_pid"
        wait "$browser_pid" || true
    fi
    for service_pid in "${service_pids[@]}"; do
        if kill -0 "$service_pid" 2>/dev/null; then
            kill -TERM "$service_pid"
        fi
    done
    wait || true
}
trap cleanup EXIT
trap 'exit 0' TERM INT

dbus-daemon --session --nofork --address="$DBUS_SESSION_BUS_ADDRESS" &
service_pids+=("$!")
Xvfb "$DISPLAY" -screen 0 1440x900x24 -nolisten tcp &
service_pids+=("$!")
for ((attempt = 0; attempt < 100; attempt++)); do
    if xdpyinfo >/dev/null 2>&1; then
        break
    fi
    sleep 0.1
done
xdpyinfo >/dev/null

openbox &
service_pids+=("$!")
x11vnc -display "$DISPLAY" -forever -shared -localhost -rfbport 5900 \
    -rfbauth /run/secrets/vnc-password -noxdamage -quiet &
service_pids+=("$!")
websockify --web=/usr/share/novnc 6080 127.0.0.1:5900 &
service_pids+=("$!")
google-chrome-stable --no-sandbox --no-first-run --no-default-browser-check \
    --disable-session-crashed-bubble --password-store=basic \
    --user-data-dir="$HOME/chrome-migrated" --start-maximized \
    --profile-directory="${CHROME_PROFILE:-Operator}" about:blank \
    >"$HOME/chrome-runtime.log" 2>&1 &
browser_pid=$!

wait -n "$browser_pid" "${service_pids[@]}"
