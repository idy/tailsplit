#!/usr/bin/env bash
# Build the standalone CLI package; never ship configuration or device state.
set -euo pipefail
cd "$(dirname "$0")/.."
go=${GO:-go}
python=${PYTHON:-python3}
version=$("$python" -I -c 'from pathlib import Path; import re; value=Path("VERSION").read_text().strip(); assert re.fullmatch(r"[0-9]+\.[0-9]+\.[0-9]+", value), "Invalid VERSION"; print(value)')
arch=${GOARCH:-$("$go" env GOARCH)}
case "$arch" in
  amd64) export GOAMD64=v1 ;;
  arm64) export GOARM64=v8.0 ;;
  *) printf 'Unsupported architecture: %s\n' "$arch" >&2; exit 1 ;;
esac
commit=$(git rev-parse HEAD)
expected_commit=${RELEASE_SOURCE_COMMIT:-${GITHUB_SHA:-$commit}}
[[ $commit =~ ^[0-9a-f]{40}$ ]] && [ "$expected_commit" = "$commit" ] || {
  printf 'Package source must match the selected source commit\n' >&2; exit 1;
}
export GOWORK=off CGO_ENABLED=0 GOOS=linux GOARCH="$arch"
export SOURCE_DATE_EPOCH
SOURCE_DATE_EPOCH=$(git show -s --format=%ct HEAD)
output=$(pwd)/bin/deb/$arch
mkdir -p "$output"
stage=$(mktemp -d "$output/.tailsplit-deb.XXXXXX")
trap 'rm -rf "$stage"' EXIT
root=$stage/tailsplit
install -d "$root/DEBIAN" "$root/usr/bin" "$root/usr/share/doc/tailsplit/examples"
"$go" build -mod=readonly -tags with_gvisor -trimpath -buildvcs=false \
  -ldflags "-s -w -X main.version=$version -X main.commit=$commit" \
  -o "$root/usr/bin/tailsplit" ./cmd/tailsplit
notices=${THIRD_PARTY_NOTICES_FILE:-$stage/THIRD_PARTY_NOTICES.md}
if [ -z "${THIRD_PARTY_NOTICES_FILE:-}" ]; then
  "$python" -I -B packaging/licenses.py --os linux --arch "$arch" --output "$notices"
fi
install -m 0644 LICENSE "$root/usr/share/doc/tailsplit/copyright"
install -m 0644 "$notices" "$root/usr/share/doc/tailsplit/THIRD_PARTY_NOTICES.md"
install -m 0644 config.example.yaml "$root/usr/share/doc/tailsplit/examples/config.yaml"
printf '{"version":"%s","commit":"%s","architecture":"%s"}\n' "$version" "$commit" "$arch" > "$root/usr/share/doc/tailsplit/build.json"
cat > "$root/DEBIAN/control" <<CONTROL
Package: tailsplit
Version: $version
Architecture: $arch
Maintainer: TailSplit contributors <idy@users.noreply.github.com>
Section: net
Priority: optional
Depends: ca-certificates
Homepage: https://tailsplit.idy.dev
Description: Rule-based network routing with embedded sing-box and Tailscale
CONTROL
find "$root" -exec touch -h -d "@$SOURCE_DATE_EPOCH" {} +
dpkg-deb --build --root-owner-group --uniform-compression -Zxz --threads-max=1 \
  "$root" "$output/tailsplit_${version}_${arch}.deb"
(cd "$output" && sha256sum "tailsplit_${version}_${arch}.deb" > SHA256SUMS)
