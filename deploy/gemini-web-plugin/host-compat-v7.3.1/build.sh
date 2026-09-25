#!/usr/bin/env bash
set -euo pipefail
umask 077

case "${1:-}" in
  --help)
    printf 'Usage: bash %s\nBuild and test the exact v7.3.1 host patch in fresh /tmp/opencode scratch. No deployment.\n' "$0"
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
toolchain=$(jq -r .go_version "$base/source.json")
goroot=$(GOTOOLCHAIN="$toolchain" go env GOROOT)
test "$("$goroot/bin/go" env GOVERSION)" = "$toolchain"
test "$(gcc -dumpfullversion)" = "$(jq -r .gcc_version "$base/source.json")"
test "$(getconf GNU_LIBC_VERSION)" = "$(jq -r .glibc_version "$base/source.json")"
ld --version | rg -Fx "$(jq -r .binutils_version "$base/source.json")"
work=$(mktemp -d /tmp/opencode/cpa-host-v731-build-XXXXXX)
printf 'Build directory: %s\n' "$work"
export GIT_MASTER=1
git clone --depth 1 --branch "$tag" "$repository" "$work/source"
test "$(git -C "$work/source" rev-parse HEAD)" = "$pin"
test "$(git -C "$work/source" rev-list -n 1 "$tag")" = "$pin"
test "$(git -C "$work/source" rev-parse 'HEAD^{tree}')" = "$(jq -r .tree "$base/source.json")"
git -C "$work/source" archive --format=tar HEAD | sha256sum > "$work/source-archive.sha256"
read -r archive_hash _ < "$work/source-archive.sha256"
test "$archive_hash" = "$(jq -r .git_archive_sha256 "$base/source.json")"
test -z "$(git -C "$work/source" status --porcelain)"

buildenv=(env -i PATH="$goroot/bin:$PATH" HOME="$work" GOPATH="$(go env GOPATH)"
  GOMODCACHE="$(go env GOMODCACHE)" GOCACHE="$(go env GOCACHE)"
  GOENV=off GOTOOLCHAIN=local GOPROXY=off GOSUMDB=off GOFLAGS=-mod=readonly
  GOOS=linux GOARCH=amd64 GOAMD64=v1 CGO_ENABLED=1 CC=gcc)
packages=(./internal/pluginhost ./sdk/auth ./sdk/cliproxy/auth ./sdk/pluginabi
  ./sdk/pluginapi ./internal/watcher/... ./internal/api/handlers/management)
target='TestHostAuth(Save(PreservesDisabled|RejectsInvalidDisabledBeforeWrite)|UpsertPreservesDisableAfterBuild)$'
git -C "$work/source" apply --check "$base/maintained.patch"
git -C "$work/source" apply --include='*_test.go' "$base/maintained.patch"
if "${buildenv[@]}" go -C "$work/source" test -race -count=1 -json -run "$target" ./internal/pluginhost > "$work/red.jsonl" 2>&1; then
  printf 'Regression tests unexpectedly passed on unpatched source\n' >&2
  exit 1
fi
jq -se '[.[] | select(.Action == "fail" and (.Test == "TestHostAuthSavePreservesDisabled" or .Test == "TestHostAuthSaveRejectsInvalidDisabledBeforeWrite" or .Test == "TestHostAuthUpsertPreservesDisableAfterBuild"))] | length == 3' "$work/red.jsonl"
git -C "$work/source" apply --exclude='*_test.go' "$base/maintained.patch"
test -z "$("$goroot/bin/gofmt" -l "$work/source/internal/pluginhost/auth_callbacks.go" "$work/source/internal/pluginhost/auth_callbacks_disabled_test.go")"
git -C "$work/source" diff --check
"${buildenv[@]}" go -C "$work/source" test -race -count=1 -json -run "$target" ./internal/pluginhost > "$work/green.jsonl" 2>&1
"${buildenv[@]}" go -C "$work/source" test -race -shuffle=on -count=1 "${packages[@]}" 2>&1 | tee "$work/related-race.log"
"${buildenv[@]}" go -C "$work/source" vet "${packages[@]}" 2>&1 | tee "$work/vet.log"
"${buildenv[@]}" go -C "$work/source" build -trimpath -buildvcs=false \
  -ldflags="-s -w -X main.Version=$version -X main.Commit=$pin-local-patched -X main.BuildDate=$build_date" \
  -o "$work/CLIProxyAPI-host-compat" ./cmd/server
sha256sum "$work/source/go.mod" "$work/source/go.sum" "$work/source/LICENSE" "$work/source/internal/pluginhost/auth_callbacks.go" "$work/source/internal/pluginhost/auth_callbacks_disabled_test.go" > "$work/source-files.sha256"
sha256sum "$base/maintained.patch" "$base/source.json" "$work/CLIProxyAPI-host-compat" > "$work/artifacts.sha256"
"${buildenv[@]}" go version -m "$work/CLIProxyAPI-host-compat" > "$work/build-info.txt"
printf 'Verified local binary: %s/CLIProxyAPI-host-compat\n' "$work"
