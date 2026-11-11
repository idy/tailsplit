---
version: 1
slug: "internal-configweb-assets-tray-html"
primary_target: "internal/configweb/assets/tray.html"
related_targets: ["internal/configweb/assets/tray.css","internal/configweb/assets/tray.js"]
---

# Tray popup

Mode: Operate. A compact, repeated-use desktop surface anchored to the menu bar or tray. The user's Magpie screenshot pins the quiet, rounded desktop-popover direction. Inherit the existing panel's typography, semantic tokens, English/Chinese preference and system appearance.

## Direction contract

THESIS: Make the real connection and selected traffic exit clear at a glance. Remove the obsolete primary-device menu tree.

OWN-WORLD: The user's video reference pins a compact neutral popover, green brand mark and connection switch, a softly tinted connected row, fine detail-row dividers and two filled footer actions. Load the pinned Magpie 0.4.3 stylesheet before product styles; retain its semantic tokens, fonts, buttons and controlled switch geometry. The popup scopes checked tracks to green/amber/error state colors. Bundled Montserrat/Lato and established Chinese fallbacks.

STORY: Read connection status, connect or disconnect, inspect the current mode and gateway, then open the full panel for changes. Mode and gateway are strictly read-only: no click, selector, chevron, pointer cursor or hover treatment. Quit retains normal network cleanup.

FIRST VIEWPORT: A 360px compact panel with the TailSplit mark, name and connection switch in its header. A prominent, separately tinted connection row leads into two read-only mode and gateway rows, followed by equal-width Open TailSplit and Quit buttons. Routine status detail is visually hidden but remains accessible; setup, pending and recovery guidance stays visible. Empty configuration replaces the header switch with Set up exit. No charts, counters, tabs or companion. Native opening and outside/Escape dismissal supply the signature interaction; reduced motion follows system behavior.

FORM: User-pinned compact utility popover. seed: none (user-pinned).

FINISH: unreviewed and undocumented is unfinished; this build ends with the finish review, the verdict, DESIGN.md, and every shipping raster carrying its provenance
