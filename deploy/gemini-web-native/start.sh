#!/bin/sh
set -eu
umask 027

set -a
. /etc/cliproxy/op-service-account.env
set +a

install -d -m 750 -o root -g 65532 /run/gemini-web-native
install -d -m 700 -o 65532 -g 65532 /run/gemini-web-native/cookies
op inject --in-file=/opt/gemini-web-native/config.json.tpl --out-file=/run/gemini-web-native/config.json --force
chown root:65532 /run/gemini-web-native/config.json
chmod 640 /run/gemini-web-native/config.json
exec docker compose -f /opt/gemini-web-native/docker-compose.yml up -d --force-recreate gemini-web-native
