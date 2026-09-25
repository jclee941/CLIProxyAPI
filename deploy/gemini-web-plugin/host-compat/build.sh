#!/usr/bin/env bash
set -euo pipefail
umask 077

case "${1:-}" in
  --help)
    printf 'Usage: bash %s\nBuilds and tests the pinned host patch in a new /tmp/opencode checkout. No deployment.\n' "$0"
    exit 0
    ;;
  '') ;;
  *) printf 'Unexpected argument: %s\n' "$1" >&2; exit 2 ;;
esac

base=$(dirname "$(readlink -f "$0")")
pin=$(jq -r .commit "$base/source.json")
tag=$(jq -r .tag "$base/source.json")
repository=$(jq -r .repository "$base/source.json")
version=$(jq -r .version "$base/source.json")
build_date=$(jq -r .build_date "$base/source.json")
test "$(go env GOVERSION)" = "$(jq -r .go_version "$base/source.json")"
test "$(gcc -dumpfullversion)" = "$(jq -r .gcc_version "$base/source.json")"
test "$(getconf GNU_LIBC_VERSION)" = "$(jq -r .glibc_version "$base/source.json")"
ld --version | rg -Fx "$(jq -r .binutils_version "$base/source.json")"
work=$(mktemp -d /tmp/opencode/cpa-host-compat-build-XXXXXX)
printf 'Build directory: %s\n' "$work"
export GIT_MASTER=1
git clone --depth 1 --branch "$tag" "$repository" "$work/source"
test "$(git -C "$work/source" rev-parse HEAD)" = "$pin"
test "$(git -C "$work/source" rev-parse 'HEAD^{tree}')" = "$(jq -r .tree "$base/source.json")"
git -C "$work/source" archive --format=tar HEAD | sha256sum > "$work/source-archive.sha256"
read -r archive_hash _ < "$work/source-archive.sha256"
test "$archive_hash" = "$(jq -r .git_archive_sha256 "$base/source.json")"

buildenv=(env -i PATH="$PATH" HOME="$work" GOPATH="$(go env GOPATH)"
  GOMODCACHE="$(go env GOMODCACHE)" GOCACHE="$(go env GOCACHE)"
  GOENV=off GOTOOLCHAIN=local GOPROXY=off GOSUMDB=off GOFLAGS=-mod=readonly
  GOOS=linux GOARCH=amd64 GOAMD64=v1 CGO_ENABLED=1 CC=gcc)
packages=(./internal/pluginhost ./sdk/auth ./sdk/cliproxy/auth ./sdk/pluginabi
  ./sdk/pluginapi ./internal/watcher/... ./internal/api/handlers/management)
target='TestHostAuth(Save(PreservesDisabled|RejectsInvalidDisabledBeforeWrite)|UpsertPreservesDisableAfterBuild)$'
(
  cd "$work/source"
  git apply --check "$base/maintained.patch"
  git apply --include='*_test.go' "$base/maintained.patch"
  if "${buildenv[@]}" go test -race -count=1 -json -run "$target" ./internal/pluginhost > "$work/red.jsonl" 2>&1; then
    printf 'Regression tests unexpectedly passed on unpatched source\n' >&2
    exit 1
  fi
  jq -se '[.[] | select(.Action == "fail" and (.Test == "TestHostAuthSavePreservesDisabled" or .Test == "TestHostAuthSaveRejectsInvalidDisabledBeforeWrite" or .Test == "TestHostAuthUpsertPreservesDisableAfterBuild"))] | length == 3' "$work/red.jsonl"
  git apply --exclude='*_test.go' "$base/maintained.patch"
  test -z "$(gofmt -l internal/pluginhost/auth_callbacks.go internal/pluginhost/auth_callbacks_disabled_test.go)"
  git diff --check
  "${buildenv[@]}" go test -race -count=1 -v -run "$target" ./internal/pluginhost | tee "$work/green.log"
  "${buildenv[@]}" go test -race -shuffle=on -count=1 "${packages[@]}" | tee "$work/related-race.log"
  "${buildenv[@]}" go vet "${packages[@]}" 2>&1 | tee "$work/vet.log"
  "${buildenv[@]}" go build -trimpath -buildvcs=false \
    -ldflags="-s -w -X main.Version=$version -X main.Commit=$pin-local-patched -X main.BuildDate=$build_date" \
    -o "$work/CLIProxyAPI-host-compat" ./cmd/server
  sha256sum go.mod go.sum LICENSE internal/pluginhost/auth_callbacks.go internal/pluginhost/auth_callbacks_disabled_test.go > "$work/source-files.sha256"
  sha256sum "$base/maintained.patch" "$base/source.json" "$work/CLIProxyAPI-host-compat" > "$work/artifacts.sha256"
  "${buildenv[@]}" go version -m "$work/CLIProxyAPI-host-compat" > "$work/build-info.txt"
)
printf 'Verified local binary: %s/CLIProxyAPI-host-compat\n' "$work"
