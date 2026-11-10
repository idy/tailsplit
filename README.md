# TailSplit

Choose which traffic goes through your Tailscale exit node and which stays direct.
TailSplit uses sing-box routing rules, can use exits in several Tailscale networks
at once, and lets you configure routing by domain and IP.

[Website](https://tailsplit.idy.dev/) · [Download](https://github.com/idy/tailsplit/releases/latest) · [Guide](docs/guide.md) · [Discord](https://discord.gg/RY7ktfh8Pr)

## Install

| Platform | Available packages |
| --- | --- |
| macOS 14+, Apple Silicon or Intel | Desktop app (`.dmg` / `.zip`) and CLI |
| Windows 10+ x64 or Windows 11 ARM64 | Desktop Setup EXE and CLI ZIP |
| Linux amd64 or arm64 | CLI archive and `.deb` |

Get desktop installers from [GitHub Releases](https://github.com/idy/tailsplit/releases/latest).
macOS and Linux also support the installer below, which selects your platform
and verifies the release's SHA-256 checksums:

```sh
curl -fsSL https://tailsplit.idy.dev/install.sh | sh
```

macOS users can also use the [Homebrew tap](https://github.com/idy/homebrew#install).
For a user-local CLI, pass `sh -s -- --cli --no-sudo` after the pipe.
See [installer options](docs/guide.md#install) for other destinations and versions.
Windows executables are unsigned; verify the release checksums before installation.

## Get connected

1. Open TailSplit and use **Add exit** to authorize a device in your chosen Tailnet, or sign in to an existing saved exit. Then choose its exit node.
2. Review your routing rules in the configuration panel.
3. Choose a default gateway and connect when ready. Signing in alone does not connect. New configurations enable automatic connection on later launches once a gateway is configured.

The **Exit Nodes** page lists your saved exits; its top-right switch starts or
stops the connection. Above the list, **Add exit** sits on the left and the current
default exit on the right; no saved-exit count is shown. Each added exit is a separate embedded device in the same or a
different Tailnet and needs its own authorization. Each device's login is saved
locally and reused after restarting the app. Authorizing another exit does not
sign out existing devices. Adding an exit disconnects an active session, opens
browser sign-in to choose the network, then opens the node picker, without asking
for a name first. In active Global mode, a confirmation explains that this stops
Kill-Switch protection and restores system networking. Cancel or Escape changes
nothing. After confirmation the app stays disconnected, including after a save
or login failure, until you explicitly reconnect. Add requires the desktop app;
the standalone configuration page cannot authorize or add devices. Confirming the first node binds
the exit's Tailnet guard and names it `<exit-device-name>@<network name>`.
Automatically named profiles keep `auto_name: true`, so later node choices update
the label without changing the profile ID or device identity. Existing custom
names are retained. Open any exit to choose its node and test nodes with
Tailscale Ping individually. Node
changes require confirmation; saving reconnects an active session or leaves an
idle app stopped.

On **Exit Nodes**, the current Split default proxy exit or Global exit is shown
in a compact text dropdown at the top right of the card list, or **Not set** if
none is configured. Choose a gateway directly in that dropdown to save it, without
an edit dialog. A disconnected app stays disconnected; an active Split session
reconnects. Switching devices during active Global mode first asks for consent
to stop protection, then disconnects and saves the selection while staying stopped
until you reconnect manually. Signing in to another saved device follows the
same consent rule; active Global settings, node and DNS-metadata applies also
require that separate disconnect. Exact no-op saves keep the current session.
Native actions without confirmation require disconnecting first. Recovery of the
current device's pending authorization keeps its helper. Turning on without a default
still opens a selection dialog with no preselected option. Choose an
exit and confirm to save it and connect; Cancel or Escape changes nothing. A
failed or stale save never starts traffic. Settings drafts must be resolved
first. An existing offline default is retained rather than silently replaced.

The titlebar routing dropdown chooses **Rule-based** or **Global (Kill-Switch)**.
It saves immediately and reconnects active traffic. Global/TUN mode blocks
traffic if its exit goes offline; Rule-based restores Split without deleting
saved rules. Manual disconnect or quitting restores system networking.
During connection preflight, incomplete peer discovery is shown as checking the
exit rather than asking for a replacement; the same connection continues once
its selected node becomes ready. Refresh sits
to the right of both switches. With an active session, first configure the target
mode's gateway before switching; unsaved settings drafts block the shortcut.

In **Settings › Routing rules**, choose Split or Global and save to apply it.
Split's default proxy exit (`rules.default_exit`) is independent of the
unmatched-traffic setting (`rules.final`). It carries proxy matches from the
builtin or custom rule source, `proxy_domains`, `proxy_ips`, `final: proxy`, and
bare Tailnet addresses without a more specific route. Rule groups can send
domains and CIDRs through other exits; unmatched traffic can go direct, use the
default proxy exit, or use any configured exit, including `main`. Global runs only the device
selected by `rules.global_exit` for all captured traffic. Split rules remain
saved while hidden in Global. Connection preflight checks node availability for
the default gateway, explicit final exit and nonempty rule groups. Other retained
endpoints with an identity guard must authenticate to their configured Tailnet,
but their nodes need not be online. Signed-out optional devices with known DNS
suffixes are omitted from the running core, and their private names are refused
without changing saved profiles or blocking the selected gateway. If an optional
device has neither a saved private suffix nor authenticated live DNS metadata,
connection is refused: refresh or sign in to that device before retrying. Removing
an endpoint alone cannot protect an unknown namespace. Reconnecting reevaluates
them. The selected traffic device keeps its interactive login session alive until sign-in
completes. If another required final or rule-group exit needs login, preflight
stops with a recoverable sign-in error: open that saved exit, sign in, then retry
connecting. Initial device states, including Starting, Stopped and missing status,
have a 45-second readiness budget. Optional devices without an identity guard
also get that opportunity to report their login state and private DNS suffix;
after the budget, unavailable optional endpoints can be isolated only when their
private namespaces are known, and required gateways must still be ready.
Only the selected device's interactive authentication pauses the
availability timeout; other backend transitions preserve its deadline.

An extra's `tailnet` is its login identity guard; `magic_dns_suffix` stores the
actual DNS suffix independently of its display network name. Traffic preflight
checks both the network guard and saved suffix metadata. After a legitimate DNS
suffix change, refresh the saved device and explicitly apply its new DNS metadata
in the picker; the node and account guard stay unchanged. DNS and traffic
use the same longest-suffix ownership order, with the primary winning exact
duplicates. Exit IDs `main`, `direct`, `proxy` and Windows device basenames such
as `con`, `nul`, `com1` and `lpt1` are reserved. Pending or missing owners of live
private-name policy must be configured before connecting; their rules never
silently fall through to another resolver or gateway.

The legacy `tailscale` profile has logical ID `main`; it is optional and
removable. Additional exits keep their IDs and device state under
`state_dir/exits/<id>/tailscale`, even when `main` is removed. **Remove exit** at
the bottom of every picker also supports the last exit. After confirmation it
disconnects, signs out only that device, and revision-checks removal of the local
profile. Selectors that reference it become explicitly unset (`"-"`), so the next
connection asks for a new choice. Explicit `rules.final` and route-group references
remain as dormant policy: choose replacements or explicitly edit those rules
before another Split connection can start, even when the default is still ready.
Adding a different device remains possible without deleting the retained policy.
The app stays stopped. The
remote device record remains in the Tailnet admin console; TailSplit has no
management authority to delete it. There are no separate switch-network or
sign-out controls; use Add exit to choose another network. Removing the last
exit retains the remaining routing policy as a draft. Adding again creates a
new additional profile and leaves both traffic exit selections unset until confirmed.

**Application proxy** lets individual applications use the local HTTP/SOCKS
listeners. **System routing** captures system traffic when `tun.enabled` is set.
Both modes create a DNS TUN and require the privileged network service:
Windows Setup installs it; macOS requests administrator approval on first connection.

Configuration and device state are retained across normal updates. The network
service checks import eligibility per device, reads only still-needed CLI or
fallback copies, and holds their ordinary-user state lease through cleanup. An
unused primary remains eligible after another device initializes the new service
cache. Existing service-owned or logged-out devices retain authority, and legacy
initialization markers are interpreted conservatively. If a service channel
fails, the helper reconnects to confirm that its registered session has finished
before releasing the lease. A slow disconnect reports cleanup in progress and
keeps the helper alive; the desktop does not force-kill it after its stop timeout.
Older services without session recovery must be updated before connecting.

New desktop profiles use [config.default.yaml](config.default.yaml): direct Internet
IPv6 off, FakeIP and system TUN on, MTU 1400, Split/builtin rules, unmatched traffic
direct, system DNS for direct traffic and `8.8.8.8` for proxy DNS. Auto-connect is
on once a gateway is configured. The template has no saved devices or account
identity. Existing files, including their omitted-field behavior, are preserved.
The CLI example uses the same portable preferences with an illustrative exit.

The local configuration page includes a [Peetty](https://github.com/idy/peetty)
Miru companion that walks and jumps between safe page edges. Drag it to move it,
or hover for actions. **Settings › General › Page companion** hides or restores
it immediately; the preference is saved with the app's UI preferences and survives
restarts and updates. The companion runs locally with bundled assets, respects reduced motion, and
requires no AI connection or API key.

## CLI

Start from [config.example.yaml](config.example.yaml). Replace its placeholder
exit with an approved gateway from your Tailnet before running:

```sh
tailsplit init -c config.yaml
tailsplit check -c config.yaml
sudo tailsplit run -c config.yaml
```

Run the Windows CLI from an Administrator terminal when connecting.
See [CLI configuration](docs/guide.md#cli-configuration) for DNS, rule lists,
network privileges, and configuration migration.

## Rule sources

Choose **Builtin rules**, a **Custom URL or file**, or **No rule list** under
**Settings → Routing rules**. The equivalent YAML setting is:

```yaml
rules:
  source: builtin
```

Custom sources support Base64 and plain-text domain lists, including lists
without an AutoProxy header. Builtin rules download on first use and are cached
for offline reuse. See [rule sources](docs/guide.md#rule-sources) for URL and file
examples.

## Community

Join the [TailSplit channel in IDY Community](https://discord.gg/RY7ktfh8Pr) for
questions, routing examples, and feature ideas. Use the **ideas** and
**bug-reports** forums with the **TailSplit** tag to track feedback.
For reproducible bugs, [open a GitHub issue](https://github.com/idy/tailsplit/issues)
with your version, platform, and reproduction steps.

## Development

Requires Go 1.27.3+ and Python 3.11+. From the repository root:

```sh
make test check
python3 -B -m unittest discover -s tests/installer
python3 -B -m unittest discover -s tests/packaging
```

macOS app builds also need Xcode Command Line Tools and Swift.
See the [build guide](docs/guide.md#build-and-test) for platform-specific commands.

The production macOS and Windows desktop uses MyGo v0.8.3 for its window and
tray. Its entry point is `cmd/tailsplit-tray`, with `internal/desktopwindow` and
`internal/desktoptray` hosting the existing configuration UI and controls.
On macOS, `make run-tray` builds the CLI helper and `bin/tailsplit-tray`, then
opens the configuration window with automatic connection disabled for that launch.
`make test` checks the desktop packages in the root module. See the
[build guide](docs/guide.md#build-and-test) for launch options and Windows packaging.

| Path | Contents |
| --- | --- |
| `cmd/`, `internal/` | CLI, desktop controls, network service, and configuration UI |
| `config.default.yaml` | Shared new-desktop defaults, without saved devices |
| `config.example.yaml` | CLI starter with the same preferences and a placeholder exit |
| `packaging/`, `scripts/` | Installers, release tools, and development helpers |
| `site/` | Public website |
| `tests/` | Installer, packaging, and website checks |

## Documentation

- [Desktop setup and controls](docs/guide.md#macos-app)
- [Windows desktop and CLI](docs/guide.md#windows-desktop)
- [Updates and older-version migration](docs/guide.md#desktop-updates)
- [CLI configuration and DNS](docs/guide.md#cli-configuration)
- [Releases and signing](docs/guide.md#ci-releases)
- [Website preview](docs/guide.md#website-preview)

## License

[GPL-3.0-or-later](LICENSE). See [third-party notices](THIRD_PARTY_NOTICES.md).
