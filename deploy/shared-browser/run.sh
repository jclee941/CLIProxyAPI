#!/usr/bin/env bash
set -Eeuo pipefail

die() { printf 'run.sh: %s\n' "$*" >&2; exit 1; }

IMAGE="${SHARED_BROWSER_IMAGE:-cpa-shared-browser:local}"
CONTAINER_NAME="${SHARED_BROWSER_CONTAINER_NAME:-}"
PROFILE_DIRECTORY="${SHARED_BROWSER_PROFILE_DIRECTORY:-}"
DATA_DIR="${SHARED_BROWSER_DATA_DIR:-}"
BIND_IP="${SHARED_BROWSER_BIND_IP:-127.0.0.1}"
CDP_BIND_IP="${SHARED_BROWSER_CDP_BIND_IP:-127.0.0.1}"
NOVNC_PORT="${SHARED_BROWSER_NOVNC_PORT:-}"
CDP_PORT="${SHARED_BROWSER_CDP_PORT:-}"
VNC_PORT="${SHARED_BROWSER_VNC_PORT:-}"
MEMORY="${SHARED_BROWSER_MEMORY:-2g}"
RUN_UID="${SHARED_BROWSER_UID:-$(id -u)}"
RUN_GID="${SHARED_BROWSER_GID:-$(id -g)}"

is_ipv4() { [[ "$1" =~ ^([0-9]{1,3}\.){3}[0-9]{1,3}$ ]]; }
is_loopback() { [[ "$1" =~ ^127\.[0-9]+\.[0-9]+\.[0-9]+$ ]]; }
is_port() { [[ "$1" =~ ^[0-9]+$ ]] && ((10#$1 >= 1 && 10#$1 <= 65535)); }
check_owned() {
    local path=$1 kind=$2 label=$3
    [[ ! -L "$path" ]] || die "$label must not be a symlink: $path"
    if [[ $kind == d ]]; then
        [[ -d "$path" ]] || die "$label is not a directory: $path"
    else
        [[ -f "$path" && -s "$path" ]] || die "$label is not a non-empty regular file: $path"
    fi
    [[ "$(stat -c %u "$path")" == "$RUN_UID" ]] || die "$label must be owned by uid $RUN_UID: $path"
    (((8#$(stat -c %a "$path")) & 077)) && die "$label must not be accessible by group/other (chmod go-rwx): $path"
    return 0
}

command -v docker >/dev/null || die "docker not found"
[[ "$CONTAINER_NAME" =~ ^shared-browser-[a-z0-9][a-z0-9-]*$ ]] || die "SHARED_BROWSER_CONTAINER_NAME is required and must match shared-browser-<account> (lowercase letters, digits, dashes)"
[[ "$PROFILE_DIRECTORY" =~ ^(Default|Profile\ [0-9]+)$ ]] || die "SHARED_BROWSER_PROFILE_DIRECTORY is required and must be Default or 'Profile N'"
[[ "$RUN_UID" =~ ^[0-9]+$ && "$RUN_GID" =~ ^[0-9]+$ ]] || die "uid/gid must be numeric"
is_ipv4 "$BIND_IP" || die "SHARED_BROWSER_BIND_IP must be an IPv4 address"
is_ipv4 "$CDP_BIND_IP" || die "SHARED_BROWSER_CDP_BIND_IP must be an IPv4 address"
is_port "$NOVNC_PORT" || die "SHARED_BROWSER_NOVNC_PORT is required (1-65535)"
is_port "$CDP_PORT" || die "SHARED_BROWSER_CDP_PORT is required (1-65535)"
[[ -z "$VNC_PORT" ]] || is_port "$VNC_PORT" || die "invalid SHARED_BROWSER_VNC_PORT"
[[ "$MEMORY" =~ ^[0-9]+[bkmg]$ ]] || die "SHARED_BROWSER_MEMORY must look like 2g or 1536m"
# CDP has no authentication: refuse non-loopback exposure unless explicitly acknowledged.
if ! is_loopback "$CDP_BIND_IP" && [[ "${SHARED_BROWSER_ALLOW_REMOTE_CDP:-}" != 1 ]]; then
    die "CDP is unauthenticated; set SHARED_BROWSER_ALLOW_REMOTE_CDP=1 to bind it to $CDP_BIND_IP"
fi

[[ -n "$DATA_DIR" && "$DATA_DIR" == /* ]] || die "SHARED_BROWSER_DATA_DIR is required and must be an absolute path"
check_owned "$DATA_DIR" d "data dir"
check_owned "$DATA_DIR/chrome" d "Chrome user data dir"
check_owned "$DATA_DIR/chrome/$PROFILE_DIRECTORY" d "Chrome profile directory"
check_owned "$DATA_DIR/vnc.passwd" f "VNC auth file"

docker image inspect "$IMAGE" >/dev/null 2>&1 || die "image not found: $IMAGE (build it first, see README.md)"
existing="$(docker ps -a --filter "name=^/${CONTAINER_NAME}\$" --format '{{.Names}}')"
[[ -z "$existing" ]] || die "container $CONTAINER_NAME already exists; refusing to touch it"

ports=(-p "$BIND_IP:$NOVNC_PORT:6080" -p "$CDP_BIND_IP:$CDP_PORT:9223")
[[ -z "$VNC_PORT" ]] || ports+=(-p "$BIND_IP:$VNC_PORT:5900")
envs=(-e "SHARED_BROWSER_PROFILE_DIRECTORY=$PROFILE_DIRECTORY")
[[ -z "${SHARED_BROWSER_SCREEN:-}" ]] || envs+=(-e "SHARED_BROWSER_SCREEN=$SHARED_BROWSER_SCREEN")

exec docker run -d \
    --name "$CONTAINER_NAME" \
    --label "cpa.shared-browser.profile=$PROFILE_DIRECTORY" \
    --init \
    --restart unless-stopped \
    --user "$RUN_UID:$RUN_GID" \
    --shm-size 1g \
    --memory "$MEMORY" \
    --stop-timeout 60 \
    --security-opt no-new-privileges \
    --log-opt max-size=10m --log-opt max-file=3 \
    -v "$DATA_DIR:/config" \
    "${ports[@]}" \
    "${envs[@]}" \
    "$IMAGE"
