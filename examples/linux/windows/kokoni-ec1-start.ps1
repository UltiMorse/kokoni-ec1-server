$ErrorActionPreference = "Stop"

$KokoniAdb = "192.168.x.x:5555"
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
    Write-Host "Wi-Fi ADB not ready. Falling back to default adb device."
}

Write-Host "=== adb root ==="
adb root

Start-Sleep -Seconds 2

if ($adbReady) {
    adb connect $KokoniAdb | Out-Null
    $env:ANDROID_SERIAL = $KokoniAdb
}

try {
    adb shell "setenforce 0"
} catch {
}

adb shell "su -c 'mkdir -p /data/local/kokoni_agent/jobs/uploaded'"
adb shell "su -c 'chmod -R 777 /data/local/kokoni_agent'"

Write-Host "=== start launcher ==="
adb shell "su -c '/system/bin/kokoni_launcher start'"

try {
    adb forward --remove tcp:18080 2>$null
} catch {
}

adb forward tcp:18080 tcp:8080

Write-Host "=== launcher status ==="
adb shell "su -c '/system/bin/kokoni_launcher status'"

Write-Host "=== ps ==="
adb shell "ps | grep kokoni || true"

Write-Host "=== waiting for status ==="

$agentReady = $false

for ($i = 1; $i -le 30; $i++) {
    try {
        curl.exe -fsS $AgentUrl | Out-Null
        $agentReady = $true
        break
    } catch {
        Write-Host "Waiting for agent API... ($i/30)"
        Start-Sleep -Seconds 1
    }
}

Write-Host "=== status ==="

try {
    curl.exe -s $AgentUrl
    Write-Host ""
} catch {
    Write-Host "status request failed"
}

if (-not $agentReady) {
    Write-Host ""
    Write-Host "=== debug: adb devices ==="
    adb devices

    Write-Host "=== debug: adb forward --list ==="
    adb forward --list

    Write-Host "=== debug: ps ==="
    adb shell "ps | grep kokoni || true"

    Write-Host "=== debug: log ==="
    adb shell "tail -n 120 /data/local/kokoni_agent/current.log || true"

    throw "kokoni agent API did not become ready: $AgentUrl"
}

Write-Host "=== launch desktop ==="
Set-Location $DesktopDir
& $DesktopExe
