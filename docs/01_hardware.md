# Hardware Analysis - KOKONI EC1

## ハードウェアアーキテクチャ

### メイン基板
- **SoC**: Rockchip RK3126C (ARM Cortex-A7 Quad-Core)
- **OS**: Android 5.1.1
- **ストレージ**: 4GB NAND Flash
- **インターフェース**: Wi-Fi / Bluetooth / Micro USB
- **通信・デバッグ**: UART (`/dev/ttyS1`, 115200bps) / DEBUG端子（TTL）

### 駆動制御基板
- **MCU**: Nations N32G452 (ARM Cortex-M4F)
- **機能**: ステッピングモータードライバ、ヒーター/温度センサー制御
- **ファームウェア**: Marlin (標準的な3Dプリンター制御)
- **デバッグ**: J-Link (SWD: Serial Wire Debug)

---

## Rockchip RK3126C

### パーティション構成

```bash
# /proc/partitions

major minor  #blocks  name
  31        0       4096 rknand_uboot       (ブートローダー)
  31        1       4096 rknand_misc 
  31        2      16384 rknand_resource
  31        3      12288 rknand_kernel
  31        4      12288 rknand_boot
  31        5      32768 rknand_recovery    (リカバリーモード)
  31        6      65536 rknand_backup
  31        7     131072 rknand_cache       (/cache)
  31        8    1044480 rknand_userdata    (/data, 約1GB)
  31        9      16384 rknand_metadata
  31       10       4096 rknand_kpanic
  31       11    1048576 rknand_system      (/system, 約1GB) 
  31       12      65536 rknand_radical_update
  31       13    1447936 rknand_user        (/mnt/internal_sd, 約1.4GB) ← FAT32, noexec
```

- `/system` → ext4, Read-Only
- `/data` → ext4, Read-Write
- `/mnt/internal_sd` → FAT32, noexec

```bash
# 確認コマンド
adb -d shell "cat /proc/partitions"
adb -d shell "mount"
```

---

## N32G452 MCU

### J-Link ピン配置

```text
3V3 (VCC)
 │
SWDIO
 │
SWCLK
 │
GND
```
---

## システムハンドシェイク

### 初期化シーケンス

プリンター電源投入直後の挙動：

1. MCUの自律起動（Android起動待ち）
   - ブザー短音
   - LED点滅開始（初期化待機状態）
   - シリアルポート無応答
2. Android OS起動
   - Rockchip RK3126C が起動
   - 純正アプリ `com.dq.printer` が自動起動するが、これを止めて代替制御するのが目標
3. ハンドシェイク
   - アプリがMCUへ「初期化完了」コマンドを送信
   - MCU: LED点滅停止 → ブザー長音
   - G-code受け取り準備完了

### 通信パターン

スマホアプリ → クラウド → プリンター

しかし、サーバーが頻繁にダウンしており、サーバーがダウンしているとスマホからの操作は一切できない。また、ファイルサイズの大きなSTLは処理できない。

1. スマホからSTLをクラウドに送信
2. クラウドサーバーでSTLをスライスしてG-codeに変換
3. プリンターが G-code をダウンロード
4. RK3126C が `/dev/ttyS1` 経由で MCU へ G-code を送信

### デバイス識別

```text
deviceId: hogehogehogehogehogehogehogehogehogeho
user_id: hogehogehogehogehogeho
```

### ADBによる初期化コマンド（ハックの基本方針）

なぜ再起動後に `echo` が効かなくなるのか
KOKONIを再起動すると、Androidカーネルがシリアルポート（`/dev/ttyS1`）の通信速度を初期値（おそらく9600bps）にリセットしてしまうため。

`stty` コマンドが使えないこの環境において、通信速度を 115200bps に戻すには、導入済みの `socat` を「送信・初期化ツール」として一瞬だけ使って速度を固定させる方法が有効。

```bash
adb shell "su -c '(echo -ne "N-1 M110*15\r\n"; sleep 0.5; echo -ne "N0 M355 S0*99\r\n"; sleep 2) | /data/local/tmp/socat - /dev/ttyS1,raw,echo=0,b115200'"
```

`socat` がシリアルポートを確実に `b115200` で開いてくれるため、マイコン（MCU）が正しい速度とフォーマットで初期化される。
一度これが成功してLEDが消えれば、ポートの速度は115200bpsに固定されるため、以降は単純なシェルスクリプトや `cat test.gcode | socat ...` だけを用いた印刷処理が可能になる。

---

## ビルドプレート

- **サイズ**: X 100mm / Y 100mm / Z 58mm
- **ホットベッド**: なし

---

## バックアップ

### NANDバックアップ

```bash
mkdir -p kokoni_backup && cd kokoni_backup

# システム関連
adb -d pull /dev/block/rknand_uboot ./uboot.img
adb -d pull /dev/block/rknand_kernel ./kernel.img
adb -d pull /dev/block/rknand_boot ./boot.img
adb -d pull /dev/block/rknand_recovery ./recovery.img
adb -d pull /dev/block/rknand_system ./system.img

# ユーザーデータ関連
adb -d pull /dev/block/rknand_userdata ./userdata.img
adb -d pull /dev/block/rknand_user ./user.img
```

---

## 復旧方法

