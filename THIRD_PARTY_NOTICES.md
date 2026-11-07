# Third-party notices

TailSplit is distributed under GPL-3.0-or-later. Its embedded networking core is
[sing-box](https://github.com/SagerNet/sing-box), Copyright 2022 nekohasekai,
licensed under GPL-3.0-or-later; its upstream notice is preserved in
`LICENSES/sing-box.txt`. TailSplit uses its own project name and does not imply
endorsement by the upstream project.

GFWList is downloaded from upstream on demand and cached on disk. The upstream
list is not included in this repository or release packages. Its upstream license
is [LGPL-2.1](https://www.gnu.org/licenses/old-licenses/lgpl-2.1.html).

The Tailscale implementation and other Go modules retain their respective
licenses; exact module versions and source locations are in `go.mod` and `go.sum`.

Windows CLI builds include the unmodified, signed Wintun 0.14.1 DLL for the
selected architecture, embedded by sing-tun. Those prebuilt binaries have separate
redistribution terms from the MIT-licensed Go bindings. Their complete license,
official source URL and archive/DLL hashes are preserved in `LICENSES/wintun.txt`
and included in the generated Windows notices. No extra driver download is made.

Release builds generate detailed dependency notices for the target platform.
The project license, dependency notices and build metadata accompany CLI
archives and Debian packages. The macOS app also carries the networking-core and
dependency notices in its Resources directory. The full corresponding project
source is available in this repository.
