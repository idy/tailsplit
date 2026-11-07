# Changelog

## 0.7.12

- Download the official GFWList subscription when its disk cache is missing,
  validate it and atomically save it under the state directory. Reuse the cache
  for later checks and connections; failed refreshes retain the last good copy.
- Remove the upstream snapshot from the repository and application packages.
  Keep legacy `gfwlist: builtin` configurations compatible with the official
  download source. Login, logout, exit discovery and global mode do not require
  a GFWList download.
- Rename the parser package to `domainrules`, keep supplemental application
  rules in one flat file and remove eight entries already covered by upstream.
  Preserve direct exceptions and keep supplemental rules out of the disk cache.
- Add compatibility coverage only when the selected list lacks previously
  supported application domains, preserving older local and custom sources
  offline without duplicating current upstream coverage.
- Keep service-side rule loading restricted to transferred contents, and retain
  offline Debian and Windows smoke tests.
- Isolate all caches by subscription URL and migrate legacy shared caches only
  for their saved configuration, retaining offline upgrades. Unsaved drafts
  cannot adopt another source's cache or replace its rules.

## 0.7.11

- Put Connection and Logs at the top of the desktop sidebar and pin Settings at
  the bottom. Keep Tailscale sign-in, connect/disconnect, split/global routing
  mode and exit selection together on one connection page.
- Add searchable exit lists with online, unavailable and saved-but-missing
  states. Stage exit and mode choices until explicitly confirmed; reconnect an
  active session while keeping a disconnected app disconnected.
- Preserve unsaved form and YAML drafts across pages, and require settings drafts
  to be saved or discarded before applying connection changes. Keep unavailable
  selected exits visible instead of silently choosing another node.
- Add a dedicated log page, grouped settings and complete in-window connection
  controls on macOS. Share bounded, redacted diagnostic logs across macOS and
  Windows while keeping device authorization in the default browser.
- Align the sidebar Settings divider with the settings actionbar, including
  window resizing and wrapped English actions. Retain system appearance and
  English/Simplified Chinese support.

## 0.7.10

- Add explicit Tailnet switching from the desktop window and tray, with a
  confirmation before disconnecting and signing the device out.
- Clear the previous Tailnet restriction, exit selection and automatic
  authorization setting while preserving unrelated configuration. Request fresh
  browser authorization and leave exit selection and connection explicit.
- Preserve network-switch errors across configuration reloads, bound native
  browser requests, and handle macOS login-item approval accurately.

## 0.7.9

- Follow the system's light or dark appearance in the desktop configuration panel
  and local browser page. Switch immediately without reloading or losing unsaved
  form edits, YAML drafts or open dialogs.
- Match macOS window and web-view backgrounds to the current appearance. Follow
  Windows app color mode for the native frame, loading canvas and WebView2 page,
  including live changes and custom app/system color combinations.
- Adapt controls, status messages, dialogs and YAML highlighting for both themes,
  and verify initial and live appearance in the native Windows desktop fixture.

## 0.7.8

- Add English and Simplified Chinese throughout the desktop configuration panel,
  application menus, dialogs, connection states and update controls. Follow the
  system language by default, with a persistent language selector in the panel.
- Apply language changes without reconnecting, saving network configuration or
  replacing unsaved form and YAML drafts. Keep the preference in a separate
  local file so it survives app restarts and changing panel ports.
- Adapt settings layouts for longer English labels and localize dates, line
  counts, placeholders and accessibility labels. Keep user names, YAML keys,
  diagnostic logs, release notes and detailed validation diagnostics intact.

## 0.7.7

- Improve desktop configuration readability with clearer section headings, larger
  labels and explanatory text, consistent control sizes and more space between
  fields. Keep the native title row and window controls at their existing sizes.
- Keep the live YAML preview clear of the title row and bottom actions when
  scrolling, and give the full editor and preview consistent code typography.
- Improve the website's heading wraps, body and supporting text sizes, and
  narrow-screen routing illustration. Keep route explanations stable when
  switching examples and bring the shell installer alongside desktop downloads.

## 0.7.6

- Bring the app identity, current configuration section, connection status and
  update action together in a single desktop window title row. Download progress
  appears as a percentage in the update button, without a separate progress row.
- Support dragging the title row and native window controls while preserving
  close-to-tray behavior, unsaved drafts and the existing local security boundary.
