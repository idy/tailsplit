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

TailSplit is an open-source routing client based on sing-box rule lists. It can use several exits at once, each an embedded device signed in to its own Tailnet (or the same one) with one selected exit node, and routing rules choose which exit carries each destination. DNS behavior follows the routing policy.

Public introductions focus on sing-box rule lists, multiple Tailscale networks and Tailscale exit nodes. Each additional exit is a separate device that the user authorizes in its Tailnet; describe it that way rather than as one device joining several networks.

The user chooses their exit and controls their configuration. TailRelay is the separately linked companion for providing a SOCKS5-backed Tailnet exit; keep the two products' roles distinct.

## Operating Context

- The website explains routing, installation and connection behavior, and links to actual GitHub release assets, checksums, source and documentation.
- A fresh desktop installation has no saved devices or gateway and starts idle. New configurations enable system TUN, FakeIP and automatic connection once a gateway is configured; direct Internet IPv6 is off, routing is Split with builtin rules, and unmatched traffic goes direct. Device authorization opens the default browser only on request; signing in and starting a connection are separate actions. Existing configuration files and their omitted-field behavior are preserved.
- Home is the default desktop destination. A fresh installation needing an exit shows a primary Set up button; configured installations show the connection switch. Both express the connection request and guide service authorization, Tailscale sign-in and node selection as needed. Save & connect completes the chosen node and gateway before starting traffic. Login and exit discovery alone do not create a TUN. The home topology follows the supplied GizMoth serial-map reference: this computer fans out to Direct and each exit referenced by saved routing. Global shows only its selected exit. Per-destination upload/download speeds use real router-byte deltas within one engine session; unknown readings show an em dash, confirmed idle intervals can show zero, and reconnection resets the baseline.
- Desktop use continues through the macOS menu bar or Windows tray and the local configuration window. Its primary sidebar has Home, Exit Nodes and Logs at the top, with Settings pinned at the bottom. Exit Nodes opens directly to the full-width list: the optional legacy `tailscale` profile (`main`) first when present, then additional exits. Add exit sits above the list on the left, with a compact text dropdown showing the current default or Global exit, or Not set, on the right. Choosing a gateway saves directly without an edit dialog. A disconnected app stays disconnected and does not sign in; changing the gateway during an active session saves and reconnects. Exit selectors show names without appended IP addresses. There is no exit-count statistic. The top-right titlebar has an unboxed routing dropdown with Rule-based / Global (Kill-Switch) options for the existing Split/Global modes, the connection switch, then Refresh at the far right. The shortcut saves immediately, reconnects active traffic and retains Split rules; it does not add a persistent firewall. Unsaved drafts or an unconfigured target gateway during active traffic block mode switching. Turning on with no saved devices guides Add exit, sign-in and node selection, then saves the confirmed node and default exit and continues this explicit connection. A saved device missing login or a node is guided through that step. Multiple saved devices without a default still open a choice dialog with no preselected option. The user must choose and confirm before the app saves the selection and connects. Native menu/tray Connect uses the same flow. A temporary status banner explains installation, authorization and the next step; failures provide explicit Retry and Cancel actions. Plain Add or Sign in still stays disconnected. Cancel/Escape changes nothing; failed or stale saves never start traffic. Unsaved settings drafts block connection. An already configured but offline default is never silently replaced. The page owns list scrolling; there is no separate connection card or nested card scroller. Closing the configuration window keeps the app available in the menu bar or tray.
- Adding an exit requires desktop action capability and a valid saved document. It stops an active connection and opens authorization using a private staged configuration. Only after authentication and helper cleanup does it commit the new profile and show its row and node picker. Canceling or failed authentication leaves no provisional profile or list item. Active Global mode first requires explicit consent to stop Kill-Switch protection and restore system networking; Cancel/Escape changes nothing, and failures after consent leave the app disconnected until manual reconnection. There is no preliminary name dialog. Confirming the first node binds the profile's Tailnet guard and names it `<exit-device-name>@<network name>`. Automatic names follow subsequent node choices; existing custom names and stable profile IDs remain intact. Each exit's list-only dialog shows the device identity, Sign in when needed, its Tailnet's nodes and individual Tailscale Ping actions. Remove exit is at the bottom for every profile, including `main` and the last exit; no standalone switch-network or sign-out action is shown. The whole dialog scrolls, without a separate node-card scroller. Node selection uses a plain green check, with no selected/hover row fill or pointer blue focus box; keyboard focus remains visible.
- Settings › Routing rules owns the saved Split/Global selection. Split exposes an independent default proxy exit, an unmatched-traffic choice and rule groups for domains and CIDRs. Global exposes one exit selector and hides/disables Split controls while retaining their configuration. The Default or Global exit badge follows the saved mode's selector, rather than the legacy primary identity. All form dropdowns use the shared `select.js` control, with an unboxed text trigger for Log level. Node confirmation and settings Save & apply reconnect active sessions, while idle apps stay idle; removal always leaves the app stopped. Settings drafts must be saved or discarded before applying exit changes.
- An exit without a selected node carries no traffic. Ping uses that exit's embedded device, does not switch exits or start traffic capture, and measures latency rather than download speed. Direct/relay paths and failure details remain available with each result.
- Common settings use a form; the full YAML editor handles additional configuration. Users can validate drafts before saving. Saving and applying reconnects an active session while an idle app stays idle.
- The CLI supports offline initialization, validation and configuration rendering. Starting a connection requires the relevant network privileges.

