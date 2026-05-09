#!/usr/bin/env bash
set -euo pipefail

adb root
adb shell "setenforce 0" || true

adb shell "su -c 'mkdir -p /data/local/kokoni_agent/jobs/uploaded'"
adb shell "su -c 'chmod -R 777 /data/local/kokoni_agent'"

echo "=== start launcher ==="
adb shell "su -c '/system/bin/kokoni_launcher start'"

adb forward --remove tcp:18080 2>/dev/null || true
adb forward tcp:18080 tcp:8080

echo "=== launcher status ==="
adb shell "su -c '/system/bin/kokoni_launcher status'"

echo "=== ps ==="
adb shell "ps | grep kokoni || true"

echo "=== status ==="
curl -s http://127.0.0.1:18080/api/status || true
echo
