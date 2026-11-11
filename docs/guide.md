# TailSplit guide

[Back to the project overview](../README.md). Commands and file paths in this guide are relative to the repository root.

TailSplit is an open-source routing client based on sing-box rule lists. It
supports multiple Tailscale networks: switch between networks and access the
internet through your chosen Tailscale exit node. Available as a desktop app for
macOS and Windows, with a cross-platform CLI.

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
curl -fsSL https://raw.githubusercontent.com/idy/tailsplit/main/install.sh | sh -s -- --version 0.7.16 --cli --prefix "$HOME/.local" --no-sudo
```

`--app-dir` selects a macOS app directory; `--method binary` forces a CLI archive.
The installer never changes your routing, DNS, proxy rules or device identity.
Quit the existing app before updating its bundle with the shell installer.
Desktop apps also offer the confirmed update flow described below. Existing
configuration and network-service state live outside the app bundle and are retained.

| Platform | Architectures | Downloads |
| --- | --- | --- |
| macOS 14+ | amd64, arm64 | App ZIP/DMG and CLI tar.gz |
| Linux | amd64, arm64 | CLI tar.gz and .deb |
| Windows 10+ (x64), Windows 11 (ARM64) | amd64, arm64 | Desktop Setup EXE and CLI ZIP |

## Desktop updates

**macOS 0.7.0 needs one manual update.** Its updater fails at the code-signature
check before replacing the app. Quit TailSplit, then install the latest macOS app
from [GitHub Releases](https://github.com/idy/tailsplit/releases/latest) or use the
shell installer above. Configuration and device login are retained. In-app updates
work from 0.7.1 onward; Windows 0.7.0 is unaffected.

Packaged macOS and Windows apps check the stable GitHub Release ten seconds after
startup and every five minutes while running. Choose **Check for updates…** in the
tray menu (under **Settings and Tools** on macOS), or open **Settings → About & updates** in the configuration panel. The title-row update button shows
download percentage and installation state without adding a second row. The panel's App Updates settings show the installed
version, release notes, last check and errors. Development builds skip
automatic checks; prereleases and older versions are never offered.

After validating release metadata once, automatic checks inspect GitHub's public
latest-release redirect every five minutes and reuse cached metadata when the tag
is unchanged. Only a changed tag or a manual check revalidates the API metadata,
using its ETag when available. This avoids spending the shared unauthenticated API
quota on routine probes. Checks honor server retry/reset deadlines and back off
on rate limits; cached offers remain visible. Invalid release metadata, such as a
release whose assets are still publishing, is retried at most every 15 minutes
for the same tag while five-minute public probes continue. New tags retry
immediately; a manual check bypasses this local validation delay. Cache and
backoff are process-local, and same-tag release-note edits appear after a manual
check or app restart.

`app.check_for_updates` defaults to `true` when omitted. The menu and panel can
change it immediately without reconnecting the proxy. Checks only discover new
versions: installation always asks for confirmation. Before that confirmation,
the app refreshes the latest stable release so an older cached offer does not
force an intermediate upgrade. Any supported older version can jump directly to
the latest stable version. If that recheck fails, installation stops instead of
using a stale offer. After confirmation, the download stays pinned to that exact
version. Downloads use the matching architecture's desktop package and must match
the release's `SHA256SUMS` and size.
Network failures or failed verification leave the existing app running.

On Windows, the verified Setup EXE requests UAC and uses its existing graceful
shutdown, service upgrade and rollback flow; the next app launch removes the
downloaded Setup. Windows CI runs the updater's
installer handoff natively and silently upgrades a running, connected desktop.
Hosted runners cannot approve UAC or click through the Setup wizard; check those
steps manually on Windows. On macOS, the updater verifies the
bundle version, Developer ID signature and Gatekeeper acceptance, stages a whole
bundle on the destination filesystem, then disconnects, replaces and relaunches
the app. A signed installation requires the same signing team. Non-writable app
directories request administrator authorization before shutdown. Failed activation
or launch handoff restores the previous bundle. Configuration and saved device
identity are retained. In-app updates restore the connection switch at shutdown:
active traffic sessions reconnect; disconnected, discovery-only and sign-in-only
sessions stay disconnected. All active Global sessions refuse installation while
retaining their current protection: explicitly disconnect before updating, then
reconnect manually afterward. This also protects against an exit going offline
during replacement; the desktop cannot keep its TUN across process replacement.
Split reconnection is bound to the active configuration revision and the helper's
actually compiled rule projection, including local files and cached subscriptions. An external edit before shutdown stops the update;
an edit during replacement prevents automatic reconnection until you review the
saved configuration. An acknowledged disconnect during cleanup also cancels
reconnection. This one-time restart choice does not change `app.connect_on_start`.
The same choice is used when macOS relaunches the previous bundle after a failed
activation. Manual Windows installations still open disconnected. Older updaters
that always pass `--no-connect` need one manual reconnect after updating to a
version with connection-state recovery. On startup, macOS checks an already
installed network service before restoring device status. A
matching live service is reused; an outdated or unreachable installed service
opens the bundled Installer at restart, including after a `--no-connect` update
relaunch. While preparation waits, its authorization state and Cancel connection
control remain visible; canceling releases the operation gate for offline edits
and a repair retry. Automatic connection uses the helper's existing readiness
check instead of repeating the parent's checksum and probe. macOS administrator
approval remains required. Explicit Connect, Sign in and Add exit requests also
check that the service matches the app, opening the bundled Installer if it is
missing, outdated or unreachable before continuing the requested action. This
lets a fresh installation authorize its first device before choosing a traffic
gateway. Background discovery, Ping and logout never open Installer. Unbundled
development binaries cannot install a service without its package.

## Windows desktop

Download the Windows desktop Setup EXE for x64 (`amd64`) or ARM64 from
[GitHub Releases](https://github.com/idy/tailsplit/releases). It provides a native
application window and notification-area controls through MyGo v0.8.3. The installer
puts `TailSplit.exe` and `tailsplit-core.exe` in protected `Program Files\\TailSplit`
and installs an idle SYSTEM network service. The UI runs as a normal user; only
installation and service maintenance request UAC.

The window contains saved exit devices, connection controls, configuration and
bounded diagnostic logs. On both macOS and Windows, **Add exit** creates a separate
embedded device and opens browser authorization to choose its Tailnet. The login
uses a private staged configuration: the new profile appears in the saved list
only after successful authentication. Canceling or failed authorization leaves no
provisional row; retry Add exit when ready. Each attempt proposes a fresh ID, and
the state owner exclusively reserves its new namespace before authentication.
A failed attempt keeps that namespace claimed so a retry cannot adopt an
unpublished authorization or overwrite another configuration's identity. Remote
administrative device records are not deleted by this mechanism. The page's
configuration revision is checked before staging and again before publication,
so conflicting edits cannot silently change the authorization target or replace
newer edits. Choose its exit
node afterward; the profile is named `<exit-device-name>@<network name>`.
Existing valid saved logins are restored automatically during device discovery;
explicit sign-out or expired/revoked authorization requires signing in again.
To use another network, add another exit. **Remove exit** at the bottom of each
picker disconnects, logs out only that client, removes its local profile, and
leaves the app stopped. Explicit final and route-group references remain; choose
replacements or edit those rules before reconnecting in Split mode. Tailnet
admin-console device records remain. In active Global mode, Add exit first asks
for consent to stop Kill-Switch protection and restore system networking; Cancel
or Escape leaves the session unchanged. After consenting, reconnect manually when
ready, including after a save or authorization failure. Save or discard any draft
before changing exits. Existing-device sign-in and cross-device Global gateway
changes obtain the same consent before a separate disconnect, and remain stopped
afterward. Native sign-in that would replace a protected helper requires an
explicit disconnect first; current-device authorization recovery reuses it.
Removing the
primary or last device preserves remaining identities and dormant policy; no
other device is promoted or automatically selected as its replacement. Primary
private DNS suffixes remain unresolved, and former primary custom domains become
a retained `main` route group. Resolve or explicitly remove that policy before
Split can reconnect, even when another default gateway is ready.
Closing the window hides it to the tray, whose icon turns green while connected. Quit first
disconnects and waits for the network service to clean up. Windows login startup
is optional and separate from automatic connection.

Configuration remains in the user's `TailSplit` configuration directory. The
service stores per-SID runtime state in protected `ProgramData\\TailSplit` and
imports primary private CLI state once as bounded contents. Additional exits are
imported independently when first encountered, including after a service upgrade;
existing service-owned or logged-out exits are never overwritten by an old CLI
copy. Initialization and per-exit authority survive logout, upgrade and uninstall. A live desktop
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

An optional `GH_TOKEN` (or `GITHUB_TOKEN`) authenticates public GitHub release metadata requests to avoid shared-IP rate limits. CI supplies its read-only token to Windows packaging steps. The downloader sends it only to `api.github.com`, does not forward it through redirects, and excludes it from downloaded tools and installed-app subprocesses.

Use `arm64` on Windows on Arm. Windows CI is configured to execute the installed UI at medium
integrity, captures its native window, exercises real service sessions and tests
repair, failed-upgrade recovery, upgrading a running and connected desktop with a
newer installer, and uninstall. This is separate from cross-build
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

These commands do not start proxy listeners, a TUN or Tailnet login. Configure
your proxy or authorized Tailnet destination, then use an **Administrator** terminal for
`tailsplit.exe run -c .\config.yaml`. Both connection modes create a DNS TUN;
Windows adapter, route and DNS changes require elevation. Ctrl+C stops the
foreground session and removes its managed networking. Keep the existing user
state directory when upgrading; its Windows ACL is restricted to the current
user, SYSTEM and administrators, with a process lock preventing concurrent use.
The Wintun driver is bundled, with its redistribution terms in the notices;
no additional driver download is required. The CLI executable is not
Authenticode-signed; verify the release's `SHA256SUMS` before running it.

## macOS app

Open `TailSplit.app` normally. A fresh installation starts in the menu bar with
no devices or gateway, so it stays idle until setup. Use Add exit, authorize the
device and choose its node and default gateway. New configurations enable
auto-connect for subsequent launches once a gateway is configured; the icon
turns green while connected. Review your routing settings before connecting. The
first Add exit, Sign in or connection opens the bundled installer if the
privileged network service is missing or does not match this app; complete its
administrator approval to continue the original action. The panel distinguishes
this wait from Tailscale browser authorization. Both connection modes use that
service for TUN DNS.

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

Choose **Open** (`打开`), the first item in the menu bar app's menu, to open the
configuration panel. The panel opens in TailSplit's MyGo v0.8.3 window, which
uses the system web view: WKWebView on macOS and WebView2 on Windows. A single
integrated title row contains the app
identity, current section, connection state and update button. Downloads show
their percentage inside that button; installation shows its current phase in
the same place. The title row supports window dragging and double-click zoom,
with native macOS window buttons and Windows minimize, maximize/restore and
close controls. Closing still hides the window to the menu bar or tray.
The sidebar puts **Exit nodes** and **Logs** at the top and pins **Settings** at
the bottom, expanding its categories in place. Exit Nodes shows one row per saved
device. Add exit is above the list on the left; its default-gateway dropdown is
on the right, without an exit count. Choosing a gateway saves immediately and
reconnects an active session; an idle app stays idle. The titlebar orders status,
**Rule-based / Global (Kill-Switch)**, the connection switch and Refresh at the
far right. When connecting without a default gateway, a dialog requires an
explicit choice and confirmation; Cancel or Escape changes nothing.

Click an exit row to open its list-only node picker, without search, manual node
entry, Refresh or bulk Ping. Confirm a node choice to save it, or cancel or press
Escape to keep the previous selection. A running session reconnects after
confirmation. Unavailable and missing saved nodes remain visible; TailSplit never
automatically chooses a replacement. Save or discard any settings draft before
applying exit or mode changes. The page and whole picker own scrolling, without
an independent card scrollbar; short pages fit without an unnecessary scrollbar.

Each online node has its own **Ping** action. Ping uses the embedded Tailscale device's
discovery ping to measure round-trip latency; it does not measure download speed
or change the selected exit. Connected desktop sessions reuse their running engine.
When disconnected, the saved device performs a short listener-free check without
creating a TUN or changing system DNS. Pings run one at a time with a 15-second
overall deadline; sign-in and other device transitions must finish first. Failures
can be retried, and results clear when the network or device login changes. The
desktop helper and network service must support this operation; update an older
network service if Ping is unavailable.

Node rows show a plain green check only when selected, the node name,
right-aligned latency and a **Ping** text button. Unselected check slots stay
empty; rows have no selected or hover fill. Mouse selection leaves no blue
focus box, while keyboard focus remains visible. Click **Ping** to test or retry independently; click the
name or checkmark to select. Latency is green through 500 ms, amber above 500 ms
through 1000 ms, and red above 1000 ms. Hover for direct/relay path or error
details. Tailscale IPs and radio controls are not shown in the lists.

Settings groups general preferences, network options, DNS, routing rules, advanced
parameters and app updates. Logs has its own page with bounded, redacted output
and a log-level control. Logs use a terminal-style dark gray background and light
text in both system appearances. Terminal foreground colors render with a readable
bright palette; other terminal commands are discarded, and log content remains
plain text. The log viewport fills the remaining window height and scrolls internally.
While visible, Logs reads the latest file tail every four seconds and follows new
entries at the bottom. Scrolling up retains the displayed snapshot while newer
entries are fetched in the background, so trimming the bounded tail cannot move
the entry being read. Returning to the bottom or refreshing displays the latest
entries and resumes following. Reopening Logs also follows the latest entry.
Read errors appear separately and preserve the last successful output. Unchanged
output retains its existing text nodes and selection.

Desktop logs are saved to `~/Library/Application Support/TailSplit/logs/tailsplit.log`
on macOS and `%APPDATA%\TailSplit\logs\tailsplit.log` on Windows unless overridden
with `--log`. The file rotates at about 5 MiB to `tailsplit.log.1`; the panel reads
at most the latest 128 KiB, with device authorization links and keys redacted.

Moving between these pages preserves unsaved form and YAML drafts, including
incomplete source edits. The panel and native window follow
the system's light or dark appearance and update immediately when it changes,
preserving unsaved edits. The local page also follows the browser's color
preference when opened outside the app. The compact desktop interface uses
platform system fonts, with monospace reserved for YAML, diagnostic logs and
code-like fields. Shared custom dropdowns use the app's surfaces and selected
checks; routing and other ordinary settings rows have consistent heights, and
MTU units sit outside the numeric field so native steppers cannot overlap them.
Wide windows add a live YAML preview, and a full source editor is always
available. Validate or save/apply changes from either view. Saving reconnects an
active proxy; an idle app stays idle. Existing comments and unrelated settings are preserved. External edits
cause a revision conflict, and failed edits retain the unsaved draft. App service
checks run before saving: the application proxy port must be at least 1024, the
system chooses the TUN name, and the app uses interactive or saved login rather
than environment authentication keys.

The **Language** control under **Settings → General** offers **Follow system**,
**English** and **简体中文**. The panel and application-owned native menus and
dialogs share this choice; unsupported system languages use English. Changes take
effect without reconnecting or replacing unsaved form/YAML drafts. The preference
is saved beside the selected configuration as `config.yaml.ui.json`, independently
of network settings, so it survives app restarts and the panel's changing port.
YAML keys, node names, diagnostic logs and upstream release notes remain unchanged.

While the window is open, TailSplit also appears in the Dock and Command-Tab,
and the menu bar shows its TailSplit, Edit and Window menus, so the standard
copy, cut, paste, select-all, undo and redo shortcuts work in the form and the
YAML editor. Closing the window hides it and returns TailSplit to the menu bar;
the menu item shows the same window again, with any unsaved draft. The window
remembers its size and position. Command-Q quits like **Quit TailSplit**
(`退出 TailSplit`) and stops a proxy the app started.

The app owns the page on a random `127.0.0.1` port and closes it on exit. Its
per-run capability travels in the URL fragment and then an Authorization header.
Host/Origin checks and embedded assets keep the panel local; it does not expose
Tailnet login links or device credential state. The window keeps the capability
and other web data in memory only. It loads only that page's own
`http://127.0.0.1:<port>` origin and has no new windows, downloads, context menu
or web inspector. If the window cannot be created, the menu opens the page in the
default browser instead. Reopen the panel from the menu after restarting the app.

