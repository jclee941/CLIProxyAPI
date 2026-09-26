#!/bin/sh
set -eu
umask 077
IFS= read -r GEMINI_WEB_SESSION_KEY < /run/gemini-web/session.key
export GEMINI_WEB_SESSION_KEY
exec /CLIProxyAPI/CLIProxyAPI
