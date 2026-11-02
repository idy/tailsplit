#!/bin/sh
set -eu
umask 077
project=$(CDPATH='' cd -- "$(dirname -- "$0")/.." && pwd)
mkdir -p "$project/bin/config-web-preview"
config="$project/bin/config-web-preview/config.yaml"
if [ ! -e "$config" ]; then
  cp "$project/config.example.yaml" "$config"
fi
exec go -C "$project" run -tags with_gvisor ./cmd/tailsplit-config-web --config "$config" --listen 127.0.0.1:19790