**Detailed Logging (Debug)** (`详细日志（Debug）`) under **Settings and Tools**
(`设置与工具`) saves `log_level: debug` and reconnects an active proxy. Disable it
to restore `log_level: info`. Use **View Logs…** (`查看日志…`) to inspect DNS, routing and
connection diagnostics.

## CLI configuration

Use [config.example.yaml](../config.example.yaml) as a starting point:

```sh
tailsplit init -c config.yaml
tailsplit check -c config.yaml
tailsplit render -c config.yaml
tailsplit version
```

`init` creates without overwriting an existing file. `check` validates without
starting the proxy; `render` shows the embedded-core configuration. When a remote
rule-list cache is missing, both commands download and cache it first. With a cache
or local rule file, validation and rendering work offline. Connecting requires
appropriate network privileges and a configured proxy or authorized Tailnet destination:

```sh
sudo tailsplit run -c config.yaml
```

New templates use Split/builtin routing, direct unmatched traffic, system DNS
for direct traffic, `8.8.8.8` for proxy DNS, FakeIP, system TUN and MTU 1400.
`dns.direct_ipv6: false` disables direct Internet IPv6 handling; it does not
disable proxy/Tailnet IPv6 or change the operating system's IPv6 setting.
Desktop auto-connect is enabled once a device and gateway are configured.
Existing configuration files and legacy omitted-field semantics are preserved.

