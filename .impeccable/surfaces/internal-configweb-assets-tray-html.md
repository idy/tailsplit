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

OWN-WORLD: Load the pinned Magpie 0.4.2 stylesheet before product styles; use its semantic tokens, fonts, buttons and controlled switch track. Existing neutral canvas, white/dark grouped rows, inherited neutral connection switch and textual green/amber connection state. Bundled Montserrat/Monda/Lato and established Chinese fallbacks.

STORY: Read connection status, connect or disconnect, inspect the current mode and gateway, then open the full panel for changes. Mode and gateway are strictly read-only: no click, selector, chevron, pointer cursor or hover treatment. Quit retains normal network cleanup.

FIRST VIEWPORT: A 360px compact panel, TailSplit header with open-panel icon, a prominent status row with switch, two quiet detail rows for mode and exit, and a restrained footer with Open TailSplit and Quit. Empty configuration directs users to setup. No charts, counters, tabs or companion. Native opening and outside/Escape dismissal supply the signature interaction; reduced motion follows system behavior.

FORM: User-pinned compact utility popover. seed: none (user-pinned).

FINISH: unreviewed and undocumented is unfinished; this build ends with the finish review, the verdict, DESIGN.md, and every shipping raster carrying its provenance
