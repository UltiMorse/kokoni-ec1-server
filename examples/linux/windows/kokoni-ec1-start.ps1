$ErrorActionPreference = "Stop"

$KokoniAdb = "192.168.11.25:5555"
$AgentUrl = "http://127.0.0.1:18080/api/status"
$DesktopDir = "C:\Users\YourUsername\src\kokoni-ec1-desktop"
$DesktopExe = Join-Path $DesktopDir "build\bin\kokoni-ec1-desktop.exe"

Write-Host "Connecting Wi-Fi ADB: $KokoniAdb"

$adbReady = $false

for ($i = 1; $i -le 20; $i++) {
    adb connect $KokoniAdb | Out-Null

    $state = adb -s $KokoniAdb get-state 2>$null

    if ($state -eq "device") {
        $adbReady = $true
        break
    }

    Write-Host "Waiting for Wi-Fi ADB... ($i/20)"
    Start-Sleep -Seconds 1
}

if ($adbReady) {
    $env:ANDROID_SERIAL = $KokoniAdb
    Write-Host "Using Wi-Fi ADB: $env:ANDROID_SERIAL"
} else {
    Remove-Item Env:ANDROID_SERIAL -ErrorAction SilentlyContinue
    throw "Wi-Fi ADB not ready: $KokoniAdb"
}

Write-Host "=== adb root ==="
adb -s $KokoniAdb root

Start-Sleep -Seconds 2

adb connect $KokoniAdb | Out-Null
$env:ANDROID_SERIAL = $KokoniAdb

try {
    adb -s $KokoniAdb shell "setenforce 0"
} catch {
}

adb -s $KokoniAdb shell "su -c 'mkdir -p /data/local/kokoni_agent/jobs/uploaded'"
adb -s $KokoniAdb shell "su -c 'chmod -R 777 /data/local/kokoni_agent'"

Write-Host "=== cleanup huge log ==="
adb -s $KokoniAdb shell "su -c ': > /data/local/kokoni_agent/current.log'"
adb -s $KokoniAdb shell "su -c 'chmod 666 /data/local/kokoni_agent/current.log'"

Write-Host "=== start launcher ==="
adb -s $KokoniAdb shell "su -c '/system/bin/kokoni_launcher start'"

try {
    adb -s $KokoniAdb forward --remove tcp:18080 2>$null
} catch {
}

adb -s $KokoniAdb forward tcp:18080 tcp:8080

Write-Host "=== launcher status ==="
adb -s $KokoniAdb shell "su -c '/system/bin/kokoni_launcher status'"

Write-Host "=== ps ==="
adb -s $KokoniAdb shell "ps | grep kokoni || true"

Write-Host "=== waiting for printer ready ==="

$printerReady = $false

for ($i = 1; $i -le 60; $i++) {
    try {
        $status = curl.exe -fsS $AgentUrl

        if (
            $status -match '"agent":"running"' -and
            $status -match '"printer":"ready"' -and
            $status -match '"uart_connected":true'
        ) {
            $printerReady = $true
            break
        }

        Write-Host "Waiting for printer ready... ($i/60)"
        Write-Host $status
    } catch {
        Write-Host "Waiting for agent API... ($i/60)"
    }

    Start-Sleep -Seconds 1
}

Write-Host "=== status ==="

try {
    curl.exe -s $AgentUrl
    Write-Host ""
} catch {
    Write-Host "status request failed"
}

if (-not $printerReady) {
    Write-Host ""
    Write-Host "=== debug: adb devices ==="
    adb devices

    Write-Host "=== debug: adb forward --list ==="
    adb -s $KokoniAdb forward --list

    Write-Host "=== debug: ps ==="
    adb -s $KokoniAdb shell "ps | grep kokoni || true"

    Write-Host "=== debug: launcher status ==="
    adb -s $KokoniAdb shell "su -c '/system/bin/kokoni_launcher status'"

    Write-Host "=== debug: current.log last 120 lines ==="
    try {
        adb -s $KokoniAdb shell "cat /data/local/kokoni_agent/current.log" | Select-Object -Last 120
    } catch {
        Write-Host "could not read current.log"
    }

    throw "kokoni printer did not become ready: $AgentUrl"
}

Write-Host "=== launch desktop ==="
Set-Location $DesktopDir
& $DesktopExe
