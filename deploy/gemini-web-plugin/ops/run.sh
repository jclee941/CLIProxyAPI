#!/bin/sh
set -eu
export GEMINI_MAINTENANCE_KEY=op://homelab/hazdy6l5k5rfahjdcdszzenizm/management_key
exec /usr/bin/op run -- /usr/bin/python3 /opt/gemini-web-plugin/ops/maintain.py "$@"
