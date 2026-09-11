#!/bin/sh
set -eu
umask 027

set -a
. /etc/cliproxy/op-service-account.env
set +a

install -d -m 750 -o root -g 65532 /run/gemini-web2api
op inject --in-file=/opt/gemini-web2api/config.json.tpl --out-file=/run/gemini-web2api/config.json.next --force
op inject --in-file=/opt/gemini-web2api/cookie.txt.tpl --out-file=/run/gemini-web2api/cookie.txt.next --force
chown root:65532 /run/gemini-web2api/config.json.next /run/gemini-web2api/cookie.txt.next
chmod 640 /run/gemini-web2api/config.json.next /run/gemini-web2api/cookie.txt.next
mv -f /run/gemini-web2api/cookie.txt.next /run/gemini-web2api/cookie.txt
mv -f /run/gemini-web2api/config.json.next /run/gemini-web2api/config.json
exec docker compose -f /opt/gemini-web2api/docker-compose.yml up -d --force-recreate gemini-web2api
