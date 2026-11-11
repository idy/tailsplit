# TailSplit for iOS and Android

A Flutter app that brings TailSplit's routing to phones with **one** exit: one
embedded Tailscale device and one selected exit node. Rule-based (Split) and Global
routing follow the desktop's semantics. sing-box runs as the system VPN through
[`sing_box_flutter`](https://github.com/idy/sing-box-flutter); Tailscale sign-in and
exit-node discovery are implemented in Dart inside this app.

## How it works

1. **Add exit** generates device keys and registers with Tailscale's control plane
   over the `ts2021` Noise protocol (`lib/src/tailscale/`). The sign-in page opens in an
   in-app browser; the app continues automatically after authorization.
2. The app reads the netmap, lists peers that offer an exit route, and saves the
   device identity in tsnet's on-disk state format, so sing-box's `tailscale`
   endpoint starts already signed in as the same device. No TUN is created while
   signing in or listing nodes.
3. Choosing a node names the exit `<node>@<network>`. The switch builds a sing-box
   configuration (`lib/src/services/config_builder.dart`, mirroring the desktop's
   `compile.go`) and starts the VPN.

The node picker shows the reachability Tailscale reports (Online, Offline, last
seen). Tailscale Ping needs the VPN core and is not offered here.

## Design

Native controls on each platform, carrying the desktop's TailSplit tint, semantic
state colours and terminal log style:

- **iOS 26+**: Liquid Glass `UITabBar`, toolbar, `UISwitch`, `UISegmentedControl`
  and `UIMenu` pull-downs through
  [`adaptive_platform_ui`](https://pub.dev/packages/adaptive_platform_ui); Cupertino
  fallbacks on iOS 15–18. System materials only for translucency.
- **Android**: Material 3 navigation bar, large top app bar, switches, segmented
  buttons, menus and bottom sheets with a static TailSplit-blue scheme.
- System fonts with Dynamic Type / font scale for all UI text; Montserrat only for
  top-level titles. English and Simplified Chinese.
- Card lists use 24-unit continuous corners and a leading platform icon on every
  single-line item. Long values truncate; explanations sit below the group.
  The rule-list download time appears in its own row beside Update, independently
  of the extra domain/IP entries.

See [the native mobile design system](DESIGN.md).

## Build

Requires Flutter 3.47.2+, CocoaPods and Xcode for iOS, and JDK 17 with
Android SDK 36. Android needs NDK `28.0.13004108` for the selected core and
`28.2.13676358` for Flutter packaging. The native sing-box core is a
source build selected by the `sing_box_flutter.core` block in `pubspec.yaml`
(TUN, direct, Tailscale endpoint, local/UDP/TCP/FakeIP/Tailscale DNS):

```bash
flutter pub get
```

```bash
dart run sing_box_flutter build --target apple
```

```bash
dart run sing_box_flutter build --target android
```

Rebuild the core after any `pubspec.yaml` edit; CocoaPods and Gradle refuse a
stale receipt. On iOS the Runner app and the `PacketTunnel` extension share the
App Group `group.dev.idy.tailsplit`. `ios/configure_project.rb` recreates the
extension target and entitlements if the Xcode project is regenerated. A real
VPN needs a physical device and provisioning profiles with the App Group and
Network Extension capabilities; the simulator runs the UI and Tailscale sign-in
only.

```bash
flutter run
```

### CI and organization publication

[Mobile CI and iOS publication](docs/release.md) describes the Android/iOS build
checks and manual iOS IPA/TestFlight workflow. Mobile distribution uses
**TERMITOC PTE. LTD. / BGL4WMWD3W**, the LetSolo organization account, for both
the app and Packet Tunnel extension. The required `BGL4WMWD3W_APPLE_CREDENTIALS`
secret must be configured in this repository's `mobile-release` environment;
LetSolo's repository secret is not automatically shared.

### Design preview

A preview build uses simulated exits and a simulated VPN and never contacts
Tailscale. It uses the same interface without an on-screen preview banner;
review messages and screenshot captions disclose when data is simulated:

```bash
flutter run --dart-define=TAILSPLIT_PREVIEW=true
```

Use `TAILSPLIT_PREVIEW_SCREEN` to open a specific state and
`TAILSPLIT_PREVIEW_LANG=en|zh` to set the preview language. Screen values include
`picker`, `settings`, `logs`, `empty`, `signin`, `connected`, `connecting`, `global`,
`reconnect`, `error`, `offline` (selected node offline) and `rules` (first rule-list
download failed). These options only take effect in preview builds.

## Failure handling

- **Connecting** names its step and can be cancelled by turning the switch off.
  Before starting, it refreshes a netmap older than a minute and refuses an exit
  node that Tailscale reports offline or no longer offers, or a device still
  waiting for admin approval. It never picks another node.
- **Rule lists** download on first use and are then reused from a per-source
  saved copy, so a blocked or slow GitHub does not delay every connection.
  Settings › Routing › Rule list updates it explicitly; a failed or invalid
  download keeps the saved copy.
- **Errors** are explained by cause (offline, timeout, TLS, rate limit, server
  response, refusal, invalid list) in English or Chinese, with the raw detail in
  Logs. A sign-in whose result cannot be saved ends with a retryable error rather
  than waiting forever; storage failures keep changes in memory with Retry.
- **Logs** are throttled and drop terminal escapes. A bounded queue retains the
  newest 2,000 lines; the hidden Logs tab keeps collecting without rebuilding.
  Opening the tab shows the latest lines and resumes following if it was at the end.

```bash
flutter run --dart-define=TAILSPLIT_PREVIEW=true --dart-define=TAILSPLIT_PREVIEW_SCREEN=picker --dart-define=TAILSPLIT_PREVIEW_LANG=zh
```

### Verification status

The app builds for the iOS simulator. The Exit Node, node picker, Settings and
Logs screens were inspected on an iPhone 18 Pro simulator running iOS 27, in
English/Chinese and light/dark appearance. Selecting another node and confirming
it updated the exit label while leaving the preview disconnected. These captures
use explicitly labelled simulated data.

The failure states (offline node, first rule-list download failure, connecting
step) and the Exit Node, picker and Settings screens at the accessibility-extra-large
text size were also captured on that simulator.

Static analysis is clean. The default test run passes 69 tests and skips six
checks that require external Go helper services; `test/hardening_test.dart` covers
cancelled connects, offline/missing nodes, failed sign-in saves, storage errors, the
rule-list cache, resolver and CIDR parsing, and damaged settings.
`test/cleanup_test.dart` checks bounded log ordering and notifications, inactive-tab
updates, theme reuse and control-error categories independent of diagnostic wording. Generated Split and
Global configurations were also instantiated with the desktop's pinned sing-box
1.14.4 engine. The Tailscale client received an authorization URL from the real
control server; authorization with a real account and device VPN traffic remain
unverified. Android and tablet layouts have not been verified on devices or
emulators.

## Tests

```bash
flutter test
```

## Key files

| Path | Purpose |
| --- | --- |
| `lib/src/tailscale/` | Tailscale control client (Noise IK, register, map) and tsnet state handoff |
| `lib/src/services/config_builder.dart` | sing-box JSON for Split and Global |
| `lib/src/services/rules.dart` | AutoProxy/domain list projection (port of `internal/domainrules`) and per-source cache |
| `lib/src/services/errors.dart` | Failure causes shown to the user; raw detail for Logs |
| `lib/src/state/app_controller.dart` | Exit, settings and VPN lifecycle |
| `lib/src/ui/` | Native pages: Exit Node, node picker, Settings, Logs |
| `ios/PacketTunnel/` | Network Extension entry point |
| `assets/` | Montserrat (OFL), rule supplement, app icon source |

## Data and privacy

Device keys and the saved exit stay in the app's private storage; tsnet state and
the sing-box cache live in the App Group container (iOS) or app files (Android),
excluded from backups. Authorization URLs and keys are never logged.
