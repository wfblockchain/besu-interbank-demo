#!/usr/bin/env bash
# run.sh — build the three Go services and run the interbank demo.
# Requires the Besu chain to be up and contracts deployed (see README Quickstart).
set -euo pipefail
cd "$(dirname "$0")"

echo "→ building services…"
go build -o bin/ ./cmd/...

echo "→ running demo…"
exec ./bin/demo
