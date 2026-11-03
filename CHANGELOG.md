# Changelog

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