- Improve configuration-panel text contrast, keyboard navigation, touch targets
  and the visibility of connection and update errors.

## 0.7.5

- Ship the forked-tail snake icon and green connected state from the unreleased
  0.7.4 build.
- Put Open (`打开`) first in the macOS menu bar app's menu, opening the existing
  configuration panel directly instead of from Settings and Tools.
- Choose loopback integration-test ports through TCP before probing UDP, avoiding
  Windows TCP-excluded ranges that prevented the ARM64 release build.

## 0.7.4

- Replace the split-route mark with a monochrome snake: its body draws the S and
  its forked tail is the route splitting in two. The app, installer, tray, menu
  bar, website and configuration panel all use it, on one dark tile color.
- Show the connection in the menu bar and tray. The snake turns green while a
  tunnel or proxy session is running, matching the configuration panel's status,
  and returns to its normal color while connecting, blocked or disconnected.

## 0.7.3

- Open the macOS configuration panel in TailSplit's own dark, resizable window
  using the system WKWebView. Remember the window's size and position, show it
  in the Dock and Command-Tab while open, and support standard editing shortcuts.
  Closing hides the window and keeps unsaved edits for the next opening.
- Keep the panel's web data in memory and restrict navigation to its own local
  origin. Block new windows, downloads and external navigation; use the browser
  only if the native window cannot be created.
- Retry only the window's own failed page load. A later successful load clears
  the failure state so reopening the panel does not reload and discard a draft.

## 0.7.2

- Let the configuration panel use the full window width. Remove the 1240px
  content cap that left a large empty area on wide screens, and let the YAML
  preview grow with the form. Narrower windows keep the single-column layout.
- Document the one-time manual installation needed by macOS 0.7.0 users before
  they can use the fixed in-app updater included since 0.7.1.

## 0.7.1

- Install desktop updates on macOS. The app passed its code-signing requirement
  to `codesign -R` as a file path instead of as text, so every in-app update
  stopped at the signature check and kept the running app. The macOS 0.7.0 app
  cannot install updates itself: install 0.7.1 once from the release page or
  with the shell installer. Windows updates were not affected.
- Check the requirement with the real `codesign`, and run the replacement worker
  against a `codesign` stand-in that rejects requirement file paths.

## 0.7.0

- Check for stable desktop releases on GitHub ten seconds after the app starts
  and every six hours, offering only strictly newer stable versions. Downloads
  must match their release size and `SHA256SUMS`, and installation always asks
  first. Windows runs the verified Setup; macOS checks the bundle's version,
  signature and Gatekeeper acceptance, then replaces and relaunches the app,
  restoring the previous bundle on failure. `app.check_for_updates` defaults to
  on and changes without reconnecting.
- Let standard Windows accounts connect to the network service. Since 0.6.1 the
  desktop authenticated the service by opening its SYSTEM process, which only
  administrators may do; the service now grants its registered desktop user a
  limited process query.
- Start the Windows desktop again after WebView2's first run. Startup no longer
  rejects the sandbox permissions WebView2 sets on its own browser cache.
- Restyle the configuration panel and Windows window in a compact dark layout
  with a dark title bar, and give the app, installer, tray and menu bar one
  consistent TailSplit icon.
- Upgrade a running, connected Windows desktop to a newer Setup in CI, and run
  the updater and its installer handoff natively on Windows.

## 0.6.2

- Prevent the network service from panicking while logging IPv6-to-IPv4 direct
  destination recovery. Format resolved address slices before passing them to
  the core logger, including the app's platform-log channel at Info level.
- Cover recovery with the real production logger and app log filtering at both
  Info and Debug levels, preserving the original routing and DNS behavior.

## 0.6.1

- Authenticate Windows service endpoints through read-only SCM process and
  configuration queries instead of requiring ordinary clients to inspect a
  SYSTEM process token. Require the running LocalSystem service's exact PID,
  protected image and fixed arguments, and retain registered-client SID checks.
- Include the Windows desktop and fixed-TUN DNS changes prepared for 0.6.0. The
  earlier source tag is retained for an unpublished release attempt.

## 0.6.0

Unpublished initial tag, superseded after release verification found the Windows
standard-user service authentication failure.

- Remove the obsolete `dns.listen` option from YAML and desktop service requests.
  Both connection modes serve DNS exclusively through the fixed TUN peers at
  `172.31.255.2:53` and `[fd74:6169:6c73::2]:53`; `tun.enabled` only controls
  whole-system traffic capture and system DNS publishing. Remove `dns.listen`
  from older configuration files before upgrading.
