#!/usr/bin/env bash
# Build astrlink-core, classifier-worker, and the .NET 10 shell into ./bin
set -euo pipefail
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
REPO="${ASTRLINK_REPO:-/workspace}"
BIN="$ROOT/bin"
mkdir -p "$BIN" "$ROOT/models"

export PATH="${HOME}/.dotnet:${PATH}"
export DOTNET_ROOT="${DOTNET_ROOT:-${HOME}/.dotnet}"
export GOTOOLCHAIN="${GOTOOLCHAIN:-auto}"

echo "==> Go core"
(cd "$REPO/core" && go build -trimpath -o "$BIN/astrlink-core" ./cmd/astrlink-core)

echo "==> classifier-worker"
(cd "$REPO/apps/classifier-worker" && cargo +1.88 build --locked --release --bin astrlink-classifier-worker)
cp -f "$REPO/apps/classifier-worker/target/release/astrlink-classifier-worker" "$BIN/"
if [[ -f /tmp/cls-venv/lib/python3.12/site-packages/onnxruntime/capi/libonnxruntime.so.1.23.2 ]]; then
  cp -f /tmp/cls-venv/lib/python3.12/site-packages/onnxruntime/capi/libonnxruntime.so.1.23.2 "$BIN/"
fi

echo "==> .NET 10 shell"
(cd "$ROOT" && dotnet build -c Release -p:EnableWindowsTargeting=true)
cp -f "$ROOT/src/AstrLink.Shell.Host/bin/Release/net10.0/AstrLink.Shell.Host"* "$BIN/" 2>/dev/null || true
cp -f "$ROOT/src/AstrLink.Shell.Core/bin/Release/net10.0/AstrLink.Shell.Core.dll" "$BIN/" 2>/dev/null || true
# Windows tray (cross-built); copy when present
if [[ -d "$ROOT/src/AstrLink.Shell/bin/Release/net10.0-windows" ]]; then
  mkdir -p "$BIN/windows"
  cp -a "$ROOT/src/AstrLink.Shell/bin/Release/net10.0-windows/." "$BIN/windows/"
fi

if [[ -d "$HOME/astrlink-models/astrlink-intent-v1" && ! -d "$ROOT/models/astrlink-intent-v1" ]]; then
  cp -a "$HOME/astrlink-models/astrlink-intent-v1" "$ROOT/models/"
fi

echo "==> done: $BIN"
ls -la "$BIN" | head -40
