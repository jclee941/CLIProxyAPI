#!/bin/sh
set -eu
umask 027

config_dir=${CLIPROXY_CONFIG_DIR:-/etc/cliproxy}
runtime_dir=${GEMINI_WEB_NATIVE_RUN_DIR:-/run/gemini-web-native}
source=$config_dir/gemini-web-native/config.json
if [ ! -f "$source" ] || [ ! -r "$source" ] || [ ! -s "$source" ]; then
    printf 'Required local configuration is missing, unreadable or empty: %s\n' "$source" >&2
    exit 1
fi

install -d -m 750 -o root -g 65532 "$runtime_dir"
install -d -m 700 -o 65532 -g 65532 "$runtime_dir/cookies"
install -m 640 -o root -g 65532 "$source" "$runtime_dir/config.json"
exec docker compose -f /opt/gemini-web-native/docker-compose.yml up -d --force-recreate gemini-web-native
