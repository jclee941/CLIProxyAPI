#!/bin/sh
set -eu
umask 077

set -a
. /etc/cliproxy/op-service-account.env
set +a

install -d -m 700 /run/cliproxy-manager-plus
op inject --in-file=/opt/cpa-manager-plus/admin-key.tpl --out-file=/run/cliproxy-manager-plus/cpa_admin_key.next --force
op inject --in-file=/opt/cpa-manager-plus/data-key.tpl --out-file=/run/cliproxy-manager-plus/data.key.next --force
chmod 600 /run/cliproxy-manager-plus/cpa_admin_key.next /run/cliproxy-manager-plus/data.key.next
test -s /run/cliproxy-manager-plus/cpa_admin_key.next
test -s /run/cliproxy-manager-plus/data.key.next
mv -f /run/cliproxy-manager-plus/cpa_admin_key.next /run/cliproxy-manager-plus/cpa_admin_key
mv -f /run/cliproxy-manager-plus/data.key.next /run/cliproxy-manager-plus/data.key
exec docker compose -f /opt/cpa-manager-plus/compose.yml --profile approval-required up -d --no-deps --force-recreate cpa-manager-plus
