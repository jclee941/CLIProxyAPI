#!/usr/bin/env bash
set -euo pipefail
umask 077
case "${1:-}" in
  --help) printf 'Usage: bash %s CORE_BINARY\nRuns synthetic offline Docker request-count tests in a unique directory.\n' "$0"; exit 0 ;;
  '') printf 'CORE_BINARY required\n' >&2; exit 2 ;;
esac
test "$#" = 1
core=$(readlink -f "$1")
test -f "$core"
base=$(dirname "$(readlink -f "$0")")
work=$(mktemp -d /tmp/opencode/cpa-stop-fixture-XXXXXX)
printf 'Fixture directory: %s\n' "$work"
cp "$base"/{package.json,bun.lock,tsconfig.json,pyrightconfig.json,provider-fragment.yaml,*.ts,python-client.py} "$work/"
cp "$core" "$work/core"
cp "$(command -v bun)" "$work/bun"
managed=$(uv python find --managed-python)
runtime=$(dirname "$(dirname "$managed")")
uv venv --python "$managed" "$work/venv"
uv pip install --python "$work/venv/bin/python" -r "$base/python-dependencies.txt"
bun install --cwd "$work" --ignore-scripts --frozen-lockfile
"$work/bun" "$work/node_modules/typescript/bin/tsc" --project "$work/tsconfig.json"
uv pip freeze --python "$work/venv/bin/python" > "$work/python-packages.txt"
sha256sum "$work/core" "$work/bun" "$work/bun.lock" > "$work/artifacts.sha256"
name="cpa-stop-$(basename "$work")"
cleanup() { docker rm -fv "$name" > "$work/cleanup.log" 2>&1 || true; }
trap cleanup EXIT
docker create --pull never --name "$name" --network none --dns 127.0.0.1 --cap-drop ALL --security-opt no-new-privileges --read-only --user "$(id -u):$(id -g)" \
  --mount "type=bind,src=$work,dst=$work" --mount "type=bind,src=$runtime,dst=$runtime,readonly" \
  --mount "type=bind,src=$work,dst=/var/lib/postgresql/data" --workdir "$work" \
  --env "HOME=$work" --env "TMPDIR=$work" --env PYTHONDONTWRITEBYTECODE=1 \
  --entrypoint "$work/bun" postgres@sha256:f1c3376c26f2609ab9f29f71f824103fe2fcd8ee0346485cb6122a4f93df6f94 \
  "$work/fixture.ts" "$work/core" "$work/results" "$work/venv/bin/python" > "$work/container-id.txt"
docker inspect --format '{{json .HostConfig}}' "$name" > "$work/isolation.json"
docker start -a "$name" > "$work/console.log" 2>&1 || true
status=$(docker inspect --format '{{.State.ExitCode}}' "$name")
printf 'Fixture exit=%s report=%s/results/report.json\n' "$status" "$work"
exit "$status"
