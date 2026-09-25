#!/bin/sh
set -eu
umask 077
IFS= read -r GEMINI_WEB_SESSION_KEY < /bootstrap/session.key
IFS= read -r MANAGEMENT_PASSWORD < /bootstrap/management.key
export GEMINI_WEB_SESSION_KEY MANAGEMENT_PASSWORD
export PATH=/stage/empty-bin
exec /stage/core --config /stage/config.yaml --local-model
