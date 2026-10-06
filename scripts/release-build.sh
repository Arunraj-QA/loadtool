#!/usr/bin/env bash
# Builds LoadTool release archives and their checksums.
#
#   scripts/release-build.sh v0.1.0 dist
#
# For each platform it writes loadtool_<version>_<os>_<arch>.tar.gz (.zip
# for Windows) holding the binary, LICENSE and README.md, then
# checksums.txt with their SHA-256 sums. Binaries are static (no cgo),
# built with -trimpath, and report the version with `loadtool --version`.
# The archive names are what action.yml downloads; keep them in step.
set -euo pipefail

version=${1:?usage: release-build.sh <version> <output dir>}
out=${2:?usage: release-build.sh <version> <output dir>}
root=$(cd "$(dirname "$0")/.." && pwd)
platforms=${PLATFORMS:-"linux/amd64 linux/arm64 darwin/amd64 darwin/arm64 windows/amd64 windows/arm64"}

mkdir -p "$out"
out=$(cd "$out" && pwd)
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT

for platform in $platforms; do
  os=${platform%/*}
  arch=${platform#*/}
  name="loadtool_${version}_${os}_${arch}"
  exe=loadtool
  [ "$os" = windows ] && exe=loadtool.exe
  mkdir -p "$work/$name"
  echo "building $name"
  (cd "$root" && CGO_ENABLED=0 GOOS=$os GOARCH=$arch go build -trimpath \
    -ldflags "-s -w -X github.com/Arunraj-QA/loadtool/internal/cli.Version=$version" \
    -o "$work/$name/$exe" ./cmd/loadtool)
  cp "$root/LICENSE" "$root/README.md" "$work/$name/"
  if [ "$os" = windows ]; then
    if command -v zip >/dev/null; then
      (cd "$work" && zip -qr "$out/$name.zip" "$name")
    else
      (cd "$work" && python -m zipfile -c "$out/$name.zip" "$name")
    fi
  else
    tar -C "$work" -czf "$out/$name.tar.gz" "$name"
  fi
done

(cd "$out" && sha256sum loadtool_"${version}"_* > checksums.txt)
echo "wrote $(wc -l < "$out/checksums.txt") archives and checksums.txt to $out"
