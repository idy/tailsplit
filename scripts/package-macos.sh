#!/bin/sh
set -eu
umask 077

test "$(uname -s)" = Darwin || { echo "macOS packaging requires macOS" >&2; exit 1; }
project=$(CDPATH='' cd -- "$(dirname -- "$0")/.." && pwd)
go_command=${GO:-go}
python=${PYTHON:-python3}
third_party_notices=${THIRD_PARTY_NOTICES_FILE:-"$project/THIRD_PARTY_NOTICES.md"}
if test -n "${THIRD_PARTY_NOTICES_FILE:-}" && test ! -f "$third_party_notices"; then
  echo "THIRD_PARTY_NOTICES_FILE does not exist: $third_party_notices" >&2
  exit 1
fi
version=$(cat "$project/VERSION")
build_number=$(cat "$project/BUILD_NUMBER")
printf '%s\n' "$version" | /usr/bin/grep -Eq '^[0-9]+\.[0-9]+\.[0-9]+$' ||
  { echo "Invalid VERSION" >&2; exit 1; }
printf '%s\n' "$build_number" | /usr/bin/grep -Eq '^[1-9][0-9]*$' ||
  { echo "Invalid BUILD_NUMBER" >&2; exit 1; }
signing_mode=${SIGNING_MODE:-adhoc}
notarize=${NOTARIZE:-0}
team=${APPLE_TEAM_ID:-$(plutil -extract team_id raw "$project/release-identity.json")}
find_identities() {
  if test -n "${APPLE_KEYCHAIN:-}"; then
    security find-identity -v -p "$1" "$APPLE_KEYCHAIN"
  else
    security find-identity -v -p "$1"
  fi
}
case "$notarize" in
  0|1) ;;
  *) echo "NOTARIZE must be 0 or 1" >&2; exit 1 ;;
esac
case "$signing_mode" in
  adhoc)
    test "$notarize" = 0 ||
      { echo "Notarization requires SIGNING_MODE=developer-id" >&2; exit 1; }
    application_identity=-
    installer_identity=
    suffix=-adhoc
    ;;
  developer-id)
    # Select valid identities for TailSplit's release team. A similarly named Apple
    # Development/iOS Distribution certificate is not a Developer ID.
    application_identity=${APPLE_APPLICATION_IDENTITY:-$(find_identities codesigning |
      awk -v team="$team" 'index($0, "\"Developer ID Application: ") && index($0, "(" team ")\"") { print $2 }')}
    installer_identity=${APPLE_INSTALLER_IDENTITY:-$(find_identities basic |
      awk -v team="$team" 'index($0, "\"Developer ID Installer: ") && index($0, "(" team ")\"") { print $2 }')}
    test -n "$application_identity" && test -n "$installer_identity" ||
      { echo "Missing Developer ID Application/Installer identities for team $team." >&2; exit 1; }
    test "$(printf '%s\n' "$application_identity" | wc -l | tr -d ' ')" = 1 &&
      test "$(printf '%s\n' "$installer_identity" | wc -l | tr -d ' ')" = 1 ||
      { echo "Multiple identities found; select APPLE_APPLICATION_IDENTITY and APPLE_INSTALLER_IDENTITY." >&2; exit 1; }
    suffix=-signed
    test "$notarize" = 0 || suffix=
    if test "$notarize" = 1; then
      if test -n "${NOTARY_KEYCHAIN_PROFILE:-}"; then
        :
      elif test -n "${NOTARY_KEY_FILE:-}" && test -n "${NOTARY_KEY_ID:-}" && test -n "${NOTARY_ISSUER_ID:-}"; then
        test -f "$NOTARY_KEY_FILE" || { echo "NOTARY_KEY_FILE does not exist" >&2; exit 1; }
      else
        echo "Set NOTARY_KEYCHAIN_PROFILE or use scripts/with-notary-credentials.py." >&2
        exit 1
      fi
    fi
    ;;
  *) echo "SIGNING_MODE must be adhoc or developer-id" >&2; exit 1 ;;
esac

sign_code() {
  if test "$signing_mode" = developer-id; then
    if test -n "${APPLE_KEYCHAIN:-}"; then
      codesign --force --sign "$application_identity" --keychain "$APPLE_KEYCHAIN" \
        --options runtime --timestamp "$1"
    else
      codesign --force --sign "$application_identity" --options runtime --timestamp "$1"
    fi
    metadata=$(codesign -dvv "$1" 2>&1)
    printf '%s\n' "$metadata" | /usr/bin/grep -Fxq "TeamIdentifier=$team" ||
      { echo "Code signature is not from the expected Apple team." >&2; exit 1; }
    printf '%s\n' "$metadata" | /usr/bin/grep -q '^Authority=Developer ID Application:' ||
      { echo "Code signature is not a Developer ID Application signature." >&2; exit 1; }
  else
    codesign --force --sign - --options runtime "$1"
  fi
}

