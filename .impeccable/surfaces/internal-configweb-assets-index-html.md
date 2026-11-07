---
version: 1
slug: "internal-configweb-assets-index-html"
primary_target: "internal/configweb/assets/index.html"
related_targets: ["internal/configweb/assets/app.js","internal/configweb/assets/style.css","internal/configweb/assets/i18n.js"]
---

# Desktop connection center

Visitor mode: Operate. Technically comfortable desktop users sign in to their embedded Tailscale device, choose an authorized exit, select split or global routing, and explicitly connect. The primary window must make those everyday actions available together.

## Direction contract

THESIS: One connection page owns sign-in, connection state, routing mode, and the searchable exit list. The primary sidebar contains Connection and Logs at the top, with Settings pinned at the bottom; network configuration no longer leads the app.

OWN-WORLD: Preserve the incumbent system-font desktop UI, blue actions, neutral light/dark system surfaces, compact field rhythm, existing window chrome, and semantic state colors. No new visual identity or illustration.

STORY: Sign in, select an exit, then connect. Selection is staged; an explicit button applies exit/mode changes. A disconnected app remains disconnected. A running connection visibly reconnects. Separate current configuration from a pending choice and preserve unsaved settings.

FIRST VIEWPORT: A narrow persistent sidebar flanks a connection workspace. A compact Tailscale identity row precedes connection status/actions and the split/global selector. The remaining area is a searchable, scrollable exit list with online/unavailable state and a confirmation footer. Settings and Logs occupy full independent pages.

FORM: Connection center with sidebar, selected by the user in the planning round. seed:413a3804ca48439d. The user's subsequent corrections fix the three navigation entries and place all connection and sign-in functions on one page.

FINISH: unreviewed and undocumented is unfinished; this build ends with the finish review, the verdict, DESIGN.md, and every shipping raster carrying its provenance

## Boundaries and states

Use existing authenticated loopback APIs and revision-checked configuration writes. Never expose authorization URLs or credentials. Search real exit metadata by name/address; retain the configured exit when it is missing or offline, but do not enable unavailable choices or silently choose a replacement. No invented latency, regions, or automatic best-exit behavior. Protect settings drafts by requiring save/discard before applying quick connection changes. Settings navigation must preserve even invalid YAML drafts. Sign-in, discovery, connect, reconnect, offline, empty, failed, and disconnected states remain distinct. Preserve English/Chinese localization, system appearance, keyboard focus and reduced-motion support. No new dependency, release, commit, or publication is part of this change.
