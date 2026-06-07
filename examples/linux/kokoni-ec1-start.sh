#!/usr/bin/env bash
set -euo pipefail

KOKONI_ADB="192.168.11.25:5555"
AGENT_URL="http://127.0.0.1:18080/api/status"

echo "Connecting Wi-Fi ADB: $KOKONI_ADB"

ADB_READY=0

for i in $(seq 1 20); do
  adb connect "$KOKONI_ADB" >/dev/null 2>&1 || true

  if adb -s "$KOKONI_ADB" get-state 2>/dev/null | grep -q '^device$'; then
    ADB_READY=1
    break
  fi

  echo "Waiting for Wi-Fi ADB... ($i/20)"
  sleep 1
done

if [ "$ADB_READY" -eq 1 ]; then
  export ANDROID_SERIAL="$KOKONI_ADB"
  echo "Using Wi-Fi ADB: $ANDROID_SERIAL"
else
  unset ANDROID_SERIAL
  echo "Wi-Fi ADB not ready: $KOKONI_ADB" >&2
  exit 1
fi

cd "$HOME/kokoni-ec1-server"
./scripts/run.sh

echo "Waiting for kokoni agent and printer..."

READY=0

for i in $(seq 1 60); do
  STATUS="$(curl -fsS "$AGENT_URL" 2>/dev/null || true)"

  if echo "$STATUS" | grep -q '"agent":"running"' \
    && echo "$STATUS" | grep -q '"printer":"ready"' \
    && echo "$STATUS" | grep -q '"uart_connected":true'; then
    echo "kokoni printer is ready."
    READY=1
    break
  fi

  echo "Waiting for printer ready... ($i/60)"
  if [ -n "$STATUS" ]; then
    echo "$STATUS"
  fi

  sleep 1
done

if [ "$READY" -ne 1 ]; then
  echo "kokoni printer did not become ready." >&2

  echo "=== status ==="
  curl -s "$AGENT_URL" || echo "curl failed"
  echo

  echo "=== launcher status ==="
  adb -s "$KOKONI_ADB" shell "su -c '/system/bin/kokoni_launcher status'" || true

  echo "=== ps ==="
  adb -s "$KOKONI_ADB" shell "ps | grep kokoni || true" || true

  echo "=== forward ==="
  adb -s "$KOKONI_ADB" forward --list || true

  exit 1
fi

cd "$HOME/kokoni-ec1-desktop"
exec ./build/bin/kokoni-ec1-desktop
