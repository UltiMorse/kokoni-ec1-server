# Reverse Engineering

## APK抽出と逆コンパイル試行

暗号化・難読化ツールが使われているので、解読不可能：Tencent Legu（libnesec.so）

## アプリデータ領域の解析

### 設定ファイル（shared_prefs）の確認

```bash
adb -d shell
cd /data/data/com.dq.printer/
cat shared_prefs/*.xml
```

出力：
```xml
<?xml version='1.0' encoding='utf-8' standalone='yes' ?>
<map>
    <string name="deviceId">hogehogehogehogehogehogehogehogehogehogehogehogehoge</string>
</map>
```
デバイス固有ID

### MMKVの解析

```bash
# PC側で展開
adb -d pull /data/data/com.dq.printer/files/mmkv/ ./kokoni_mmkv/
strings ./kokoni_mmkv/mmkv.default
```

```plaintext
key_server_ip: tcp://hogehoge:1883
key_user_id: hogehogehogehoge
key_gcode_file_path: /mnt/internal_sd/com.dq.printer/gCode/hogehogehoge.gcode
```

MQTT通信の仕様

### データベースの確認

```bash
adb -d pull /data/data/com.dq.printer/files/dbs/ ./kokoni_dbs/
# DB Browser for SQLite を使用して解析
```

対象ファイル：
- `mqttAndroidService_bd`: MQTT通信ログ
- `print_bd`: 印刷履歴・G-codeファイルパス管理

---

## システムアーキテクチャの確認

### ネイティブライブラリの解剖

```bash
adb -d pull /data/app/com.dq.printer-2/lib/arm/libserial_port.so ./
strings libserial_port.so | grep -iE "M[0-9]{2,4}"
```

出力：
```plaintext
Java_android_serialport_SerialPort_close
Java_android_serialport_SerialPort_open
Cannot open port
Invalid baudrate
Configuring serial port
```

→ 汎用的なシリアルライブラリ。Mコードは暗号化層（Javaアプリ）で生成される

### ファイルシステムの構造

```bash
ls -lR /data/data/com.dq.printer/
```

出力：
```
files/
  ├─ dbs/
  │  ├─ mqttAndroidService_bd
  │  └─ print_bd
  ├─ mmkv/
  │  ├─ mmkv.default
  │  └─ mmkv.default.crc
  └─ (その他キャッシュ)

shared_prefs/
  ├─ BUGLY_COMMON_VALUES.xml
  ├─ crashrecord.xml
  └─ printer.xml
```

## 動的解析

### socatを用いたttyS1の偽装（MitM）による通信傍受

`strace` が使えないため、シリアルデバイスファイル自体を偽装するMitMを採用しました。これが起動時のハンドシェイクやLED制御コマンドを正確に把握する手がかりとなった。

1. socat静的バイナリの準備
ARM(RK3126C)で動く静的コンパイルされた `socat`（`socat-armhf` 等）を入手し、デバイスに配置します。

```bash
adb push socat /data/local/tmp/
adb shell "chmod 755 /data/local/tmp/socat"
```

2. 本物のポートの退避
本物の `/dev/ttyS1` をリネームして退避します。

```bash
adb shell "mv /dev/ttyS1 /dev/ttyS1_orig"
```

3. プロキシの開始
Terminal A にて、ダミーの `/dev/ttyS1` (PTY) を作成し、そこに入ってきた内容を本物の(`/dev/ttyS1_orig`)へ転送（かつ `-v` でコンソール出力）する `socat` を立ち上げる。

```bash
adb shell "pm enable com.dq.printer"
```
enableにした状態で本体の電源を切る。プラグも抜き、電源スイッチを数回オンオフして電気を抜く。

```bash
# Terminal A (傍受用)
adb shell "/data/local/tmp/socat -v PTY,link=/dev/ttyS1,raw,echo=0 /dev/ttyS1_orig,raw,echo=0,b115200"
```

4. 通信をインターセプト
Terminal B にて、Broadcastでブート完了イベントを擬似的に送り、バックグラウンドワーカー起動。

```bash
# Terminal B (トリガー用)
adb shell "am broadcast -a android.intent.action.BOOT_COMPLETED -p com.dq.printer"
```

5. ログの確認
Terminal A の `socat` に、純正アプリからMCUへ送られる生の通信ログが流れる。この手続きで、LED点滅などを制御するコマンド等、初期化手順がキャプチャ可能であった。
