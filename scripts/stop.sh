#!/usr/bin/env bash
set -euo pipefail

adb root

adb shell "su -c '/system/bin/kokoni_launcher stop'" || true
adb forward --remove tcp:18080 2>/dev/null || true

echo "=== launcher status ==="
adb shell "su -c '/system/bin/kokoni_launcher status'" || true

echo "=== ps ==="
adb shell "ps | grep kokoni || true"
