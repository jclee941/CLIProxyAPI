#!/usr/bin/env bash
set -euo pipefail

readonly source_commit=e1a8788ab796f4d001c5d1e9851c418989b05424
readonly source_url=https://github.com/seakee/CPA-Manager-Plus.git
readonly version=v1.12.11-geminiweb.1
package_dir=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)

if [[ $# != 1 || "$1" != /* || -e "$1" ]]; then
  printf '%s\n' 'Usage: bash build-patched.sh /absolute/new-build-directory' >&2
  printf '%s\n' 'The directory must not exist. No existing checkout is modified.' >&2
  exit 2
fi
if [[ ! -d "$(dirname -- "$1")" ]]; then
  printf '%s\n' 'The build parent directory must already exist.' >&2
  exit 2
fi
readonly build_dir=$1
readonly vendor=$build_dir/vendor
export GIT_MASTER=1

mkdir -- "$build_dir" "$vendor" "$build_dir/artifacts"
git -C "$vendor" init -q
git -C "$vendor" fetch -q --depth=1 "$source_url" "$source_commit"
git -C "$vendor" -c advice.detachedHead=false checkout -q --detach FETCH_HEAD
test "$(git -C "$vendor" rev-parse HEAD)" = "$source_commit"
git -C "$vendor" apply --check "$package_dir/patches/0001-gemini-web-manager.patch"
git -C "$vendor" apply "$package_dir/patches/0001-gemini-web-manager.patch"

(
  cd -- "$vendor"
  npm ci --no-audit --no-fund
  npm run type-check
  npm exec --workspace apps/web -- vitest run
  VERSION="$version" npm run build
  npm run check:demo-isolation
)

cp -- "$vendor/apps/web/dist/index.html" "$build_dir/artifacts/management.html"
cp -- "$vendor/LICENSE" "$build_dir/artifacts/LICENSE"
cp -- "$package_dir/patches/0001-gemini-web-manager.patch" "$build_dir/artifacts/0001-gemini-web-manager.patch"
printf 'repository=%s\nsource_commit=%s\nversion=%s\npatch=0001-gemini-web-manager.patch\n' \
  "$source_url" "$source_commit" "$version" > "$build_dir/artifacts/SOURCE"
(
  cd -- "$build_dir/artifacts"
  sha256sum management.html LICENSE 0001-gemini-web-manager.patch SOURCE > SHA256SUMS
)
printf 'Source: %s\nVersion: %s\nVendor: %s\nArtifacts: %s/artifacts\n' \
  "$source_commit" "$version" "$vendor" "$build_dir"
printf '%s\n' 'Build only. No image is installed, no service is restarted, and no runtime configuration is changed.'
