# Stops the mock core started by hunyuan-mock-up.ps1.
$ErrorActionPreference = "Stop"
$Data = Join-Path $env:TEMP "astrlink-hunyuan-mock"
$PidFile = Join-Path $Data "core.pid"
if (-not (Test-Path $PidFile)) {
  Write-Host "No pid file at $PidFile"
  exit 0
}
$procId = Get-Content $PidFile
Stop-Process -Id $procId -Force -ErrorAction SilentlyContinue
Remove-Item $PidFile -Force -ErrorAction SilentlyContinue
Write-Host "stopped pid=$procId"
