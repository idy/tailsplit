# Changelog

## 0.5.4

- Add Windows amd64 and arm64 CLI ZIP releases, with native Windows CI, archive
  execution checks and an elevated DNS-only Wintun lifecycle fixture.
- Create private Windows state/configuration with protected, inheritable ACLs and
  reject public or reparse-point state. Add nonblocking Windows process locks.
  Existing Linux/macOS filesystem checks and service identities are preserved.
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
