---
version: 1
slug: "internal-configweb-assets-index-html"
primary_target: "internal/configweb/assets/index.html"
related_targets: ["internal/configweb/assets/app.js","internal/configweb/assets/style.css","internal/configweb/assets/i18n.js"]
---

# Desktop connection center

Visitor mode: Operate. Technically comfortable desktop users sign in to their embedded Tailscale device, choose an authorized exit, select split or global routing, and explicitly connect. Keep the everyday connection path compact and put node inspection in its own destination.

## Direction contract

THESIS: Connection owns sign-in, connection state, routing mode, and a current-exit row that opens a searchable picker dialog. Exit nodes is a separate primary navigation destination for inspecting availability, measuring latency, and choosing an exit. Connection, Exit nodes, and Logs sit at the top; Settings remains pinned at the bottom.

OWN-WORLD: Preserve the compact desktop UI, blue actions, neutral light/dark system surfaces, field rhythm, window chrome, and semantic state colors. Typography follows the user's website pairing: Montserrat for headings and actions, Monda for body text and node names, Lato for supporting text and small metadata. Bundle local Latin fonts with Chinese system fallbacks; retain monospace for YAML, logs and code-like configuration fields. Both the picker and node page use a single rounded Card list surface with internal dividers and 44px single-line rows. The user's latest direction fixes the order to an always-visible leading Phosphor icon (gray Check when unselected, green CheckCircle when selected), node name, right-aligned latency in an 88px column, and an explicit Ping button at the far right. No IP address, server icon, separate status/path column, or radio is shown. Latency is green through 500ms, amber above 500ms through 1000ms, and red above 1000ms. Ping runs independently; clicking the name or checkmark selects through the existing confirmation flow. Names truncate in narrow windows without horizontal scrolling. No new visual identity or illustration.

STORY: Sign in, open the exit picker, select and confirm, then connect. Cancel and Escape discard the picker selection. A disconnected app remains disconnected after a save; a running connection reconnects. Clicking a node name on its page opens the same confirmation path. Ping measures the embedded device's round-trip latency independently of selection, with direct/relay, pending, failure, and retry states.

FIRST VIEWPORT: A narrow persistent sidebar flanks a compact connection workspace. A Tailscale identity row precedes connection status/actions, the clickable current exit, and the split/global selector. No exit list appears on the home page. The modal contains search, online/unavailable rows, manual entry, and confirmation. Exit nodes presents a searchable grouped list with a leading gray/green checkmark, node name, right-aligned latency, and dedicated Ping button; Settings and Logs remain independent pages.

FORM: Desktop connection center and node directory, following the user's explicit request for modal selection, a separate Exit nodes menu, and Tailscale-based per-node ping. The incumbent visual-world reference, seed:413a3804ca48439d, is retained from this surface's previous contract; the grouped card lists and single-line density follow the user's supplied reference and subsequent corrections.

FINISH: Verify the rendered home, picker, and node page in English/Chinese, light/dark appearance, and narrow/wide windows. Review the finished flow and document the shipped behavior in DESIGN.md. No raster assets are introduced.

## Boundaries and states

Use existing authenticated loopback APIs and revision-checked configuration writes. Never expose authorization URLs or credentials. Search real exit metadata by name/address; retain the configured exit when it is missing or offline, but do not enable unavailable choices or silently choose a replacement. Only an explicit Ping action measures latency; no invented regions, benchmark claims, or automatic best-exit behavior. Preview results are explicitly simulated. Serialize pings, bound their duration, and discard results on network/device identity changes. Protect settings drafts by requiring save/discard before applying connection changes. Settings navigation preserves even invalid YAML drafts. Sign-in, discovery, connect, reconnect, offline, empty, failed, and disconnected states remain distinct. Preserve English/Chinese localization, system appearance, keyboard focus and reduced-motion support. No new dependency, release, commit, or publication is part of this change.
