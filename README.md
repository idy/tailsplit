# TailSplit

An open-source desktop app for macOS and Windows, with a cross-platform CLI for
domain and IP routing. TailSplit embeds sing-box, GFWList projection and userspace
Tailscale. Choose which traffic goes direct, through a proxy exit, or to authorized
Tailnet peers.

[Project page](https://tailsplit.idy.dev) · [Releases](https://github.com/idy/tailsplit/releases) · [Source](https://github.com/idy/tailsplit)

## Install

On macOS and Linux, the shell installer selects your platform and architecture,
downloads a public GitHub Release and checks SHA-256 before installing. Windows
uses a native desktop Setup EXE or the standalone CLI ZIP described below:

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
curl -fsSL https://raw.githubusercontent.com/idy/tailsplit/main/install.sh | sh -s -- --version 0.6.1 --cli --prefix "$HOME/.local" --no-sudo
```

`--app-dir` selects a macOS app directory; `--method binary` forces a CLI archive.
The installer never changes your routing, DNS, proxy rules or device identity.
Quit the existing app before updating its bundle. Existing configuration and
network-service state live outside the app bundle and are retained.

| Platform | Architectures | Downloads |
| --- | --- | --- |
| macOS 14+ | amd64, arm64 | App ZIP/DMG and CLI tar.gz |
| Linux | amd64, arm64 | CLI tar.gz and .deb |
| Windows 10+ (x64), Windows 11 (ARM64) | amd64, arm64 | Desktop Setup EXE and CLI ZIP |

## Windows desktop

Download the Windows desktop Setup EXE for x64 (`amd64`) or ARM64 from
[GitHub Releases](https://github.com/idy/tailsplit/releases). It provides a native
application window and notification-area controls. The installer puts
`TailSplit.exe`, `tailsplit-core.exe` and `TailSplit.Window.dll` in protected
`Program Files\\TailSplit` and installs an idle SYSTEM network service. The UI
runs as a normal user; only installation and service maintenance request UAC.

The window contains connection, Tailnet login/logout, exit selection, configuration
and bounded diagnostic logs. Settings stay in the window; device authorization
opens the default browser. Closing the window hides it to the tray. Quit first
disconnects and waits for the network service to clean up. Windows login startup
is optional and separate from automatic connection.

Configuration remains in the user's `TailSplit` configuration directory. The
service stores per-SID runtime state in protected `ProgramData\\TailSplit` and
imports existing private CLI state once as bounded contents. It retains its
initialization marker through logout, upgrade and uninstall. A live desktop
session holds the legacy state lock, so the CLI cannot use that identity at the
same time. Upgrades and default uninstall retain user configuration, registration
and device state.

The desktop uses Microsoft Edge WebView2. Setup includes and checks the matching
runtime prerequisite. Windows executables are currently unsigned; verify the
release checksums. Desktop releases require native GUI, service and installation
lifecycle checks on both architectures, including actual app screenshots, real
connection controls, service cleanup, repair and uninstall.

For source builds on the matching Windows architecture, the release builder
collects native toolchain inputs, notices and installer payloads:

```sh
python -I -B packaging/release.py build --os windows --arch amd64 --directory bin/release/assets
```

Use `arm64` on Windows on Arm. Windows CI is configured to execute the installed UI at medium
integrity, captures its native window, exercises real service sessions and tests
repair, failed-upgrade recovery and uninstall. This is separate from cross-build
checks and the CLI's real Wintun TCP/UDP DNS fixture.

## Windows CLI

Download the Windows ZIP for x64 (`amd64`) or ARM64 and extract it. The archive
contains `tailsplit.exe`, its license, dependency notices and source metadata.
The standalone ZIP provides the command line interface. Use the Windows desktop
Setup EXE for the native window, tray and configuration panel.

From PowerShell in the extracted directory:

```powershell
.\tailsplit.exe version
.\tailsplit.exe init -c .\config.yaml
.\tailsplit.exe check -c .\config.yaml
```

These commands work without starting a network connection. Configure your proxy
or authorized Tailnet destination, then use an **Administrator** terminal for
`tailsplit.exe run -c .\config.yaml`. Both connection modes create a DNS TUN;
Windows adapter, route and DNS changes require elevation. Ctrl+C stops the
foreground session and removes its managed networking. Keep the existing user
state directory when upgrading; its Windows ACL is restricted to the current
user, SYSTEM and administrators, with a process lock preventing concurrent use.
The Wintun driver is bundled, with its redistribution terms in the notices;
no additional driver download is required. The CLI executable is not
Authenticode-signed; verify the release's `SHA256SUMS` before running it.

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

### Configuration panel and logging

Choose **Settings and Tools → Configuration Panel…** (`设置与工具 → 配置面板…`)
from the menu bar app. The page groups connection, DNS, routing and startup
settings, with a live YAML preview and a full source editor. Validate or save/apply
changes from either view. Saving reconnects an active proxy; an idle app stays
idle. Existing comments and unrelated settings are preserved. External edits
cause a revision conflict, and failed edits retain the browser draft. App service
checks run before saving: the application proxy port must be at least 1024, the
system chooses the TUN name, and the app uses interactive or saved login rather
than environment authentication keys.

The app owns the page on a random `127.0.0.1` port and closes it on exit. Its
per-run capability travels in the URL fragment and then an Authorization header.
Host/Origin checks and embedded assets keep the panel local; it does not expose
Tailnet login links or device credential state. Reopen it from the menu after
restarting the app.

**Detailed Logging (Debug)** (`详细日志（Debug）`) in the same menu saves
`log_level: debug` and reconnects an active proxy. Disable it to restore
`log_level: info`. Use **View Logs…** (`查看日志…`) to inspect DNS, routing and
connection diagnostics.

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

DNS is always served inside the TUN at `172.31.255.2:53` and
`[fd74:6169:6c73::2]:53`, including when `tun.enabled: false`. The switch only
controls whole-system traffic capture and system DNS publishing. DNS listener
addresses and ports are fixed. `dns.listen` is unsupported and fails validation;
remove that field from older YAML files.

Edit routing and DNS rules in the YAML. GFWList and the separately documented
supplemental rules are bundled; rule refresh remains an explicit operation.
Credentials and persistent device state belong in user-controlled files, not in
this repository. Keep existing configuration/state directories during upgrades.

If the physical uplink has no IPv6 route, set `dns.direct_ipv6: false`. In split
mode, direct Internet DNS and hostname dialing then use IPv4. For public IPv6
addresses cached or resolved by an application, the direct outbound can recover
the TLS/QUIC hostname and resolve it through the physical IPv4 DNS path after
routing has selected direct. Domain/CIDR priority, local IPv6 and global proxy
behavior are preserved. Recovery fails explicitly when the hostname or IPv4
answer is unavailable; it never falls back to a proxy exit. Encrypted or absent
hostnames cannot be recovered this way.

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
need real provider or signing credentials. When Node.js 18 or later is available,
`make test` also runs deterministic editor state regression tests; no npm packages
are required.

For a native Windows CLI source build, use Go directly. The supported Windows
CLI test packages include state ACL and cross-process lock checks:

```powershell
go test -mod=readonly -tags with_gvisor . ./cmd/tailsplit ./internal/privatefs ./internal/gfwlistdns
go build -mod=readonly -tags with_gvisor -o tailsplit.exe ./cmd/tailsplit
```

The privileged Windows TUN fixture is opt-in and restricted to disposable,
elevated GitHub runners. Normal local tests do not create adapters or change
network routes. The macOS service and configuration-panel integration tests are
covered by their macOS/Linux CI jobs.

To preview the configuration panel without starting a proxy:

```sh
sh scripts/preview-config-web.sh
```

This command creates a private copy of `config.example.yaml` under
`bin/config-web-preview/` if needed and prints a capability URL for
`127.0.0.1:19790`. Saves affect only that preview file; no TUN, login or installed
network service is started. The page assets are embedded and need no npm or CDN.

## CI releases

A `vX.Y.Z` tag matching root `VERSION` runs
[the multi-platform release workflow](.github/workflows/release.yml). Linux,
macOS and Windows amd64/arm64 jobs build natively and the publisher waits for all
six platform/architecture builds.
Public releases include:

- `tailsplit_X.Y.Z_{linux,darwin}_{amd64,arm64}.tar.gz`
- `tailsplit_X.Y.Z_windows_{amd64,arm64}.zip`
- `TailSplit_X.Y.Z_windows_{amd64,arm64}_setup.exe`
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
