#!/bin/sh
set -eu
unset GEMINI_MAINTENANCE_KEY
exec python3 "$(dirname "$0")/maintain.py" "$@"