DNS is always served inside the TUN at `172.31.255.2:53` and
`[fd74:6169:6c73::2]:53`, including when `tun.enabled: false`. The switch only
controls whole-system traffic capture and system DNS publishing. DNS listener
addresses and ports are fixed. `dns.listen` is unsupported and fails validation;
remove that field from older YAML files.

### Rule sources

Edit routing and DNS rules in the YAML. Choose **Settings → Routing rules →
Rule source** to use **Builtin rules**, a **Custom URL or file**, or **No rule
list**. The equivalent YAML setting is:

```yaml
rules:
  source: builtin
```

`builtin` selects the maintained default subscription. To use your own rules,
set `rules.source` to an HTTPS URL such as `https://example.com/rules.txt`, or a
local file path such as `./rules.txt`. Relative paths resolve beside the YAML
file. An empty string disables the list while keeping your explicit domain and
CIDR rules. The legacy `rules.gfwlist` key remains compatible; use only one source
key in a configuration.

TailSplit automatically detects Base64-encoded and plain-text domain lists.
Plain text supports one domain per line and AutoProxy-style entries such as
`||example.com` and direct exceptions such as `@@||safe.example.com`; the
AutoProxy header is optional.

Remote lists, including builtin rules, are downloaded and validated on first use,
then atomically cached under `<state_dir>`. Each subscription has a separate
cache, so checking an unsaved source cannot replace another source's rules.
Later checks and connections reuse the matching cache, including offline.
Compatibility rules supplement application domains missing from the selected
list, without requiring a refresh.

