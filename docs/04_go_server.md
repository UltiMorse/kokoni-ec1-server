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


初回セットアップ時やランチャーのコードを変更した場合は、ランチャーも手動でビルドします:

```bash
CGO_ENABLED=0 GOOS=linux GOARCH=arm GOARM=7 \
  go build -ldflags="-s -w" -o kokoni_launcher ./cmd/kokoni-launcher
```

ランチャーを手動でインストールする場合:

```bash
adb root
adb shell "setenforce 0" || true
adb push kokoni_launcher /data/local/tmp/kokoni_launcher

adb shell "su -c 'mount -o rw,remount /system && \
cat /data/local/tmp/kokoni_launcher > /system/bin/kokoni_launcher && \
chmod 755 /system/bin/kokoni_launcher && \
chcon u:object_r:system_file:s0 /system/bin/kokoni_launcher && \
mount -o ro,remount /system'"
```

## HTTP API

### プリンター

UARTの初期化:

```bash
curl -X POST http://127.0.0.1:18080/api/init
```

手動コマンドの送信:

```bash
curl -X POST "http://127.0.0.1:18080/api/send?cmd=M355%20S255"
```

※ジョブがアクティブな間は手動送信はブロックされます。

### ライト

ライトON:

```bash
curl -X POST "http://127.0.0.1:18080/api/light?value=255"
```

ライトOFF:

```bash
curl -X POST "http://127.0.0.1:18080/api/light?value=0"
```

印刷中、ライト制御コマンドはキューに入れられ、安全な行の境界で挿入されます。

### ジョブ

`.gcode`のアップロード:

```bash
curl -F "gcode=@/path/to/file.gcode" http://127.0.0.1:18080/api/job/upload
```

開始:

```bash
curl -X POST http://127.0.0.1:18080/api/job/start
```

一時停止:

```bash
curl -X POST http://127.0.0.1:18080/api/job/pause
```

再開:

```bash
curl -X POST http://127.0.0.1:18080/api/job/resume
```

キャンセル:

```bash
curl -X POST http://127.0.0.1:18080/api/job/cancel
```

ジョブの状態を取得:

```bash
curl http://127.0.0.1:18080/api/job
```

ログ:

```bash
curl "http://127.0.0.1:18080/api/logs?lines=120"
```