## Capabilities and Constraints

- **Routing:** sing-box rule lists with domain and IP/CIDR matching, a configurable final route, and configured Tailnet destinations. Split's `rules.default_exit` is independent of `rules.final`: it carries proxy matches from builtin/custom sources, `proxy_domains`, `proxy_ips`, `final: proxy`, and unmatched bare Tailnet addresses (`100.64.0.0/10` and `fd7a:115c:a1e0::/48`). Explicit direct rules and per-exit groups retain their precedence over that address fallback. `rules.final` chooses direct, the default proxy exit, or any configured exit, including `main`, for otherwise unmatched traffic. Global runs only `rules.global_exit`, sends all captured traffic through it and retains the saved Split policy.
- **Connection modes:** application proxy exposes local HTTP/SOCKS listeners for applications that opt in. System routing adds broader TUN traffic capture and system DNS changes. Both connected modes require a DNS TUN and the network service or equivalent CLI privileges; `tun.enabled: false` does not remove the DNS TUN.
- **Exit behavior:** unavailable selected exits block their traffic while the relevant TUN is active. TailSplit does not silently select a different exit. Disconnecting, quitting or a service crash removes the managed TUN and temporary DNS state; the global-mode protection is not a persistent system-wide firewall kill switch.
- **Multiple exits:** the optional legacy `tailscale` profile has logical ID `main` and state at `state_dir/tailscale`. Additional exits have stable IDs, device state at `state_dir/exits/<id>/tailscale`, their own login and one exit node. Removing `main` never promotes or renames an additional exit. An additional exit whose `tailnet` is a `.ts.net` MagicDNS suffix also receives that suffix. Both exit selectors default to `main` when omitted; this is a compatibility default or empty-draft placeholder, not a fixed primary role. Additional omitted hostnames derive from the automatic host base and stable ID independently of the primary; set an explicit hostname to retain an older custom-primary-derived spelling.
- **Exit removal and empty drafts:** confirmation disconnects and signs out only the selected local client. Configuration revision checks run before logout and again before saving. Removal deletes the local profile and marks removed or missing default/global selections explicitly unset (`"-"`). Explicit final and route-group references are retained as dormant policy; Split connection is blocked until their missing targets are explicitly replaced or edited, even when another default gateway is ready. Removing `main` retains its `dns.magic_domains` as unresolved ownership; former `tailscale.domains` become a retained `main` route group so private names cannot silently fall through. Other policy and remaining device identities stay intact, and the app stays stopped. The remote Tailnet client record remains because no management authority to delete it is implemented. Removing the last exit preserves remaining routing policy as a dormant draft; connection still requires the devices that policy needs. Add exit then creates a new additional profile and leaves both selectors unset until the user confirms a traffic exit. Choose a network during Add exit authorization; there is no separate desktop network-switch or sign-out control.
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
