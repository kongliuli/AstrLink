# Starts astrlink-core with /hunyuan/ai/v1 mock (loopback only).
$ErrorActionPreference = "Stop"
$Root = Resolve-Path (Join-Path $PSScriptRoot "..")
$Data = Join-Path $env:TEMP "astrlink-hunyuan-mock"
New-Item -ItemType Directory -Force -Path $Data | Out-Null
$PidFile = Join-Path $Data "core.pid"
$TokenFile = Join-Path $Data "control.token"
$ReadyFile = Join-Path $Data "ready.json"
$LogFile = Join-Path $Data "core.log"
$OutFile = Join-Path $Data "core.out"
$InferenceTokenFile = Join-Path $Data "inference.token"
$Bin = Join-Path $Data "astrlink-core.exe"

if (Test-Path $PidFile) {
  $existing = Get-Content $PidFile
  if (Get-Process -Id $existing -ErrorAction SilentlyContinue) {
    Write-Host "Already running pid=$existing"
    exit 1
  }
  Remove-Item $PidFile -Force
}

# Control token must be 32-128 [a-z0-9] and end with a newline for stdin reader.
$ControlToken = -join ((1..32) | ForEach-Object { "{0:x2}" -f (Get-Random -Maximum 256) })
[System.IO.File]::WriteAllText($TokenFile, $ControlToken + "`n")

$env:Path = "F:\DevToolchains\go\bin;C:\Program Files\Go\bin;" + $env:Path
$env:GOPROXY = "https://goproxy.cn,direct"
$env:ASTRLINK_HUNYUAN_MOCK = "1"

Write-Host "building $Bin"
Push-Location (Join-Path $Root "core")
try {
  & go build -o $Bin ./cmd/astrlink-core
  if ($LASTEXITCODE -ne 0) { throw "go build failed" }
} finally {
  Pop-Location
}

Remove-Item $OutFile, $LogFile -ErrorAction SilentlyContinue
$p = Start-Process -FilePath $Bin `
  -ArgumentList @("--data-dir", $Data, "--control-token-stdin", "--hunyuan-mock", "--inference-port-fallback") `
  -RedirectStandardInput $TokenFile `
  -RedirectStandardOutput $OutFile `
  -RedirectStandardError $LogFile `
  -PassThru -NoNewWindow -WorkingDirectory $Data
Set-Content -Path $PidFile -Value $p.Id

$ready = $null
for ($i = 0; $i -lt 80; $i++) {
  Start-Sleep -Milliseconds 250
  if (Test-Path $OutFile) {
    foreach ($line in Get-Content $OutFile -ErrorAction SilentlyContinue) {
      if ($line -match '"event"\s*:\s*"ready"') {
        $ready = $line.Trim()
        break
      }
    }
    if ($ready) { break }
  }
  if ($p.HasExited) { break }
}
if (-not $ready) {
  $err = ""
  if (Test-Path $LogFile) { $err = Get-Content $LogFile -Raw }
  throw "core did not become ready; exit=$($p.ExitCode) stderr=$err"
}

Set-Content -Path $ReadyFile -Value $ready
$json = $ready | ConvertFrom-Json
$headers = @{ Authorization = "Bearer $ControlToken" }
$tokens = Invoke-RestMethod -Uri ($json.control_url.TrimEnd('/') + "/control/v1/access-tokens") -Headers $headers
$id = $tokens.items[0].id
$revealed = Invoke-RestMethod -Uri ($json.control_url.TrimEnd('/') + "/control/v1/access-tokens/$id/secret") -Headers $headers
[System.IO.File]::WriteAllText($InferenceTokenFile, $revealed.access_token)

$base = $json.inference_url.TrimEnd('/') + "/hunyuan/ai/v1"
$cap = Invoke-RestMethod -Uri ($base + "/capabilities") -Headers @{ Authorization = "Bearer $($revealed.access_token)" }
if (-not $cap.models) { throw "capabilities empty" }

Write-Host "inference_url=$($json.inference_url)"
Write-Host "hunyuan_base=$base"
Write-Host "inference_token_file=$InferenceTokenFile"
Write-Host "ready_file=$ReadyFile"
Write-Host "pid=$($p.Id)"
Write-Host "capabilities_models=$($cap.models.Count)"
