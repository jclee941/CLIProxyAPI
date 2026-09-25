#!/bin/sh
set -eu
umask 077

key_dir=${CPA_MANAGER_KEY_DIR:-/etc/cliproxy-manager-plus}
runtime_dir=${CPA_MANAGER_RUNTIME_DIR:-/run/cliproxy-manager-plus}

for key in cpa_admin_key data.key; do
    if [ ! -f "$key_dir/$key" ] || [ ! -s "$key_dir/$key" ]; then
        printf 'Missing or empty CPA Manager key file: %s\n' "$key_dir/$key" >&2
        exit 1
    fi
done

install -d -m 700 "$runtime_dir"
install -m 600 "$key_dir/cpa_admin_key" "$runtime_dir/cpa_admin_key.next"
install -m 600 "$key_dir/data.key" "$runtime_dir/data.key.next"
test -s "$runtime_dir/cpa_admin_key.next"
test -s "$runtime_dir/data.key.next"
mv -f "$runtime_dir/cpa_admin_key.next" "$runtime_dir/cpa_admin_key"
mv -f "$runtime_dir/data.key.next" "$runtime_dir/data.key"
exec docker compose -f /opt/cpa-manager-plus/compose.yml --profile approval-required up -d --no-deps --force-recreate cpa-manager-plus
