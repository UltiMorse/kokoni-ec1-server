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
  echo "Wi-Fi ADB not ready. Falling back to default adb device."
fi

cd "$HOME/kokoni-ec1-server"
./scripts/run.sh

echo "Waiting for kokoni agent..."
for i in $(seq 1 30); do
  if curl -fsS "$AGENT_URL" >/dev/null 2>&1; then
    echo "kokoni agent is ready."
    break
  fi

  echo "Waiting for agent API... ($i/30)"
  sleep 1
done

cd "$HOME/kokoni-ec1-desktop"
exec ./build/bin/kokoni-ec1-desktop