To update the cache explicitly:

```sh
tailsplit rules refresh -c config.yaml
```

Failed downloads or invalid rules leave the existing cache intact. An initial
download failure without a cache stops split-mode startup with an error. Downloads
have a 30-second timeout and a 2 MiB limit. Global mode and device login, logout
and exit discovery do not require a rule list. The desktop app downloads as the
ordinary user and transfers the cached contents to its network service.
[Supplemental application rules](../internal/domainrules/supplemental.txt) are added
when loading the list; explicit direct exceptions take precedence.

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

Go 1.27.3 or later and Python 3.11 or later are required. macOS app builds need
Xcode Command Line Tools, Swift and the standard macOS packaging tools. The
production desktop uses MyGo v0.8.3 in the root Go module: `cmd/tailsplit-tray` is
the entry point, `internal/desktopwindow` hosts the local configuration panel,
and `internal/desktoptray` adapts its tray menus. Build commands run at this
repository root; no sibling monorepo is needed:

```sh
make test check
python3 -B -m unittest discover -s scripts -p 'test_*.py'
python3 -B -m unittest discover -s tests/installer
python3 -B -m unittest discover -s tests/packaging
make app
```

To build and open the production desktop from source on macOS:

```sh
make build build-tray
./bin/tailsplit-tray --template "$(pwd)/config.default.yaml" --helper "$(pwd)/bin/tailsplit" --open --no-connect
```

