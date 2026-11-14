#!/usr/bin/env bash
# Select matching native and Dart identities before building an unsigned archive.
set -euo pipefail

channel="${1:?Usage: build_ios_release.sh <beta|app-store> [flutter build ipa options]}"
shift
case "$channel" in
  beta|app-store) ;;
  *) printf 'Unknown iOS release channel: %s\n' "$channel" >&2; exit 1 ;;
esac

cd "$(dirname "$0")/.."
ruby ios/configure_project.rb "$channel"
flutter build ipa --release --no-codesign --no-pub \
  --dart-define="TAILSPLIT_IOS_CHANNEL=$channel" "$@"
python3 -I -B tool/release_ios.py \
  --verify-capabilities build/ios/archive/Runner.xcarchive \
  --core-receipt .dart_tool/sing_box_flutter/native.json \
  --capabilities-report build/ios/archive/core-capabilities-verification.json
