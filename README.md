# kokoni-ec1-server

KOKONI EC1 3Dプリンター用コントローラーサーバー。

このプロジェクトは、プリンターのAndroidシステム上で軽量なHTTPエージェントを直接実行し、UART経由でプリンターのMCUと通信します。デスクトップGUIやcurlクライアントから `adb forward` 経由で印刷ジョブを制御できるようになります。

重要なポイントは、**印刷ジョブはプリンター側で実行される**ということです。ジョブ開始後、PCとの接続を切断しても、プリンターは印刷を続行できます。

## ターゲットデバイス

KOKONI EC1 でテスト済み。

判明しているハードウェア/ソフトウェアの詳細:

```text
SoC: Rockchip RK3126C
Android: 5.1.1
MCU: Nations N32G452 / Marlinベースのファームウェア
MCU UART: /dev/ttyS1
UART ボーレート: 115200
```

開発時に使用した既知のプリンター制限:

```text
ビルドボリューム:
  X: 100 mm
  Y: 100 mm
  Z: 58 mm

G28後のホームポジション:
  正面から見て右下手前

PLA温度:
  純正および互換PLAともに 200°C で良好に動作
```

## アーキテクチャ

```text
PC
  |
  | adb forward tcp:18080 tcp:8080
  v
Android on KOKONI EC1
  |
  | kokoni_launcher
  v
kokoni_web HTTP agent
  |
  | /dev/ttyS1 115200bps
  v
MCU / モーションコントローラー
```

### コンポーネント

```text
cmd/kokoni-agent/main.go
  HTTP API サーバー。
  UARTの初期化、ジョブのアップロード、実行、一時停止/再開/キャンセル、
  ライト制御、ログ出力、状態の永続化を処理します。

cmd/kokoni-launcher/main.go
  adb shell から独立して kokoni_web を起動するための軽量ランチャー。
  adb shell が終了したり、adbサーバーが再起動したり、PCが切断されたりしても、
  印刷エージェントが終了しないようにします。

internal/serial/serial.go
  UARTラッパー。

scripts/build-arm.sh
  プリンター向けに kokoni_web をクロスビルドします。

scripts/deploy.sh
  kokoni_web をプリンターの /system/bin にインストールします。

scripts/run.sh
  kokoni_launcher を起動し、adb forward を設定します。

scripts/stop.sh
  kokoni_launcher 経由で kokoni_web を停止し、adb forward を解除します。
```

## なぜランチャーが存在するのか

初期バージョンでは、adb shell セッションから直接 `kokoni_web` を起動していました。これは機能しましたが、プロセスがPC側のadbセッションに紐付けられたままになる可能性がありました。

`kokoni_launcher` は、Android上で `kokoni_web` をデタッチドプロセスとして起動することでこれを解決します:

```text
kokoni_launcher start
  -> /system/bin/kokoni_web を起動
  -> 親プロセスが init / PID 1 になる
  -> stdout/stderr は /data/local/kokoni_agent/current.log に出力
  -> adb shell の終了や adb サーバーの再起動後もプロセスが存続
```

これが以下の重要な機能を可能にしています:

```text
PCから印刷を開始
PCを切断
プリンターは印刷を続行
後で再接続
scripts/run.sh を再実行
GUI/curl で進行中のジョブを監視可能
```

## ビルド

プリンター側のARMバイナリをビルドします:

```bash
./scripts/build-arm.sh
```

これにより以下が生成されます:

```text
./kokoni_web
```

必要に応じてランチャーを手動でビルドします:

```bash
CGO_ENABLED=0 GOOS=linux GOARCH=arm GOARM=7 \
  go build -ldflags="-s -w" -o kokoni_launcher ./cmd/kokoni-launcher
```

## デプロイ

プリンターはUSB経由で接続されており、adbアクセスが可能である必要があります。

```bash
./scripts/stop.sh
./scripts/deploy.sh
./scripts/run.sh
```

`deploy.sh` は以下をインストールします:

```text
/system/bin/kokoni_web
```

`kokoni_launcher` も以下の場所にインストールされている必要があります:

```text
/system/bin/kokoni_launcher
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

## 実行

起動または再接続:

```bash
./scripts/run.sh
```

これにより3つの処理が行われます:

```text
1. Android側のディレクトリが存在することを確認
2. kokoni_launcher 経由で kokoni_web を起動
3. adb forward tcp:18080 -> tcp:8080 を設定
```

停止:

```bash
./scripts/stop.sh
```

状態の確認:

```bash
curl http://127.0.0.1:18080/api/status
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

## ジョブの動作

エージェントはGコードを1行ずつ送信し、MCUから `ok` を受け取ってから次のコマンドを送信します。

状態は以下に保存されます:

