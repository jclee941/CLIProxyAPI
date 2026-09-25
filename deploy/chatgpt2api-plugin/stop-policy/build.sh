#!/usr/bin/env bash
set -euo pipefail
umask 077
case "${1:-}" in
  --help) printf 'Usage: bash %s\nBuilds pinned CPA + existing disabled-state patch + terminal STOP patch. No deployment.\n' "$0"; exit 0 ;;
  '') ;;
  *) printf 'Unexpected argument\n' >&2; exit 2 ;;
esac
base=$(dirname "$(readlink -f "$0")")
host=$(readlink -f "$base/../../gemini-web-plugin/host-compat")
pin=$(jq -r .commit "$host/source.json")
test "$(go env GOVERSION)" = "$(jq -r .go_version "$host/source.json")"
test "$(gcc -dumpfullversion)" = "$(jq -r .gcc_version "$host/source.json")"
test "$(getconf GNU_LIBC_VERSION)" = "$(jq -r .glibc_version "$host/source.json")"
work=$(mktemp -d /tmp/opencode/cpa-stop-build-XXXXXX)
printf 'Build directory: %s\n' "$work"
GIT_MASTER=1 git clone --depth 1 --branch "$(jq -r .tag "$host/source.json")" "$(jq -r .repository "$host/source.json")" "$work/source"
test "$(GIT_MASTER=1 git -C "$work/source" rev-parse HEAD)" = "$pin"
test "$(GIT_MASTER=1 git -C "$work/source" rev-parse 'HEAD^{tree}')" = "$(jq -r .tree "$host/source.json")"
GIT_MASTER=1 git -C "$work/source" archive --format=tar HEAD | sha256sum > "$work/source-archive.sha256"
read -r archive_hash _ < "$work/source-archive.sha256"
test "$archive_hash" = "$(jq -r .git_archive_sha256 "$host/source.json")"
buildenv=(env -i PATH="$PATH" HOME="$work" GOPATH="$(go env GOPATH)" GOMODCACHE="$(go env GOMODCACHE)" GOCACHE="$(go env GOCACHE)" GOENV=off GOTOOLCHAIN=local GOPROXY=off GOSUMDB=off GOFLAGS=-mod=readonly GOOS=linux GOARCH=amd64 GOAMD64=v1 CGO_ENABLED=1 CC=gcc)
(
  cd "$work/source"
  GIT_MASTER=1 git apply --check "$host/maintained.patch"
  GIT_MASTER=1 git apply "$host/maintained.patch"
  GIT_MASTER=1 git apply --check "$base/maintained.patch"
  GIT_MASTER=1 git apply --include='*_test.go' "$base/maintained.patch"
  if "${buildenv[@]}" go test -race -count=1 -json -run 'TestStop(Boundary|Header)' ./sdk/cliproxy/auth ./sdk/api/handlers > "$work/red.jsonl" 2>&1; then
    printf 'STOP regressions unexpectedly passed without source patch\n' >&2; exit 1
  fi
  jq -se '[.[] | select(.Action == "fail" and (.Test == "TestStopBoundaryPreservesTerminalMarker" or .Test == "TestStopHeaderOverridesPassthrough"))] | length == 2' "$work/red.jsonl"
  GIT_MASTER=1 git apply --exclude='*_test.go' "$base/maintained.patch"
  test -z "$(gofmt -l sdk/cliproxy/auth/conductor_stop_boundary_test.go sdk/cliproxy/auth/conductor_request_scoped_errors.go sdk/cliproxy/auth/conductor_execution.go sdk/api/handlers/handlers_stop_test.go sdk/api/handlers/handlers_errors.go)"
  GIT_MASTER=1 git diff --check
  packages=(./sdk/cliproxy/auth ./sdk/api/handlers ./sdk/api/handlers/openai ./internal/pluginhost ./internal/watcher/... ./internal/config ./sdk/auth ./sdk/pluginabi ./sdk/pluginapi ./internal/api/handlers/management)
  "${buildenv[@]}" go test -race -shuffle=on -count=1 "${packages[@]}" | tee "$work/green.log"
  "${buildenv[@]}" go test -race -count=1 -run 'TestOpenAICompat(RetryAfter|ExecutorPropagatesRetryAfter)$' ./internal/runtime/executor | tee "$work/executor-retry.log"
  "${buildenv[@]}" go vet "${packages[@]}" | tee "$work/vet.log"
  "${buildenv[@]}" go build -trimpath -buildvcs=false -ldflags="-s -w -X main.Version=v7.2.159-compatible-host-auth-save-stop.1 -X main.Commit=$pin-local-patched -X main.BuildDate=2026-09-14T00:00:00Z" -o "$work/CLIProxyAPI-host-stop" ./cmd/server
  sha256sum "$work/CLIProxyAPI-host-stop" "$base/maintained.patch" "$host/maintained.patch" "$host/source.json" > "$work/artifacts.sha256"
  "${buildenv[@]}" go version -m "$work/CLIProxyAPI-host-stop" > "$work/build-info.txt"
)
printf 'Verified local binary: %s/CLIProxyAPI-host-stop\n' "$work"
