# Communication Fundamentals - KOKONI EC1

### コマンド送信

```bash
# G-codeの送信
adb -d shell "echo 'G28' > /dev/ttyS1"
adb -d shell "echo 'G1 X10 Y10 F1000' > /dev/ttyS1"

# Mコマンド（メーカー機能）
adb -d shell "echo 'M104 S200' > /dev/ttyS1"  # ホットエンド 200℃設定
adb -d shell "echo 'M109 S200' > /dev/ttyS1"  # ホットエンド 200℃ 待機

# ヒートベッドはないのでM140は使えないが、curaでスライスしているためか、公式アプリ経由のgcodeにもM140が含まれていた。

adb -d shell "echo 'M114' > /dev/ttyS1"       # 現在位置レポート
adb -d shell "echo 'M503' > /dev/ttyS1"       # EEPROM設定ダンプ
adb -d shell "echo 'M115' > /dev/ttyS1"       # ファームウェア情報
```

### 応答確認

```bash
adb -d shell "cat /dev/ttyS1"
```

### 応答確認（別ターミナル）

```bash
# ターミナル1（送信）
adb -d shell
# > echo 'G28' > /dev/ttyS1

# ターミナル2（受信）
adb -d shell "cat /dev/ttyS1"
# > ok
# >（ホーム位置座標）
```

### MCU応答フォーマット

すべてのG-code/M-codeは MCU からの `ok`を含む応答を期待する

```
[SEND] G28
[RECV] ok
[RECV] X:100.00 Y:0.00 Z:10.00 E:0.00 Count X:0 Y:0 Z:0 (example)
[RECV] ok
```

## クラウド通信（MQTT）

### サーバー設定

```bash
# MMKVファイルから抽出
strings mmkv.default | grep "key_server"
# 出力:
# key_server_ip: tcp://hogehoge:1883  MQTTブローカー（中国）
```

## 通信ログ

### シリアルデータ

```bash
# 方法1: cat で受け取ったすべてのデータを表示
adb -d shell "cat /dev/ttyS1"

# 方法2: 十六進ダンプ（バイナリデータを確認）
adb -d shell "hexdump -C /dev/ttyS1"

# 方法3: strace で システムコール傍受
strace -p <PID> -e write -s 256
# ※ Android標準には strace が含まれていない場合が多い。確か今回は使えなかった。
```
