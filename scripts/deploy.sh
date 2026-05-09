#!/usr/bin/env bash
set -euo pipefail

adb root
adb shell "setenforce 0" || true

adb push kokoni_web /data/local/tmp/kokoni_web

adb shell "su -c 'pkill kokoni_web 2>/dev/null || true'"
sleep 1

adb shell "su -c 'mount -o rw,remount /system && \
cat /data/local/tmp/kokoni_web > /system/bin/kokoni_web && \
chmod 755 /system/bin/kokoni_web && \
chcon u:object_r:system_file:s0 /system/bin/kokoni_web && \
mkdir -p /data/local/kokoni_agent/jobs/uploaded && \
chmod -R 777 /data/local/kokoni_agent && \
mount -o ro,remount /system'"
