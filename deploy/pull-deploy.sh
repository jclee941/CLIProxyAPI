#!/bin/sh
# GitOps converger for the CLIProxyAPI fork.
#
# Run explicitly on the deployment host (see deploy/README.md). It pulls the
# configured branch into a dedicated deploy clone, then converges the CPA core
# and native plugins with deploy/cpa-converge.py on every run.
#
# Environment:
#   GITOPS_DEPLOY_DIR      Required. Dedicated clone used ONLY for deployment.
#                          Never point this at a developer working tree.
#   GITOPS_REMOTE          Git remote URL. Default: fork origin.
#   GITOPS_BRANCH          Branch to converge on. Default: master.
#   GITOPS_LOCK_FILE       Lock file path. Default: sibling of the deploy dir.
#   GITOPS_CPA_MANIFEST    CPA manifest, relative to the deploy dir.
#                          Default: deploy/cpa-plugins.json.

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

# The CPA core and native plugins converge through their own reconciler on
# every run, because a swap that has to wait for an idle host outlives the run
# that first saw it.
converge_cpa() {
    python3 deploy/cpa-converge.py "$cpa_manifest"
}

deploy_dir="${GITOPS_DEPLOY_DIR:-}"
if [ -z "$deploy_dir" ]; then
    fail 'GITOPS_DEPLOY_DIR is required and must point at a dedicated deploy clone'
fi

remote="${GITOPS_REMOTE:-https://github.com/jclee941/CLIProxyAPI.git}"
branch="${GITOPS_BRANCH:-master}"
cpa_manifest="${GITOPS_CPA_MANIFEST:-deploy/cpa-plugins.json}"
lock_file="${GITOPS_LOCK_FILE:-${deploy_dir%/}.pull-deploy.lock}"

# Re-exec under a non-blocking lock so overlapping runs cannot collide.
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
    converge_cpa
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

converge_cpa
log "deploy clone at $target_head; cpa-converge above reports whether the host converged"