Android 上の `/dev/block/rknand_boot` と、`rkdeveloptool` から見える raw NAND offset は単純な byte offset 加算では扱えない点に注意が必要。

### 事前バックアップの確認

`/dev/block/rknand_boot` の完全バックアップ（`boot.img`）。

```bash
ls -l boot.img kernel.img recovery.img system.img uboot.img
file boot.img kernel.img recovery.img system.img uboot.img
binwalk boot.img | head -n 30
```

結果: `boot.img` は 12582912 bytes  
`boot.img` は通常の AOSP Android boot image ではなく、Rockchip 系の KRNL header + gzip ramdisk*だった。
```text
00000000: 4b52 4e4c .... .... 1f8b 0800 ...
           K R  N L          gzip
```

### 壊れた原因

ramdisk 内の `sepolicy` を `sepolicy-inject` で変更し、その変更済み `rknand_boot` を NAND に書き戻したこと。

Android 5.1 / SELinux policy v26 の古い binary sepolicy に対して、`sepolicy-inject` の出力が実機の kernel/init と互換しなかった可能性

通常 boot 失敗 → recovery へ fallback → adb shell が recovery で使えない → 最終的に MaskROM / Loader 経由で復旧。

> 後から `parameter` を確認した結果、元々 kernel cmdline に `androidboot.selinux=permissive` が入っていた。つまり、本端末は本来 boot 時点で SELinux Permissive になる設計だったため、sepolicy を改造する必要はなかった。

### MaskROM / Loader へ入る

壊れた後 USB接続では `recovery` として認識されたが、`adb shell` が使えなかったため、bootloader へ移行

```bash
adb reboot bootloader
```

確認:
```bash
lsusb | grep -i rockchip
rkdeveloptool ld
adb devices
```
成功時:
`lsusb` では `Mask ROM mode` と表示され、`rkdeveloptool ld` では `Loader` と表示される（`adb devices` は空になる）。この状態なら `rkdeveloptool rl/wl` が使える。

### parameter 領域の読み出し

まず Rockchip の parameter 領域を読む。

```bash
sudo rkdeveloptool rl 0x0 0x400000 parameter_area.bin
ls -lh parameter_area.bin

# 情報の抽出
strings -a parameter_area.bin | grep -iE 'MAGIC|CMDLINE|mtdparts|KERNEL_IMG|BOOT_IMG|RECOVERY_IMG|MISC_IMG|parameter|uboot|misc|resource|kernel|boot|recovery|backup|cache|userdata|metadata|kpanic|system|radical|user' | head -n 500
```

実際に得られた mtdparts:
```text
mtdparts=rk29xxnand:
0x00002000@0x00002000(uboot),
...
0x00006000@0x00014000(boot),
...
```
boot パーティションは `0x00006000@0x00014000(boot)` と定義されていた。

### boot パーティション位置の確定と読み出し

mtdparts より:
- boot start = `0x00014000` sectors
- boot size  = `0x00006000` sectors (0x6000 * 512 = 0xC00000 bytes)

つまり、rkdeveloptool では以下で `rknand_boot` が読める。
（`rkdeveloptool rl` の第1引数は sector offset、第2引数は byte length）

```bash
sudo rkdeveloptool rl 0x14000 0xC00000 boot_current_correct.img
binwalk boot_current_correct.img | head -n 30
xxd -l 64 boot_current_correct.img
```
これで `0x14000` が `rknand_boot` の正しい書き込み先

### boot.img の書き戻し

一致確認：
```bash
cmp ~/kokoni-controller-public/kokoni_backup/boot.img ~/kokoni-ec1-tools/rknand_boot.img.original.backup
```

書き戻し実行：
```bash
cd ~/kokoni-controller-public/kokoni_backup
sudo rkdeveloptool wl 0x14000 boot.img
```

書き戻し後の検証：
```bash
sudo rkdeveloptool rl 0x14000 0xC00000 boot_after_restore.img
cmp ~/kokoni-controller-public/kokoni_backup/boot.img boot_after_restore.img
```
再起動：
```bash
sudo rkdeveloptool rd
```

### 復旧後確認

通常起動後：
```bash
adb wait-for-device
adb shell getenforce  # Permissive
adb shell getprop ro.hardware
adb shell getprop ro.board.platform
```

---

## セキュリティとハック

### Android

- SELinux: 有効化・無効化可能（`setenforce 1/0`）
- root権限: adb root で取得可
- 純正アプリのロック: `pm disable` で無効化可

必須ステップ（毎回電源投入後）
```bash
adb root
adb shell "setenforce 0"
# adb shell "pm disable com.dq.printer" # 既に止めていれば不要
```

### ファイルシステムアクセス権

```bash
# /data（アプリデータ領域）
adb -d shell
cd /data/data/com.dq.printer/

# 設定ファイル（MMKV）
cat files/mmkv/mmkv.default | strings | grep "key_"
# 出力例:
# key_server_ip: tcp://hogehoge:1883  ← MQTTブローカー
# key_user_id: hogehogehogehoge                 ← デバイスID
# key_gcode_file_path: /mnt/internal_sd/com.dq.printer/gCode/*.gcode

# データベース（SQLite）
ls -l files/dbs/
# - mqttAndroidService_bd（通信ログ）
# - print_bd（印刷履歴）
```
