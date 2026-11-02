# TailSplit

An open-source macOS menu bar app and Linux/macOS CLI for domain and IP routing.
TailSplit embeds sing-box, GFWList projection and userspace Tailscale. Choose
which traffic goes direct, through a proxy exit, or to authorized Tailnet peers.

[Project page](https://tailsplit.idy.dev) · [Releases](https://github.com/idy/tailsplit/releases) · [Source](https://github.com/idy/tailsplit)

## Install

The installer selects your platform and architecture, downloads a public GitHub
Release and checks SHA-256 before installing:

```sh
curl -fsSL https://tailsplit.idy.dev/install.sh | sh
```

It is also available directly from this repository:

```sh
curl -fsSL https://raw.githubusercontent.com/idy/tailsplit/main/install.sh | sh
```

On macOS it installs the app into your Applications directory. Linux prefers a
.deb when apt/dpkg and administrator access are available, otherwise it installs
the CLI tarball. To select a specific CLI release and a user-local prefix:

```sh
curl -fsSL https://raw.githubusercontent.com/idy/tailsplit/main/install.sh | sh -s -- --version 0.5.1 --cli --prefix "$HOME/.local" --no-sudo
```

`--app-dir` selects a macOS app directory; `--method binary` forces a CLI archive.
The installer never changes your routing, DNS, proxy rules or device identity.
Quit the existing app before updating its bundle. Existing configuration and
network-service state live outside the app bundle and are retained.

| Platform | Architectures | Downloads |
| --- | --- | --- |
| macOS 14+ | amd64, arm64 | App ZIP/DMG and CLI tar.gz |
| Linux | amd64, arm64 | CLI tar.gz and .deb |

## macOS app

Open `TailSplit.app` normally. It starts in the menu bar, disconnected. Review
your YAML configuration and choose when to connect. The first connection opens
the bundled installer for the privileged network service; an administrator
approves it once. Both connection modes use that service for TUN DNS.

- **Application proxy:** applications opt into local HTTP/SOCKS listeners. A
  narrow DNS TUN route is created; the system proxy and default traffic route
  remain unchanged.
- **System routing:** `tun.enabled` adds system-traffic routing and DNS
  interception. Rules apply to captured connections.

Login and exit discovery do not create a TUN. Connected operation uses a DNS
TUN in both modes. Unavailable selected exits block their traffic while that
TUN is active; manual disconnect or app exit restores the network it managed.

The app keeps `im.lbv.tailsplit` identities and existing service labels for
upgrade compatibility. Check each release's receipt for Developer ID signing
and notarization status. Ad-hoc developer builds are not notarized and macOS
may require an explicit launch approval.

## CLI configuration

Use [config.example.yaml](config.example.yaml) as a starting point:

```sh
tailsplit init -c config.yaml
tailsplit check -c config.yaml
tailsplit render -c config.yaml
tailsplit version
```

`init` creates without overwriting an existing file. `check` validates offline;
`render` shows the embedded-core configuration. Connecting requires appropriate
network privileges and a configured proxy or authorized Tailnet destination:

```sh
sudo tailsplit run -c config.yaml
```

Edit routing and DNS rules in the YAML. GFWList and the separately documented
supplemental rules are bundled; rule refresh remains an explicit operation.
Credentials and persistent device state belong in user-controlled files, not in
this repository. Keep existing configuration/state directories during upgrades.

## Build and test

Go 1.26.2 or later and Python 3.11 or later are required. macOS app builds need
Xcode Command Line Tools, Swift and the standard macOS packaging tools. Build
commands run at this repository root; no sibling monorepo is needed:

```sh
make test check
python3 -B -m unittest discover -s scripts -p 'test_*.py'
python3 -B -m unittest discover -s tests/installer
python3 -B -m unittest discover -s tests/packaging
make app
```

The core uses the `with_gvisor` build tag; the Makefile supplies it. `make app`
creates a local ad-hoc app, ZIP, DMG, checksums and verification receipt. Linux
CLI Debian packages can be built on Linux with `GOARCH=amd64 make deb` or
`GOARCH=arm64 make deb`; these require Bash, GNU coreutils and `dpkg-deb`.
Source tests use loopback fixtures and synthetic signing inputs; they do not
need real provider or signing credentials.

## CI releases

A `vX.Y.Z` tag matching root `VERSION` runs
[the multi-platform release workflow](.github/workflows/release.yml). Linux and
macOS amd64/arm64 jobs build natively and the publisher waits for all platforms.
Public releases include:

- `tailsplit_X.Y.Z_{linux,darwin}_{amd64,arm64}.tar.gz`
- `tailsplit_X.Y.Z_linux_{amd64,arm64}.deb`
- `TailSplit_X.Y.Z_darwin_{amd64,arm64}.zip` and `.dmg`
- Per-architecture macOS signing receipts, `release-manifest.json` and `SHA256SUMS`

CLI archives include source metadata, the project license and detailed
third-party notices. CI checks asset hashes and complete platform coverage
before publishing. Existing release assets are never silently overwritten.

Developer ID signing and notarization use externally supplied encrypted inputs
and Actions secrets; no certificate, private key, Apple API key or encrypted
credential payload is committed here. See [signing setup](credentials/README.md).
Without configured signing, macOS builds are explicitly recorded as ad-hoc.
The public site is deployed independently from `site/` by its Pages workflow;
the root installer is published as `/install.sh` alongside it.

## License and source provenance

TailSplit is GPL-3.0-or-later; see [LICENSE](LICENSE) and
[third-party notices](THIRD_PARTY_NOTICES.md). The embedded GFWList snapshot
retains its LGPL-2.1 license and source provenance under
`internal/gfwlistdns/data/`.

The initial public application tree was imported from `letsolo/tailsplit`
without its private signing files or private credential history. The shared
parser was copied into this project, while its original remains available to
other monorepo components. Runtime bundle IDs, service labels and user state
paths retain their existing values for upgrades.