- Show the fixed DNS TUN addresses in the configuration panel and clarify the
  whole-system traffic capture switch.
- Add a Windows desktop window and native tray with connect/disconnect, external
  browser device login, logout, exit selection, configuration and private logs.
- Keep the UI unelevated and broker bounded, validated sessions over authenticated
  local named pipes to an installed SYSTEM network service. Stop/EOF leases wait
  for engine cleanup rather than relying on Unix signals or parent-PID changes.
- Scope service state by the administrator-registered desktop SID, migrate saved
  identity once, and retain configuration, registration and runtime state through
  upgrades and default uninstall. Serialize shared configuration edits and hold
  the legacy CLI state lock during service sessions.
- Add architecture-matched Inno Setup EXE installers with verified WebView2
  prerequisites, protected Program Files payloads and explicit service/file
  recovery after failed upgrades. Preserve the standalone CLI ZIP downloads.
- Gate desktop publication on native Windows x64/ARM64 installation, actual
  medium-integrity GUI automation and screenshots, service cleanup, repair,
  failed-upgrade recovery and uninstall checks.

## 0.5.4

- Add Windows amd64 and arm64 CLI ZIP releases, with native Windows CI, archive
  execution checks and an elevated DNS-only Wintun lifecycle fixture.
- Create private Windows state/configuration with protected, inheritable ACLs and
  reject public or reparse-point state. Add nonblocking Windows process locks.
  Existing Linux/macOS filesystem checks and service identities are preserved.
- Use the gVisor TCP/UDP stack on Windows for synthetic DNS peers, preserving
  the existing mixed-stack selection on macOS/Linux.
- Use Windows write-through atomic rule-cache replacement while preserving the
  last good cache on failed updates and keeping Unix directory-sync behavior.
- Include the embedded Wintun driver's full redistribution notice and provenance.
- Make project-page downloads go directly to architecture-specific GitHub assets,
  updating from complete official release metadata with versioned HTML fallbacks.
- Keep release publication gated on complete Linux/macOS/Windows amd64/arm64
  coverage and byte verification before promoting Latest.

## 0.5.3

- Add a local configuration panel to the macOS menu with grouped forms, live YAML
  preview, a full source editor, validation and save/apply. Edits preserve comments
  and unrelated settings, and reject stale revisions rather than overwriting
  external changes. The app serves embedded assets on loopback with a per-run
  capability; the panel does not expose login links or device credentials.
- Add a persistent Debug logging switch under Settings and Tools. Changing it
  reconnects an active proxy and restores ordinary logging when disabled.
- Recover direct public IPv6 TLS/QUIC destinations through IPv4 when
  `dns.direct_ipv6: false` and the hostname is recoverable. Preserve routing-rule
  precedence, local IPv6 and global proxy behavior; failed recovery never falls
  back to a proxy exit.
- Preserve drafts across delayed or rejected previews and background reloads,
  validate the app service policy before applying edits, and show blocked or
  unavailable exits without a connected indicator. Malformed form values remain
  repairable in the source editor.
- Cover cached IPv6 recovery through HTTP CONNECT and SOCKS, including ordinary
  and connected UDP, in addition to system-routing traffic.
- Add an isolated configuration-page preview command for development and ensure
  tray builds include the embedded core's build tags.

## 0.5.2

First standalone public release from `github.com/idy/tailsplit`.

- Publish the complete application, embedded rule parser, configuration example,
  licensing and source provenance without private release credentials.
- Build Linux/macOS amd64 and arm64 CLI archives, Debian packages, and native
  macOS application ZIP/DMG distributions through GitHub Actions.
- Add a platform-aware installer with checksum verification and preservation of
  external configuration and device state.
- Support Developer ID signing and Apple notarization through a dedicated,
  tag-restricted release environment, with verified distribution receipts.
- Publish the project page and installer through this repository's GitHub Pages.
- Verify Debian install, remove and purge behavior against the pinned Tailscale
  fork's license notices.
- Keep macOS disk-image diagnostic warnings separate from machine-readable plist
  output during artifact verification.

Bundle identifiers, network-service labels and user-state locations retain
existing values for upgrades. The initial `v0.5.1` build attempt was superseded
before release publication after native packaging validation found the last two
verification issues above; its source tag is retained.
