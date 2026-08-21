#!/bin/sh
# GitOps converger for the CLIProxyAPI fork.
#
# Run by cron on the deployment host. It pulls the configured branch into a
# dedicated deploy clone and rebuilds every compose project when the branch
# moved. It is a no-op when the clone already matches the remote.
#
# Environment:
#   GITOPS_DEPLOY_DIR      Required. Dedicated clone used ONLY for deployment.
#                          Never point this at a developer working tree.
#   GITOPS_REMOTE          Git remote URL. Default: fork origin.
#   GITOPS_BRANCH          Branch to converge on. Default: main.
#   GITOPS_COMPOSE_FILES   Space-separated compose files, relative to the
#                          deploy dir, applied in order.
#   GITOPS_LOCK_FILE       Lock file path. Default: sibling of the deploy dir.

set -eu

timestamp() {
    date -u '+%Y-%m-%dT%H:%M:%SZ'
}

log() {
    printf '%s pull-deploy: %s\n' "$(timestamp)" "$*"
}

fail() {
    printf '%s pull-deploy: error: %s\n' "$(timestamp)" "$*" >&2
    exit 1
}

deploy_dir="${GITOPS_DEPLOY_DIR:-}"
if [ -z "$deploy_dir" ]; then
    fail 'GITOPS_DEPLOY_DIR is required and must point at a dedicated deploy clone'
fi

remote="${GITOPS_REMOTE:-https://github.com/jclee941/CLIProxyAPI.git}"
branch="${GITOPS_BRANCH:-main}"
compose_files="${GITOPS_COMPOSE_FILES:-docker-compose.yml deploy/telegram-usage-bot/docker-compose.yml}"
lock_file="${GITOPS_LOCK_FILE:-${deploy_dir%/}.pull-deploy.lock}"

# Re-exec under a non-blocking lock so overlapping cron ticks cannot collide.
# The lock lives outside the deploy clone to keep the clone clean.
if [ -z "${GITOPS_LOCK_HELD:-}" ]; then
    GITOPS_LOCK_HELD=1
    export GITOPS_LOCK_HELD
    flock -n -E 75 "$lock_file" "$0" "$@" && lock_status=0 || lock_status=$?
    if [ "$lock_status" -eq 75 ]; then
        log 'another run holds the lock; skipping this tick'
        exit 0
    fi
    exit "$lock_status"
fi

fresh_clone=0
if [ ! -d "$deploy_dir/.git" ]; then
    log "cloning $remote ($branch) into $deploy_dir"
    mkdir -p "$deploy_dir"
    git clone --branch "$branch" "$remote" "$deploy_dir"
    fresh_clone=1
fi

cd "$deploy_dir"

# Only tracked modifications abort the run. Untracked operator files such as
# .env, state/, and pull-deploy.log are expected to live inside the clone.
if [ -n "$(git status --porcelain --untracked-files=no)" ]; then
    git status --porcelain --untracked-files=no >&2
    fail "$deploy_dir has local changes; the deploy clone must stay clean"
fi

git fetch --quiet "$remote" "$branch"

current_head="$(git rev-parse HEAD)"
target_head="$(git rev-parse FETCH_HEAD)"

if [ "$current_head" = "$target_head" ] && [ "$fresh_clone" -eq 0 ]; then
    log "up to date at $current_head"
    exit 0
fi
# A fresh clone already sits at the target; only an existing clone needs the reset.
if [ "$fresh_clone" -eq 0 ]; then
    log "updating $current_head -> $target_head"
    git checkout --quiet "$branch" 2>/dev/null || git checkout --quiet -B "$branch" FETCH_HEAD
    git reset --hard --quiet FETCH_HEAD
else
    log "initial deployment at $target_head"
fi

# Word splitting is intentional: GITOPS_COMPOSE_FILES is space-separated.
# shellcheck disable=SC2086
for compose_file in $compose_files; do
    if [ ! -f "$compose_file" ]; then
        log "skipping missing compose file $compose_file"
        continue
    fi
    log "applying $compose_file"
    docker compose -f "$compose_file" up -d --build
done

log "converged at $target_head"
