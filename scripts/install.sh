#!/bin/sh
set -eu
root=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
client=${1:-all}
config=${2:-"$root/jev-client.json"}
binary="$root/bin/jev-mcphub"
if [ ! -x "$binary" ]; then
  command -v go >/dev/null 2>&1 || { echo 'Install Go 1.26.4+, or place the release binary in bin/jev-mcphub.' >&2; exit 1; }
  (cd "$root" && go build -o "$binary" ./cmd/jev-mcphub)
fi
exec "$binary" install --client "$client" --config "$config"