`make run-tray` runs those build and launch steps. It uses your normal TailSplit
configuration and log paths; `--template` initializes only a missing configuration.
`--open` shows the window and `--no-connect` disables automatic connection for that
launch. Pass `--config` and `--log` to the binary to use separate files. The
compatibility targets `make build-mygo` and `make run-mygo` call the production
build and launch targets; desktop tests run with the root `make test` target.
Windows uses different desktop flags and helper names; use the
[Windows release build](#windows-desktop) to assemble its installer and prerequisites.

To verify the tray popup over another app's full-screen Space on a logged-in
macOS desktop, use a fresh output directory after building both binaries:

```sh
swift scripts/verify-tray-fullscreen.swift bin/tailsplit-tray bin/tailsplit bin/fullscreen-proof
```

The fixture temporarily opens its own full-screen window, launches an isolated,
disconnected TailSplit, and checks native visibility, keyboard focus, foreground-app
preservation, reopening and dismissal. It saves a popup screenshot and JSON report,
then closes both test processes without touching the installed app or its configuration.
Physical menu-bar hit testing and network actions are not part of this proof.

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
go test -mod=readonly -tags with_gvisor . ./cmd/tailsplit ./internal/privatefs ./internal/domainrules
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
network service is started. The page works offline with embedded assets and needs
no npm. The preview also exercises Peetty's independent CDN updater; its private
cache is stored beside the preview configuration, separate from installed profiles.

## CI releases

A `vX.Y.Z` tag matching root `VERSION` runs
[the multi-platform release workflow](../.github/workflows/release.yml). Linux,
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
credential payload is committed here. See [signing setup](../credentials/README.md).
Without configured signing, macOS builds are explicitly recorded as ad-hoc.
The public site is deployed independently from `site/` by its Pages workflow;
the root installer is published as `/install.sh` alongside it.

## Website preview

The static project page lives in `site/`. Preview it with
`python3 -m http.server 8080 --directory site`; no build or package installation
is required. The Pages workflow also publishes the root installer as `/install.sh`.

The page follows the browser's English/Chinese language preference and the
system light/dark theme. Header controls override either choice and remember it
for this site's origin. `site/shared/` contains the common idy.dev shell and
preference and release helpers. Its self-hosted fonts use Montserrat for headings,
Monda for text and controls, Lato for notes and status, and Phosphor for icons;
the font licenses are included.

Downloads use GitHub's latest stable release, falling back to the newest published
prerelease only when no stable release exists. Buttons use the actual release
asset URLs; missing assets or API failures retain useful release-page links.
The page remains readable in English without JavaScript. Run the dependency-free
website checks with `node --test tests/website.test.cjs`.

## Pull request review

The protected `main` branch requires an up-to-date pull request, resolved review
conversations, all four platform CI checks (Linux, macOS and Windows amd64/arm64),
and all three OpenAI review checks. These rules also apply to administrators.
Website-only pull requests run CI too, so required checks cannot remain pending
because of path filters. The repository supports squash auto-merge: enable it on
a pull request to merge once all required checks and conversations are satisfied.

[The OpenAI PR review workflow](../.github/workflows/codex-review.yml) calls the
shared [GizClaw reviewer](https://github.com/GizClaw/github-workflows) at its
`latest` tag. Default-target automatic reviews run directly on
`pull_request_target` from the protected default-branch workflow, including every
synchronization event. They do not depend on a caller in the proposed branch.
Non-default targets use a metadata-only dispatcher, with no provider secret, to
request actual review on the trusted default workflow; source-run fencing rejects
stale requests. The stable `codex-review.yml` path preserves older target-branch
callers. New dispatches capture the original base ref and SHA, which are rechecked
after the shared whole-workflow PR queue admits the worker. Legacy dispatches
without a base capture are accepted only while the PR remains non-default, so
they cannot overwrite eligible default-target results after retargeting. These fallback reports are subject to the dispatch-origin gate
restriction described below. Secret-bearing review jobs reject unprotected or
non-default workflow refs. The shared reviewer checks out
only the trusted base and reads the PR diff as data; it never runs proposed
project code. Install this trusted caller on the protected default branch before
requiring its three merge checks; dispatch-origin reports cannot bootstrap an
eligible default-branch caller.
Each review publishes `OpenAI PR Review`, `OpenAI Issue Review` and
`OpenAI Code Review` on the reviewed head with one combined report.

GitHub does not count checks from `workflow_dispatch` or `issue_comment` workflow
jobs as required merge checks. Those entry points remain available for review
reports: post `@codex review` on its own line in a PR comment, optionally followed
by a focus, or manually run `codex-review.yml` with the PR number. To refresh
merge-gated results without a code update, edit the PR metadata to start an
eligible `pull_request_target` run. Actions reruns are rejected by the shared
reviewer; create a fresh request instead. See [GitHub's required-check event
restrictions](https://docs.github.com/en/pull-requests/how-tos/merge-and-close-pull-requests/troubleshooting-required-status-checks#checks-from-some-workflow-jobs-are-not-evaluated).

Fork review uses the default-branch caller and needs the `OPENAI_API_KEY`
Actions secret. Because this repository is public, pull requests from forks are
reviewed automatically with that key, so use a dedicated key with a spending
limit. Diffs over 5,000,000 bytes wait until an admin comments
`@codex review approve <full-head-sha>`.

## License and source provenance

TailSplit is GPL-3.0-or-later; see [LICENSE](../LICENSE) and
[third-party notices](../THIRD_PARTY_NOTICES.md). Downloaded rule lists retain their
respective upstream licenses.

The initial public application tree was imported from `letsolo/tailsplit`
without its private signing files or private credential history. The shared
parser was copied into this project, while its original remains available to
other monorepo components. Runtime bundle IDs, service labels and user state
paths retain their existing values for upgrades.
