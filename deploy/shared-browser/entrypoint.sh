#!/usr/bin/env bash
set -Eeuo pipefail

CONFIG_DIR=/config
PROFILE_DIR="$CONFIG_DIR/chrome"
VNC_AUTH="$CONFIG_DIR/vnc.passwd"
DISPLAY_ID=:99
SCREEN="${SHARED_BROWSER_SCREEN:-1920x1080x24}"
READY_TIMEOUT="${SHARED_BROWSER_READY_TIMEOUT:-60}"
STOP_TIMEOUT=20
PROFILE_DIRECTORY="${SHARED_BROWSER_PROFILE_DIRECTORY:-}"

log() { printf '%s entrypoint: %s\n' "$(date -u +%FT%TZ)" "$*"; }
die() { log "ERROR: $*" >&2; exit 1; }

[[ "$PROFILE_DIRECTORY" =~ ^(Default|Profile\ [0-9]+)$ ]] || die "SHARED_BROWSER_PROFILE_DIRECTORY must be Default or 'Profile N'"
[[ "$SCREEN" =~ ^[0-9]+x[0-9]+x[0-9]+$ ]] || die "invalid SHARED_BROWSER_SCREEN"
[[ "$READY_TIMEOUT" =~ ^[0-9]+$ ]] || die "invalid SHARED_BROWSER_READY_TIMEOUT"
[[ -d "$CONFIG_DIR" && -w "$CONFIG_DIR" ]] || die "$CONFIG_DIR must be a writable directory"
[[ -f "$VNC_AUTH" && -s "$VNC_AUTH" && -r "$VNC_AUTH" ]] || die "$VNC_AUTH must be a readable, non-empty x11vnc auth file"
mkdir -p "$PROFILE_DIR"
[[ -w "$PROFILE_DIR" ]] || die "$PROFILE_DIR is not writable by uid $(id -u)"
[[ -d "$PROFILE_DIR/$PROFILE_DIRECTORY" ]] || log "profile directory '$PROFILE_DIRECTORY' does not exist yet; Chrome will create an empty one"

export HOME=/tmp/home
export DISPLAY="$DISPLAY_ID"
mkdir -p "$HOME"

# Profile lock files record the previous host/pid and block Chrome on a new host; they hold no user data.
rm -f "$PROFILE_DIR/SingletonLock" "$PROFILE_DIR/SingletonCookie" "$PROFILE_DIR/SingletonSocket"

declare -a PIDS=() NAMES=()

start() {
    local name=$1 pid
    shift
    "$@" &
    pid=$!
    PIDS+=("$pid")
    NAMES+=("$name")
    log "started $name pid=$pid"
}

stop_pid() {
    local pid=$1 n
    kill -0 "$pid" 2>/dev/null || return 0
    kill -TERM "$pid" 2>/dev/null || true
    for ((n = 0; n < STOP_TIMEOUT * 10; n++)); do
        kill -0 "$pid" 2>/dev/null || return 0
        sleep 0.1
    done
    log "pid $pid ignored TERM, killing"
    kill -KILL "$pid" 2>/dev/null || true
}

terminate_all() {
    local i
    trap - EXIT TERM INT
    # Reverse start order: Chrome flushes the profile before Xvfb goes away.
    for ((i = ${#PIDS[@]} - 1; i >= 0; i--)); do
        stop_pid "${PIDS[i]}"
    done
}

trap terminate_all EXIT
trap 'log "termination signal received"; exit 0' TERM INT

wait_ready() {
    local name=$1 pid=$2 n max=$((READY_TIMEOUT * 5))
    shift 2
    for ((n = 0; n < max; n++)); do
        kill -0 "$pid" 2>/dev/null || die "$name exited before becoming ready"
        if "$@" 2>/dev/null; then
            log "$name ready"
            return 0
        fi
        sleep 0.2
    done
    die "$name not ready within ${READY_TIMEOUT}s"
}

probe_socket() { [[ -S "$1" ]]; }
probe_tcp() { (exec 3<>"/dev/tcp/127.0.0.1/$1"); }
probe_http() { curl -fsS -o /dev/null --max-time 2 "$1"; }

W=${SCREEN%%x*}
rest=${SCREEN#*x}
H=${rest%%x*}

start xvfb Xvfb "$DISPLAY_ID" -screen 0 "$SCREEN" -nolisten tcp -ac
wait_ready xvfb "${PIDS[-1]}" probe_socket "/tmp/.X11-unix/X${DISPLAY_ID#:}"

start chrome google-chrome-stable \
    --user-data-dir="$PROFILE_DIR" \
    --profile-directory="$PROFILE_DIRECTORY" \
    --password-store=basic \
    --no-sandbox \
    --hide-crash-restore-bubble \
    --remote-debugging-port=9222 \
    --no-first-run \
    --no-default-browser-check \
    --window-position=0,0 \
    --window-size="$W,$H" \
    about:blank
wait_ready chrome "${PIDS[-1]}" probe_http http://127.0.0.1:9222/json/version

start x11vnc x11vnc -display "$DISPLAY_ID" -rfbport 5900 -rfbauth "$VNC_AUTH" -forever -shared -noxdamage
wait_ready x11vnc "${PIDS[-1]}" probe_tcp 5900

start novnc websockify --web /usr/share/novnc 6080 127.0.0.1:5900
wait_ready novnc "${PIDS[-1]}" probe_http http://127.0.0.1:6080/vnc.html

start cdp-proxy socat TCP-LISTEN:9223,fork,reuseaddr TCP:127.0.0.1:9222
wait_ready cdp-proxy "${PIDS[-1]}" probe_http http://127.0.0.1:9223/json/version

log "all components ready"

while :; do
    wait -n "${PIDS[@]}" || true
    for i in "${!PIDS[@]}"; do
        kill -0 "${PIDS[i]}" 2>/dev/null || die "${NAMES[i]} exited"
    done
done
