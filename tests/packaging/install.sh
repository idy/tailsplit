#!/usr/bin/env bash
# Run on a disposable native Debian runner, never on an operator's installed app.
set -euo pipefail
cd "$(dirname "$0")/../.."
[ "$(id -u)" = 0 ] || { printf 'Debian lifecycle checks require root\n' >&2; exit 1; }
arch=$(dpkg --print-architecture)
version=$(tr -d '\n' < VERSION)
commit=$(git rev-parse HEAD)
package=${1:-bin/deb/$arch}/tailsplit_${version}_${arch}.deb
[ -f "$package" ]
if dpkg-query -W -f='${Status}' tailsplit 2>/dev/null | grep -q ' installed$'; then
  printf 'Refusing to replace an existing TailSplit package\n' >&2
  exit 1
fi
[ "$(dpkg-deb -f "$package" Package)" = tailsplit ]
[ "$(dpkg-deb -f "$package" Version)" = "$version" ]
[ "$(dpkg-deb -f "$package" Architecture)" = "$arch" ]
[ "$(dpkg-deb -f "$package" Depends)" = ca-certificates ]
work=$(mktemp -d)
installed=0
cleanup() {
  if [ "$installed" = 1 ]; then dpkg --purge tailsplit; fi
  rm -rf "$work"
}
trap cleanup EXIT
mkdir "$work/control"
dpkg-deb -e "$package" "$work/control"
# No scripts, triggers, conffiles, or services may activate networking on install.
[ "$(find "$work/control" -type f | wc -l | tr -d ' ')" = 1 ]
[ -f "$work/control/control" ]
dpkg-deb --fsys-tarfile "$package" | tar -tf - > "$work/contents"
while IFS= read -r entry; do
  case "$entry" in
    ./|./usr/|./usr/bin/|./usr/bin/tailsplit|./usr/share/|./usr/share/doc/|./usr/share/doc/tailsplit/|./usr/share/doc/tailsplit/*) ;;
    *) printf 'Unexpected package path: %s\n' "$entry" >&2; exit 1 ;;
  esac
done < "$work/contents"
export HOME=$work/home
mkdir -p "$HOME/.config/tailsplit" "$HOME/.local/share/letsolo/tailsplit"
printf 'user-owned configuration\n' > "$HOME/.config/tailsplit/config.yaml"
printf 'persistent-device-identity\n' > "$HOME/.local/share/letsolo/tailsplit/device.state"
(cd "$HOME" && find . -type f -exec sha256sum {} + | sort) > "$work/before"
check_user_state() {
  (cd "$HOME" && find . -type f -exec sha256sum {} + | sort) > "$work/after"
  cmp "$work/before" "$work/after"
}
installed=1
dpkg -i "$package"
/usr/bin/tailsplit version | tee "$work/version"
grep -F "tailsplit $version (commit $commit;" "$work/version"
[ -s /usr/share/doc/tailsplit/copyright ]
[ -s /usr/share/doc/tailsplit/THIRD_PARTY_NOTICES.md ]
[ -s /usr/share/doc/tailsplit/examples/config.yaml ]
python3 -I - "$version" "$commit" "$arch" <<'PY'
import json
import sys
from pathlib import Path
metadata = json.loads(Path('/usr/share/doc/tailsplit/build.json').read_text())
assert metadata == dict(zip(('version', 'commit', 'architecture'), sys.argv[1:]))
notices = Path('/usr/share/doc/tailsplit/THIRD_PARTY_NOTICES.md').read_text()
for expected in ('github.com/sagernet/sing-box', 'github.com/sagernet/tailscale', 'Go standard library', 'Embedded GFWList'):
    assert expected in notices, expected
PY
/usr/bin/tailsplit init -c "$work/generated.yaml"
[ "$(stat -c %a "$work/generated.yaml")" = 600 ]
/usr/bin/tailsplit check -c "$work/generated.yaml"
if /usr/bin/tailsplit init -c "$work/generated.yaml"; then
  printf 'init overwrote an existing file\n' >&2
  exit 1
fi
if pgrep -x tailsplit >/dev/null; then
  printf 'Package commands left a running TailSplit process\n' >&2
  exit 1
fi
check_user_state
dpkg --remove tailsplit
[ ! -e /usr/bin/tailsplit ]
check_user_state
# Purge after removal as well as purge of an installed package must retain user data.
dpkg --purge tailsplit
check_user_state
dpkg -i "$package"
dpkg --purge tailsplit
installed=0
[ ! -e /usr/bin/tailsplit ]
check_user_state
printf 'Verified Debian install, remove and purge: %s/%s %s\n' "$arch" "$version" "$commit"
