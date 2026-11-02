# Changelog

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
