#!/usr/bin/env bash
set -euo pipefail

CGO_ENABLED=0 GOOS=linux GOARCH=arm GOARM=7 \
  go build -ldflags="-s -w" -o kokoni_web ./cmd/kokoni-agent
