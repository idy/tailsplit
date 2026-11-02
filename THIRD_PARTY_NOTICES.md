# Third-party notices

TailSplit is distributed under GPL-3.0-or-later. Its embedded networking core is
[sing-box](https://github.com/SagerNet/sing-box), Copyright 2022 nekohasekai,
licensed under GPL-3.0-or-later; its upstream notice is preserved in
`LICENSES/sing-box.txt`. TailSplit uses its own project name and does not imply
endorsement by the upstream project.

The embedded GFWList snapshot is licensed under LGPL-2.1. Its unmodified license,
upstream revision and source provenance are retained in
`internal/gfwlistdns/data/COPYING.txt` and `internal/gfwlistdns/data/README.md`.
The separately authored supplemental rules are identified in that provenance
file. The Tailscale implementation and other Go modules retain their respective
licenses; exact module versions and source locations are in `go.mod` and `go.sum`.

Windows CLI builds include the unmodified, signed Wintun 0.14.1 DLL for the
selected architecture, embedded by sing-tun. Those prebuilt binaries have separate
redistribution terms from the MIT-licensed Go bindings. Their complete license,
official source URL and archive/DLL hashes are preserved in `LICENSES/wintun.txt`
and included in the generated Windows notices. No extra driver download is made.

Release builds generate detailed dependency notices for the target platform.
The project license, dependency notices and build metadata accompany CLI
archives and Debian packages. The macOS app also carries the networking-core and
GFWList notices in its Resources directory. The full corresponding project
source is available in this repository.
