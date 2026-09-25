#!/bin/sh
set -eu
umask 027

config_dir=${CLIPROXY_CONFIG_DIR:-/etc/cliproxy}
runtime_dir=${GEMINI_WEB2API_RUN_DIR:-/run/gemini-web2api}
for source in "$config_dir/gemini-web2api/config.json" "$config_dir/gemini-web2api/cookie.txt"; do
    if [ ! -f "$source" ] || [ ! -r "$source" ] || [ ! -s "$source" ]; then
        printf 'Required local configuration is missing, unreadable or empty: %s\n' "$source" >&2
        exit 1
    fi
done

install -d -m 750 -o root -g 65532 "$runtime_dir"
install -m 640 -o root -g 65532 "$config_dir/gemini-web2api/config.json" "$runtime_dir/config.json.next"
install -m 640 -o root -g 65532 "$config_dir/gemini-web2api/cookie.txt" "$runtime_dir/cookie.txt.next"
mv -f "$runtime_dir/cookie.txt.next" "$runtime_dir/cookie.txt"
mv -f "$runtime_dir/config.json.next" "$runtime_dir/config.json"
exec docker compose -f /opt/gemini-web2api/docker-compose.yml up -d --force-recreate gemini-web2api