```text
/data/local/kokoni_agent/state.json
```

ログは以下に出力されます:

```text
/data/local/kokoni_agent/current.log
```

アップロードされたジョブのパス:

```text
/data/local/kokoni_agent/jobs/current.gcode
```

エージェント再起動時、アクティブなジョブは自動的に再開されません。以前の状態が `printing`、`paused`、`pausing` だった場合、`interrupted`（中断）として復元されます。

## 一時停止 / 再開の動作

一時停止は安全な行の境界で実装されています。

```text
一時停止を要求
  -> 現在のGコードコマンドが完了する
  -> エージェントが ok を受け取る
  -> Z軸が 10 mm 上昇する
  -> 状態が paused になる
```

再開:

```text
再開を要求
  -> Z軸が 10 mm 下降する
  -> 状態が printing になる
  -> 次のGコード行が送信される
```

一時停止時の退避シーケンス:

```gcode
G91
G1 Z10 F600
G90
```

再開時の復帰シーケンス:

```gcode
G91
G1 Z-10 F600
G90
```

元のXY位置の追跡や復元を避けるため、一時停止中のXY移動は意図的に行いません。

## キャンセルの動作

キャンセル時は、ジョブの状態を `cancelled` に設定し、以下を送信します:

```gcode
M104 S0
```

`M140`、`M106`、`M107` などは、このEC1 MCUが不明なコマンドとして報告することがあるため、キャンセル時には使用しません。

## 既知のMCUの動作

MCUは一部の未対応コマンドに対しても `ok` を返します。

確認された例:

```text
M106 S0 -> echo:Unknown command: "M106 S0" -> ok
M107    -> echo:Unknown command: "M107"    -> ok
```

したがって、APIの `ok:true` は以下を意味します:

```text
MCUが ok を返した。
```

以下を意味するとは限りません:

```text
コマンドが意味的にサポートされ、適切に処理された。
```

## Gコードの取り扱い

アップロードAPIは `.gcode` ファイルのみ受け付けます。

エージェントは軽量な解析を行い、以下のような概要情報を返します:

```text
推定時間
フィラメントの長さ
レイヤー数
温度コマンド
バウンディングボックス
M106 / M140 の有無
```

Curaや公式ワークフローには `M106` や `M140` が含まれる可能性があり、警告として表示されても致命的なエラーにはならないため、これらは寛大に扱われます。

## フィラメントに関するメモ

現在、デスクトップGUIでは測定済みのEC1プリセットを使用しています:

```text
ロード:   340 mm
アンロード: 340 mm
微調整:   +/-20 mm
```

測定された約 340 mm のアンロード値は、テストしたプリンターにおいてフィラメントを良好な取り出し位置に戻します。これはファームウェアレベルの保証ではなく、実用的なプリセット値です。

## リカバリーに関する方針

このプロジェクトでは、boot、init、SELinuxポリシーの変更を意図的に避けています。

以前の調査で、Rockchipのローダーモードからプリンターのリカバリーが可能であることは確認されていますが、日常的な運用でbootイメージを触る必要がないようにすべきです。

現在の設計原則:

```text
絶対に必要でない限り、bootを変更しない。
Androidのinitの変更に依存しない。
/system/bin から取り外したランチャーで実行する。
リカバリーをシンプルに保つ。
```

## 典型的なワークフロー

```bash
# ビルド
./scripts/build-arm.sh

# 更新したエージェントのデプロイ
./scripts/stop.sh
./scripts/deploy.sh
./scripts/run.sh

# ジョブのアップロードと開始
curl -F "gcode=@example.gcode" http://127.0.0.1:18080/api/job/upload
curl -X POST http://127.0.0.1:18080/api/job/start

# オプション: PCを切断する
# Android側のエージェントが印刷を続行します。

# 後で再接続する
./scripts/run.sh
curl http://127.0.0.1:18080/api/job
```

## ステータス

現在のマイルストーン:

```text
スタンドアロンのAndroid側印刷エージェント: 稼働中
デタッチドランチャー: 稼働中
ADB切断時のプロセス存続: 稼働中
PC GUIとの統合: 稼働中
一時停止時のZホップ: 実装済み
ライトコマンドのキューイング: 実装済み
Gコードのアップロード/開始/一時停止/再開/キャンセル: 稼働中
```

このプロジェクトは、KOKONI EC1を実用的なPCアシストプリンターに変えました:

```text
PCを使ってアップロードと開始を行う。
印刷はプリンター単体に任せる。
必要な時に再接続する。
```

## おわりに

これは多くの試行錯誤、リカバリー、テスト、イテレーションを経て構築されました。

目標は単にGコードを送信することではありません。EC1を、スタンドアロンで使い物になるプリンターのように動作させることが目標でした。
