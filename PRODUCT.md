# Product

<!-- impeccable:product-schema 1 -->

## Platform

web

The design surfaces are the public website in `site/` and the local configuration panel in `internal/configweb/assets/`. The panel uses HTML, CSS and JavaScript inside macOS WKWebView or Windows WebView2, alongside native menu-bar, tray and window controls.

TailSplit ships desktop apps for macOS and Windows and a CLI for macOS, Windows and Linux. `README.md` records current OS requirements, architectures and installation details.

## Users

The primary users are technically comfortable individuals familiar with Tailnet and proxy concepts who manage routing on their own computers. They need to keep selected traffic direct, route other connections through their chosen exit, and reach authorized Tailnet destinations.

Familiarity with networking does not imply a desire to edit YAML before making a first connection. Common tasks should be understandable from the desktop controls and configuration form; full configuration remains available for advanced use.

## Product Purpose

Help users make a first working connection quickly, then control how domain and IP rules route their traffic. Fast first connection is the confirmed priority for future product decisions: reduce setup friction and explain useful defaults while making required authorization, exit selection and connection actions clear.

Success means a user can install TailSplit, authorize their device, choose an available exit, review the relevant settings and connect, with a clear indication of the resulting state. No numerical onboarding or performance target has been established.

## Positioning

TailSplit is an open-source routing client based on sing-box rule lists. It supports switching between multiple Tailscale networks and accessing the internet through the selected network's exit node. DNS behavior follows the routing policy.

Public introductions focus on sing-box rule lists, multiple Tailscale networks and Tailscale exit nodes. Describe network switching without implying simultaneous connections to multiple networks.

The user chooses their exit and controls their configuration. TailRelay is the separately linked companion for providing a SOCKS5-backed Tailnet exit; keep the two products' roles distinct.

## Operating Context

- The website explains routing, installation and connection behavior, and links to actual GitHub release assets, checksums, source and documentation.
- A fresh desktop installation starts disconnected. Device authorization opens the default browser; signing in and starting a connection are separate actions.
- The initial connection workflow is to authorize the embedded Tailscale device, choose an available exit, review settings and explicitly connect. Login and exit discovery do not create a TUN.
- Desktop use continues through the macOS menu bar or Windows tray and the local configuration window. Its primary sidebar has Connection and Logs at the top, with Settings pinned at the bottom. The connection page combines Tailscale sign-in, connection controls, split/global mode and exit selection. Exit and mode changes require explicit confirmation; active sessions reconnect, while idle apps only save the selection. Settings drafts must be saved or discarded before applying these changes. Closing the configuration window keeps the app available in the menu bar or tray.
- Common settings use a form; the full YAML editor handles additional configuration. Users can validate drafts before saving. Saving and applying reconnects an active session while an idle app stays idle.
- The CLI supports offline initialization, validation and configuration rendering. Starting a connection requires the relevant network privileges.

## Capabilities and Constraints

- **Routing:** sing-box rule lists with domain and IP/CIDR matching, a configurable final route, and configured Tailnet destinations. Split mode respects rule priority; global mode sends captured traffic through the selected Tailscale exit node and retains the saved split rules.
- **Connection modes:** application proxy exposes local HTTP/SOCKS listeners for applications that opt in. System routing adds broader TUN traffic capture and system DNS changes. Both connected modes require a DNS TUN and the network service or equivalent CLI privileges; `tun.enabled: false` does not remove the DNS TUN.
- **Exit behavior:** unavailable selected exits block their traffic while the relevant TUN is active. TailSplit does not silently select a different exit. Disconnecting, quitting or a service crash removes the managed TUN and temporary DNS state; the global-mode protection is not a persistent system-wide firewall kill switch.
- **Tailnet switching:** an explicitly confirmed desktop action stops the current connection, signs the device out, clears the previous Tailnet restriction, exit selection and automatic authorization setting, and requests browser authorization for the new network. Other configuration remains intact. The user selects an exit and connects explicitly afterward.
- **Configuration integrity:** preserve unrelated YAML settings and comments, detect external revision conflicts, and retain unsaved drafts after failed edits. Upgrades retain user configuration and device identity.
- **Local administration:** the configuration panel is loopback-bound, capability-authenticated and served from embedded assets. Preserve its Host/Origin checks, restrictive Content Security Policy and protection of device credentials and authorization links.
- **Startup and updates:** opening at login and connecting automatically are separate preferences; automatic connection is off by default. Packaged desktop apps can check for updates, but downloading and installing require confirmation and release verification. Rule refresh is an explicit operation.
- **Distribution claims:** supported platforms, signing status and verification details must follow the current README and release evidence. Windows executables are currently unsigned; distinguish ad-hoc macOS development builds from verified release packages.
- **Existing interfaces:** preserve native menu, tray and window behavior, established configuration terminology and compatibility with existing user state. The website's shared idy.dev files must remain consistent with the other project websites as required by `README.md`.

## Brand Commitments

The product name is **TailSplit**, part of the idy.dev collection of independent open-source tools. The repository is published under GPL-3.0-or-later; preserve licensing and third-party notices.

Existing product copy explains routing and connection behavior in concrete terms. Keep claims tied to implemented behavior and release evidence, especially around permissions, exit failures, signing and updates.

The website supports English and Simplified Chinese, with browser-based defaults and persistent language/theme overrides. The configuration panel and application-owned desktop menus and dialogs also support English and Simplified Chinese, with a shared Follow system/English/简体中文 preference. The app saves this separately from network configuration and applies changes without reconnecting or replacing unsaved drafts. Unsupported system languages fall back to English. Diagnostic logs, YAML keys, user-provided names and upstream release notes retain their original content. Locally maintained project documentation is written in English.

## Evidence on Hand

- `README.md`: installation, platform support, desktop and CLI workflows, service boundaries, updates and preview commands.
- `config.example.yaml`: actual configuration vocabulary and documented defaults.
- `site/index.html`, `site/script.js` and `site/shared/`: public product copy, translations, release links and the interactive routing concept. The concept is explicitly an illustration; it makes no connection or network changes.
- `internal/configweb/assets/` and `internal/configweb/server.go`: the implemented configuration UI, editing states and local server boundaries.
- `LICENSE`, `THIRD_PARTY_NOTICES.md` and `LICENSES/`: licensing and source attribution.
- GitHub Releases: downloadable packages, checksums and release-specific signing receipts. Check the relevant release before making release-specific claims.

No verified testimonials, customer counts, adoption metrics or performance benchmarks were supplied for this product record. Do not invent them.

## Product Principles

1. **Shorten the path to a first connection.** Favor useful defaults, clear prerequisites and understandable setup over exposing every option up front.
2. **Reveal complexity when needed.** Make common operations accessible through desktop controls and forms while keeping full YAML configuration available.
3. **Make state and consequences clear.** Distinguish authorization, connection, traffic capture, startup and updates; explain when a save or installation interrupts an active connection.
4. **Respect the chosen route.** Explain DNS and exit-failure behavior accurately and preserve explicit user choices.
5. **Preserve the user's work.** Protect configuration, unsaved drafts and device identity through failures, edits and upgrades.

## Accessibility & Inclusion

Preserve the website's existing keyboard focus treatment, skip link, semantic controls, status announcements and reduced-motion support. Maintain the English/Chinese language choices and the local window's standard editing shortcuts.

A formal accessibility conformance target and additional assistive-technology requirements remain open decisions.
