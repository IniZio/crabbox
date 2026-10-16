#!/usr/bin/env bash
# Build the controller and a matching runtime pack from this checkout, verify
# them together, and install both into <bindir>.
set -euo pipefail

BINDIR=${1:?usage: scripts/install-local.sh <bindir>}
ROOT=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
mkdir -p "$BINDIR"
BINDIR=$(cd "$BINDIR" && pwd)
STAGE=$(mktemp -d "$BINDIR/.install-local.XXXXXX")
trap 'rm -rf "$STAGE"' EXIT
mkdir "$STAGE/crabbox-runtime"
cd "$ROOT"

build_id=$(go run ./scripts/runtime-artifacts source-id --source-directory "$ROOT")
go build -trimpath -o "$STAGE/crabbox" ./cmd/crabbox

for target in darwin-amd64 darwin-arm64 linux-amd64 linux-arm64 windows-amd64.exe windows-arm64.exe; do
  platform=${target%%-*}
  arch=${target#*-}
  arch=${arch%.exe}
  CGO_ENABLED=0 GOOS=$platform GOARCH=$arch go build -trimpath \
    -ldflags="-s -w -X github.com/openclaw/crabbox/internal/runner.BuildID=$build_id" \
    -o "$STAGE/crabbox-runtime/$target" ./cmd/crabbox-runtime
done

go run ./scripts/runtime-artifacts prepare --filesystem-build-id "$build_id" \
  --directory "$STAGE/crabbox-runtime" --controller "$STAGE/crabbox" \
  > "$STAGE/manifest.json"
mv "$STAGE/manifest.json" "$STAGE/crabbox-runtime/manifest.json"
go run ./scripts/runtime-artifacts verify --filesystem-build-id "$build_id" \
  --directory "$STAGE/crabbox-runtime" --controller "$STAGE/crabbox" >/dev/null

backup=
if [[ -e "$BINDIR/crabbox-runtime" ]]; then
  backup="$BINDIR/crabbox-runtime.bak.$(date +%Y%m%d%H%M%S)"
  mv "$BINDIR/crabbox-runtime" "$backup"
fi
if ! mv "$STAGE/crabbox-runtime" "$BINDIR/crabbox-runtime" || ! mv -f "$STAGE/crabbox" "$BINDIR/crabbox"; then
  rm -rf "$BINDIR/crabbox-runtime"
  [[ -z "$backup" ]] || mv "$backup" "$BINDIR/crabbox-runtime"
  echo "install failed; previous runtime pack restored" >&2
  exit 1
fi
echo "installed $BINDIR/crabbox with matching crabbox-runtime/ (source $build_id)"
[[ -z "$backup" ]] || echo "previous runtime pack kept at $backup"