notarize_file() {
  label=$2
  notary_status=0
  if test -n "${NOTARY_KEYCHAIN_PROFILE:-}"; then
    xcrun notarytool submit "$1" --keychain-profile "$NOTARY_KEYCHAIN_PROFILE" \
      --wait --timeout 30m --output-format json > "$stage/$label-notary.json" || notary_status=$?
  else
    xcrun notarytool submit "$1" --key "$NOTARY_KEY_FILE" \
      --key-id "$NOTARY_KEY_ID" --issuer "$NOTARY_ISSUER_ID" \
      --wait --timeout 30m --output-format json > "$stage/$label-notary.json" || notary_status=$?
  fi
  cp "$stage/$label-notary.json" "$output/$label-notary.json"
  test "$notary_status" = 0 ||
    { echo "Notarization failed or timed out; see $output/$label-notary.json." >&2; exit 1; }
  if ! "$python" - "$stage/$label-notary.json" <<'PY'
import json
import sys
document = json.load(open(sys.argv[1]))
print("Notarization:", document.get("status"), "submission:", document.get("id"))
sys.exit(0 if document.get("status") == "Accepted" else 1)
PY
  then
    echo "Notarization rejected; submission details saved in $output/$label-notary.json." >&2
    exit 1
  fi
}

output=${1:-bin}
case "$output" in
  /*) ;;
  *) output="$project/$output" ;;
esac
app="$output/TailSplit.app"
artifact="TailSplit-$version-build$build_number-macos-$(uname -m)$suffix"

# macOS may invalidate mapped code if its on-disk executable is overwritten.
# Stage an update elsewhere while either the GUI or its old core is running.
if /bin/ps -axo command= | /usr/bin/awk -v prefix="$app/Contents/MacOS/" \
    'index($0, prefix) == 1 { found = 1 } END { exit !found }'; then
  echo "TailSplit.app is running here; quit it or build with OUT=bin/staging." >&2
  exit 1
fi

mkdir -p "$output"
stage=$(mktemp -d "$output/package.XXXXXX")
trap 'rm -rf "$stage"' EXIT HUP INT TERM
# Start with a fresh bundle so removed resources from an older build do not
# become part of a signed release.
rm -rf "$app"
make -C "$project" GO="$go_command" PYTHON="$python" OUT="$output" build build-tray
mkdir -p "$app/Contents/MacOS" "$app/Contents/Resources" "$output/TailSplit.iconset"
cp "$output/tailsplit" "$app/Contents/MacOS/tailsplit"
cp "$output/tailsplit-tray" "$app/Contents/MacOS/tailsplit-tray"
cp "$project/config.example.yaml" "$app/Contents/Resources/config.example.yaml"
core_module=$(cd "$project" && "$go_command" list -m -f '{{.Dir}}' github.com/sagernet/sing-box)
cp "$core_module/LICENSE" "$app/Contents/Resources/sing-box-LICENSE.txt"
if test -f "$project/LICENSE"; then
  cp "$project/LICENSE" "$app/Contents/Resources/LICENSE"
fi
if test -f "$third_party_notices"; then
  cp "$third_party_notices" "$app/Contents/Resources/THIRD_PARTY_NOTICES.md"
fi
chmod 755 "$app" "$app/Contents" "$app/Contents/MacOS" "$app/Contents/Resources"
chmod 755 "$app/Contents/MacOS/tailsplit" "$app/Contents/MacOS/tailsplit-tray"
swift "$project/scripts/make-icons.swift" "$output/TailSplit.iconset"
iconutil -c icns "$output/TailSplit.iconset" -o "$app/Contents/Resources/AppIcon.icns"
cat > "$app/Contents/Info.plist" <<PLIST
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>CFBundleName</key><string>TailSplit</string>
  <key>CFBundleDisplayName</key><string>TailSplit</string>
  <key>CFBundleIdentifier</key><string>im.lbv.tailsplit</string>
  <key>CFBundleExecutable</key><string>tailsplit-tray</string>
  <key>CFBundlePackageType</key><string>APPL</string>
  <key>CFBundleShortVersionString</key><string>$version</string>
  <key>CFBundleVersion</key><string>$build_number</string>
  <key>CFBundleIconFile</key><string>AppIcon</string>
  <key>LSUIElement</key><true/>
  <key>LSMultipleInstancesProhibited</key><true/>
  <key>LSMinimumSystemVersion</key><string>14.0</string>
  <key>NSHighResolutionCapable</key><true/>
  <key>NSLocalNetworkUsageDescription</key>
  <string>TailSplit connects to your Tailscale devices and provides a local network proxy.</string>
</dict>
</plist>
PLIST
chmod 644 "$app/Contents/Info.plist" "$app/Contents/Resources/"*
plutil -lint "$app/Contents/Info.plist"
sign_code "$app/Contents/MacOS/tailsplit"
sign_code "$app/Contents/MacOS/tailsplit-tray"

mkdir -p "$stage/root/Library/PrivilegedHelperTools" "$stage/install" "$stage/remove"
chmod 755 "$stage/root" "$stage/root/Library"
chmod 1755 "$stage/root/Library/PrivilegedHelperTools"
cp "$app/Contents/MacOS/tailsplit" "$stage/root/Library/PrivilegedHelperTools/im.lbv.tailsplit.helper"
chmod 755 "$stage/root/Library/PrivilegedHelperTools/im.lbv.tailsplit.helper"
cp "$project/packaging/macos/service-install/"* "$stage/install/"
cp "$project/packaging/macos/service-remove/"* "$stage/remove/"
chmod 755 "$stage/install/"* "$stage/remove/"*
# Copy the already-signed core. Signing this copy again would change its hash
# and make the app think its installed network service needs another update.
set --
if test "$signing_mode" = developer-id; then
  set -- --sign "$installer_identity" --timestamp
  if test -n "${APPLE_KEYCHAIN:-}"; then
    set -- "$@" --keychain "$APPLE_KEYCHAIN"
  fi
fi
pkgbuild --root "$stage/root" --scripts "$stage/install" \
  --identifier im.lbv.tailsplit.helper --version "$version" --install-location / \
  "$@" \
  --ownership recommended "$app/Contents/Resources/TailSplit Network Service.pkg"
pkgbuild --nopayload --scripts "$stage/remove" \
  --identifier im.lbv.tailsplit.helper.remove --version "$version" --install-location / \
  "$@" \
  "$app/Contents/Resources/Remove TailSplit Network Service.pkg"
chmod 644 "$app/Contents/Resources/"*.pkg

if test "$signing_mode" = developer-id; then
  for package in "$app/Contents/Resources/"*.pkg; do
    signature=$(pkgutil --check-signature "$package")
    printf '%s\n' "$signature" | /usr/bin/grep -F 'Developer ID Installer:' |
      /usr/bin/grep -Fq "($team)" ||
      { echo "Installer package is not signed by the expected Developer ID team." >&2; exit 1; }
  done
fi
if test "$notarize" = 1; then
  notarize_file "$app/Contents/Resources/TailSplit Network Service.pkg" install
  sh "$project/scripts/staple-macos.sh" "$app/Contents/Resources/TailSplit Network Service.pkg"
  notarize_file "$app/Contents/Resources/Remove TailSplit Network Service.pkg" remove
  sh "$project/scripts/staple-macos.sh" "$app/Contents/Resources/Remove TailSplit Network Service.pkg"
fi
sign_code "$app"
codesign --verify --deep --strict "$app"

if test "$notarize" = 1; then
  ditto -c -k --keepParent "$app" "$stage/notarize-app.zip"
  notarize_file "$stage/notarize-app.zip" app
  sh "$project/scripts/staple-macos.sh" "$app"
fi
ditto -c -k --keepParent "$app" "$output/$artifact.zip"

mkdir "$stage/dmg"
ditto "$app" "$stage/dmg/TailSplit.app"
ln -s /Applications "$stage/dmg/Applications"
cp "$project/packaging/macos/Install.txt" "$stage/dmg/Read Me.txt"
chmod 755 "$stage/dmg"
chmod 644 "$stage/dmg/Read Me.txt"
hdiutil create -volname TailSplit -srcfolder "$stage/dmg" \
  -format UDZO -ov "$output/$artifact.dmg"
# Build staging uses a private umask; distributable files should be readable.
chmod 644 "$output/$artifact.dmg" "$output/$artifact.zip"
if test "$signing_mode" = developer-id; then
  # Hardened runtime is an executable flag, not a disk image option.
  if test -n "${APPLE_KEYCHAIN:-}"; then
    codesign --force --sign "$application_identity" --keychain "$APPLE_KEYCHAIN" \
      --timestamp "$output/$artifact.dmg"
  else
    codesign --force --sign "$application_identity" --timestamp "$output/$artifact.dmg"
  fi
fi
if test "$notarize" = 1; then
  notarize_file "$output/$artifact.dmg" dmg
  sh "$project/scripts/staple-macos.sh" "$output/$artifact.dmg"
fi
"$python" "$project/scripts/verify-macos.py" "$app" \
  --dmg "$output/$artifact.dmg" --zip "$output/$artifact.zip" \
  --signing-mode "$signing_mode" --team "$team" --notarized "$notarize" \
  --receipt "$output/$artifact.json"
(cd "$output" && shasum -a 256 "$artifact.dmg" "$artifact.zip" > "$artifact.sha256")
chmod 644 "$output/$artifact."*
echo "$app"
echo "$output/$artifact.dmg"
