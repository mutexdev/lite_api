#!/usr/bin/env bash
# Wrapper so this gate is invoked like the others. See qa/mutations.py.
set -euo pipefail
exec python3 "$(dirname "$0")/mutations.py" "$@"
