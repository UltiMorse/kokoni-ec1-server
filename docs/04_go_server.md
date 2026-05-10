# Go言語による実装 - KOKONI EC1 Webサーバー

Goによる実装はmain.goに記載

UARTも使用した。
uart.goには

起動時にやる

adb root
adb shell "setenforce 0"

ビルド ・kokoni_webという名前のバイナリで出力

CGO_ENABLED=0 GOOS=linux GOARCH=arm GOARM=7 go build -ldflags="-s -w" -o kokoni_web main.go

書き込み

adb push kokoni_web /data/local/tmp/

移動

adb shell "su -c 'mount -o rw,remount /system && cat /data/local/tmp/kokoni_web > /system/bin/kokoni_web && chmod 755 /system/bin/kokoni_web && chcon u:object_r:system_file:s0 /system/bin/kokoni_web && mount -o ro,remount /system'"

実行
adb shell "su -c 'kokoni_web'"
