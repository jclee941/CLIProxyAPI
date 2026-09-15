#!/usr/bin/env bash
set -euo pipefail

readonly source_url=https://github.com/seakee/CPA-Manager-Plus.git
# patch_base is the revision the patch was authored against. Its blobs are
# fetched alongside the build revision so that a patch which no longer applies
# cleanly to a newer release can still be merged three-way instead of failing.
readonly patch_base=e1a8788ab796f4d001c5d1e9851c418989b05424
readonly build_suffix=geminiweb.1
readonly patch_name=0001-gemini-web-manager.patch
package_dir=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)

if [[ $# != 1 || "$1" != /* || -e "$1" ]]; then
  printf '%s\n' 'Usage: bash build-patched.sh /absolute/new-build-directory' >&2
  printf '%s\n' 'The directory must not exist. No existing checkout is modified.' >&2
  printf '%s\n' 'Set SOURCE_REF to a tag, branch or commit; it defaults to the newest upstream release tag.' >&2
  exit 2
fi
if [[ ! -d "$(dirname -- "$1")" ]]; then
  printf '%s\n' 'The build parent directory must already exist.' >&2
  exit 2
fi
readonly build_dir=$1
readonly vendor=$build_dir/vendor
export GIT_MASTER=1

source_ref=${SOURCE_REF:-}
if [[ -z "$source_ref" ]]; then
  source_ref=$(git ls-remote --tags --refs "$source_url" \
    | awk '{print $2}' | sed 's#refs/tags/##' \
    | grep -E '^v[0-9]+(\.[0-9]+)*$' | sort -V | tail -1)
fi
if [[ -z "$source_ref" ]]; then
  printf '%s\n' 'Could not resolve an upstream release tag. Set SOURCE_REF explicitly.' >&2
  exit 1
fi
readonly source_ref

mkdir -- "$build_dir" "$vendor" "$build_dir/artifacts"
git -C "$vendor" init -q
git -C "$vendor" fetch -q --depth=1 "$source_url" "$source_ref"
git -C "$vendor" -c advice.detachedHead=false checkout -q --detach FETCH_HEAD
source_commit=$(git -C "$vendor" rev-parse HEAD)
readonly source_commit
# The base tree is only needed as three-way merge input, so it is fetched
# without disturbing the checked-out build revision.
git -C "$vendor" fetch -q --depth=1 "$source_url" "$patch_base" || true

version=${source_ref}-${build_suffix}
if [[ ! "$source_ref" =~ ^v[0-9] ]]; then
  version=${source_commit:0:12}-${build_suffix}
fi
readonly version

if git -C "$vendor" apply --check "$package_dir/patches/$patch_name" 2>/dev/null; then
  git -C "$vendor" apply "$package_dir/patches/$patch_name"
  apply_mode=clean
elif git -C "$vendor" apply --3way "$package_dir/patches/$patch_name"; then
  apply_mode=three-way
  printf '%s\n' "Patch applied three-way against $patch_base; review the merge before trusting the build." >&2
else
  printf '%s\n' "Patch does not apply to $source_ref ($source_commit)." >&2
  printf '%s\n' 'Rebase patches/'"$patch_name"' onto that revision, or set SOURCE_REF to a revision it fits.' >&2
  # The failed three-way leaves partly written files behind, so the tree is
  # restored first; a diagnostic run against those leftovers would report the
  # patch's own new files as pre-existing and hide the real mismatch.
  git -C "$vendor" reset -q --hard HEAD
  git -C "$vendor" apply --check -v "$package_dir/patches/$patch_name" >&2 || true
  exit 1
fi
readonly apply_mode
if ! git -C "$vendor" diff --quiet --diff-filter=U; then
  printf '%s\n' 'The three-way merge left conflicts; resolve them in the patch rather than in the build.' >&2
  git -C "$vendor" diff --name-only --diff-filter=U >&2
  exit 1
fi

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
cp -- "$package_dir/patches/$patch_name" "$build_dir/artifacts/$patch_name"
printf 'repository=%s\nsource_ref=%s\nsource_commit=%s\npatch_base=%s\napply_mode=%s\nversion=%s\npatch=%s\n' \
  "$source_url" "$source_ref" "$source_commit" "$patch_base" "$apply_mode" "$version" "$patch_name" \
  > "$build_dir/artifacts/SOURCE"
(
  cd -- "$build_dir/artifacts"
  sha256sum management.html LICENSE "$patch_name" SOURCE > SHA256SUMS
)
printf 'Ref: %s\nCommit: %s\nApply: %s\nVersion: %s\nVendor: %s\nArtifacts: %s/artifacts\n' \
  "$source_ref" "$source_commit" "$apply_mode" "$version" "$vendor" "$build_dir"
printf '%s\n' 'Build only. No image is installed, no service is restarted, and no runtime configuration is changed.'
