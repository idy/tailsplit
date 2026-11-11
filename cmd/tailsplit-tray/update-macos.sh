#!/bin/sh
# Runs outside the desktop process, optionally through macOS authorization.
# Arguments are passed as values; release content never supplies shell commands.
set -eu
work=$1
target=$2
parent_pid=$3
user_id=$4
config=$5
log=$6
requirement=$7
expected_version=$8
stage=
previous=0
activated=0
connection_arg=--no-connect

launch_app() {
    app=$1
    shift
    if [ "$(/usr/bin/id -u)" -eq 0 ]; then
        /bin/launchctl asuser "$user_id" /usr/bin/sudo -u "#$user_id" /usr/bin/open -n "$app" --args --config "$config" --log "$log" "$connection_arg" "$@"
    else
        /usr/bin/open -n "$app" --args --config "$config" --log "$log" "$connection_arg" "$@"
    fi
}

cleanup() {
    result=$?
    trap - EXIT
    if [ "$previous" -eq 1 ] && [ "$activated" -eq 0 ]; then
        /bin/mv "$stage/previous.app" "$target" || exit 1
    fi
    if [ -n "$stage" ]; then /bin/rm -rf "$stage"; fi
    if [ "$result" -ne 0 ] && [ -f "$work/proceed" ] && ! /bin/kill -0 "$parent_pid" 2>/dev/null; then
        launch_app "$target" --update-failed || true
    fi
    /bin/rm -rf "$work"
    exit "$result"
}
trap cleanup EXIT
trap 'exit 1' HUP INT TERM

[ -d "$target" ] && [ ! -L "$target" ]
[ "$(/usr/bin/plutil -extract CFBundleIdentifier raw -o - "$target/Contents/Info.plist")" = im.lbv.tailsplit ]
# Stage on the target filesystem so activation and rollback are whole renames.
stage=$(/usr/bin/mktemp -d "$(/usr/bin/dirname "$target")/.tailsplit-update.XXXXXXXX")
/bin/chmod 700 "$stage"
/usr/bin/ditto "$work/extracted/TailSplit.app" "$stage/TailSplit.app"
[ "$(/usr/bin/plutil -extract CFBundleShortVersionString raw -o - "$stage/TailSplit.app/Contents/Info.plist")" = "$expected_version" ]
# Reverify after copying into protected staging, including when authorized as root.
# A leading "=" makes codesign read the requirement as text, not a file path.
/usr/bin/codesign --verify --deep --strict -R "=$requirement" "$stage/TailSplit.app"
/usr/sbin/spctl --assess --type execute "$stage/TailSplit.app"
: > "$work/ready"

# A ready marker alone is not permission to replace the app. The desktop writes
# proceed only after it has closed its controller, then exits normally.
count=0
while /bin/kill -0 "$parent_pid" 2>/dev/null; do
    count=$((count + 1))
    [ "$count" -lt 90 ] || exit 1
    /bin/sleep 1
done
[ -f "$work/proceed" ] || exit 1
case "$(/bin/cat "$work/proceed")" in
    --start) connection_arg=--start ;;
    --no-connect) connection_arg=--no-connect ;;
    *) exit 1 ;;
esac
[ -d "$target" ] && [ ! -L "$target" ]
/bin/mv "$target" "$stage/previous.app"
previous=1
/bin/mv "$stage/TailSplit.app" "$target"
activated=1
if ! launch_app "$target"; then
    # A failed launch handoff restores the old bundle before relaunching it.
    /bin/mv "$target" "$stage/failed.app"
    activated=0
    exit 1
fi
