#!/bin/sh
set -eu

test "${GITHUB_ACTIONS:-}" = true || {
  echo "The privileged TUN DNS fixture is restricted to disposable CI runners." >&2
  exit 1
}
scratch=$(mktemp -d "${RUNNER_TEMP:?}/tailsplit-tun.XXXXXX")
trap 'rm -rf "$scratch"' EXIT HUP INT TERM
test_binary="$scratch/tun-dns.test"
go test -c -race -tags with_gvisor -o "$test_binary" .
case "$(uname -s)" in
  Linux)
    # No host routes or DNS services enter this namespace. The dummy default
    # route makes an accidental system-wide route takeover observable.
    sudo -n env GITHUB_ACTIONS=true TAILSPLIT_PRIVILEGED_TEST=1 \
      unshare --net sh -eu -c '
        ip link set lo up
        ip link add fixture0 type dummy
        ip addr add 198.51.100.2/24 dev fixture0
        ip link set fixture0 up
        ip route add default via 198.51.100.1 dev fixture0
        exec "$1" -test.run "^TestPrivilegedTUNDNS$" -test.v -test.timeout 45s
      ' tailsplit-tun-test "$test_binary"
    ;;
  Darwin)
    # This fixture creates only the DNS TUN and its two peer routes. It never
    # publishes system DNS or enables Internet-traffic capture on the runner.
    sudo -n env GITHUB_ACTIONS=true TAILSPLIT_PRIVILEGED_TEST=1 \
      "$test_binary" -test.run '^TestPrivilegedTUNDNS$' -test.v -test.timeout 45s
    ;;
  *) echo "Unsupported CI platform" >&2; exit 1 ;;
esac
