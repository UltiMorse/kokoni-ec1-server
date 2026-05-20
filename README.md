# kokoni-ec1-server

KOKONI EC1 3Dプリンター用のコントローラーサーバーです。

このプロジェクトは、KOKONI EC1 の Android システム上で軽量な HTTP エージェントを直接実行し、UART 経由でプリンターの MCU と通信します。PC 側のデスクトップ GUI や `curl` クライアントは、`adb forward` 経由でこのエージェントにアクセスします。

**最大の強みは、印刷ジョブがプリンター側で完全にスタンドアロン実行されることです。**
PC はアップロード、開始、監視、操作のために使いますが、ジョブ開始後に PC との接続が切れても、プリンター側のエージェントが生きていれば印刷は継続されます。必要なときだけ PC から再接続して監視・操作することができます。

---

## 主な特徴・現在の状態

現在のマイルストーン:
- **スタンドアロンのAndroid側印刷エージェント**: 稼働中
- **デタッチドランチャー**: 稼働中（ADB切断時のプロセス存続）
- **PC GUIとの統合**: 稼働中
- **Gコードの操作**: アップロード / 開始 / 一時停止 / 再開 / キャンセルが稼働中
- **ライトコマンドのキューイング**: 実装済み
- **一時停止時のZホップ**: 実装済み

---

## ターゲットデバイスと制限

KOKONI EC1 でテスト済みです。

**ハードウェア / ソフトウェア要件:**
- SoC: Rockchip RK3126C
- Android: 5.1.1
- MCU: Nations N32G452 / Marlinベースのファームウェア
- MCU UART: `/dev/ttyS1` （ボーレート: 115200）
- UltiMaker Curaの設定はconfigsの中にすべて入っている。

**既知のプリンター制限:**
- **ビルドボリューム**: X: 100 mm / Y: 100 mm / Z: 58 mm
- **G28後のホームポジション**: 正面から見て右下手前
- **PLA温度**: 純正PLAおよび互換PLAともに 200°C で良好に動作

---

## アーキテクチャ

PC 側の GUI や `curl` は、直接 MCU へ接続するわけではありません。エージェントが中継します。

```text
PC (127.0.0.1:18080)
  | 
  | adb forward tcp:18080 tcp:8080
  v
Android on KOKONI EC1 (127.0.0.1:8080)
  | 
  | kokoni_launcher (デタッチドプロセスとして起動)
  v
kokoni_web HTTP agent
  | 
  | /dev/ttyS1 115200bps
  v
MCU / モーションコントローラー
```

### 設計思想

- **絶対に必要でない限り、bootを変更しない。**
- **Androidのinitの変更に依存しない。**
- **ランチャーによるデタッチド起動:** `adb shell` が終了したり切断されたりしても、印刷エージェントが終了しないように、`kokoni_launcher` は init (PID 1) を親プロセスとしてエージェントを起動します。これによりPCの切断・再接続が自由に可能になります。

---

## 動作環境と準備

PC 側に以下が必要です。
- Go 1.22 以上
- `adb`
- `curl`

デスクトップ GUI と併用する場合は、別リポジトリの [kokoni-ec1-desktop](https://github.com/your-username/kokoni-ec1-desktop) も必要です。
- `kokoni-ec1-server`: Android側エージェント / ADB操作 / HTTP API （本リポジトリ）
- `kokoni-ec1-desktop`: PC側デスクトップGUI

### ADB接続について

PC からプリンターへ USB または Wi-Fi で ADB 接続できる必要があります。（ここでは Wi-Fi で `192.168.11.25:5555` を使用する例で説明します）

---

## 使い方: ビルド・デプロイ・実行

### 1. ビルド
プリンター側の ARM バイナリをビルドします。
```bash
./scripts/build-arm.sh
```

### 2. デプロイ
プリンターに ADB 接続した上でデプロイします。
```bash
adb connect 192.168.11.25:5555
./scripts/stop.sh
./scripts/deploy.sh
```

### 3. 実行（起動または再接続）
```bash
adb connect 192.168.11.25:5555
./scripts/run.sh
```
`run.sh` は以下を自動で行います。
1. 作業ディレクトリの確認
2. `kokoni_launcher` 経由で `kokoni_web` を起動
3. `adb forward tcp:18080 tcp:8080` を設定

**状態確認:**
```bash
curl http://127.0.0.1:18080/api/status
```
JSONが返れば成功です。

---

## 典型的なワークフロー

```bash
# 1. 接続・起動
adb connect 192.168.11.25:5555
./scripts/run.sh

# 2. G-codeのアップロードと開始
curl -F "gcode=@example.gcode" http://127.0.0.1:18080/api/job/upload
curl -X POST http://127.0.0.1:18080/api/job/start

# (ここでPCを切断しても印刷は続行されます)

# 3. 後で再接続・監視
adb connect 192.168.11.25:5555
./scripts/run.sh
curl http://127.0.0.1:18080/api/job
```

### OS別の起動例

<details>
<summary><strong>WSLでの起動用スクリプト例</strong></summary>

```bash
#!/usr/bin/env bash
set -euo pipefail

PRINTER_ADB="192.168.11.25:5555"
adb connect "$PRINTER_ADB" >/dev/null

cd "$HOME/src/kokoni-ec1-server"
./scripts/run.sh

cd "$HOME/src/kokoni-ec1-desktop"
exec ./build/bin/kokoni-ec1-desktop
```
</details>

<details>
<summary><strong>Windows (PowerShell) での起動用スクリプト例</strong></summary>

```powershell
adb connect 192.168.11.25:5555
adb forward --remove tcp:18080 2>$null
adb forward tcp:18080 tcp:8080

cd C:\Users\yourname\src\kokoni-ec1-desktop
.\build\bin\kokoni-ec1-desktop.exe
```
</details>

---

## HTTP API リファレンス

基本URL: `http://127.0.0.1:18080`

### システム・制御
- `POST /api/init`: UARTの初期化
- `POST /api/send?cmd=M355%20S255`: 手動コマンド送信（ジョブ非アクティブ時のみ）
- `POST /api/light?value=255`: ライトON (0でOFF)

### ジョブ管理
- `POST /api/job/upload`: `.gcode` のアップロード (multipart form `gcode=@...`)
- `POST /api/job/start`: 印刷開始
- `POST /api/job/pause`: ジョブ一時停止 (Z軸 10mm 退避)
- `POST /api/job/resume`: ジョブ再開 (Z軸 10mm 復帰)
- `POST /api/job/cancel`: ジョブキャンセル (ヒーターをOFF `M104 S0`)
- `GET /api/job`: ジョブ状態取得
- `GET /api/logs?lines=120`: ログ取得

---

## 仕様詳細・制限事項

- **G-codeの処理**: エージェントは軽量な解析を行い、推定時間、フィラメント長、レイヤー数などの概要を返します。`M106` や `M140` などはEC1 MCUが「不明なコマンド」として処理する場合がありますが、`ok` が返るため無視します。
- **一時停止/再開動作**: 一時停止時に Z軸を 10mm 上昇させ、再開時に 10mm 下降させます。XY移動は行いません。
- **キャンセル動作**: `cancelled` 状態にし `M104 S0` を送信します。他のコマンドはエラーを誘発するため送信しません。
- **フィラメント操作**: デスクトップGUI環境では、ロード/アンロードに `340 mm` のプリセットを使用しています。

---

## トラブルシューティング

生成AIに聞くと解決するでしょう。

---

