---
name: "TailSplit Desktop Configuration Panel"
description: "Compact desktop controls that follow system appearance."
colors:
  canvas: "#f5f5f7"
  sidebar: "#ededf0"
  surface: "#fff"
  raised: "#e9e9ee"
  field: "#fff"
  code: "#f0f0f4"
  line: "#00000014"
  line-strong: "#85858f"
  ink: "#1c1c1e"
  muted: "#62626c"
  faint: "#6c6c76"
  secondary-ink: "#42424a"
  soft-ink: "#4e4e56"
  emphasis-ink: "#1c1c1e"
  subtle-ink: "#55555f"
  accent: "#2f62d5"
  accent-hover: "#2856c1"
  accent-ink: "#2459c4"
  good: "#167536"
  good-ink: "#16652f"
  warn: "#855700"
  error: "#b42318"
  selected: "#0000000e"
  hover: "#00000006"
  wash: "#00000003"
  button-hover: "#dedee6"
  field-hover: "#62626c"
  subtle-hover: "#00000008"
  update-bg: "#2f62d50d"
  switch-off: "#85858f"
  code-key: "#2459b8"
  code-value: "#25673b"
  raw-ink: "#28282e"
  actionbar: "#f5f5f7f0"
  on-accent: "#fff"
  canvas-dark: "#1c1c1e"
  sidebar-dark: "#18181a"
  surface-dark: "#232326"
  raised-dark: "#2c2c30"
  field-dark: "#2a2a2e"
  code-dark: "#161618"
  line-dark: "#ffffff12"
  line-strong-dark: "#ffffff1f"
  ink-dark: "#ececf0"
  muted-dark: "#9a9aa2"
  faint-dark: "#92929c"
  secondary-ink-dark: "#d1d1d6"
  soft-ink-dark: "#c7c7cc"
  emphasis-ink-dark: "#fff"
  subtle-ink-dark: "#aeaeb2"
  accent-hover-dark: "#366bdd"
  accent-ink-dark: "#8ab4ff"
  good-dark: "#30d158"
  good-ink-dark: "#d7f7df"
  warn-dark: "#f0c46b"
  error-dark: "#ff8a80"
  selected-dark: "#ffffff14"
  hover-dark: "#ffffff0a"
  wash-dark: "#ffffff06"
  button-hover-dark: "#34343a"
  field-hover-dark: "#ffffff33"
  subtle-hover-dark: "#ffffff0d"
  update-bg-dark: "#3b78f61f"
  switch-off-dark: "#3a3a3f"
  code-key-dark: "#8eb4ff"
  code-value-dark: "#b8e3c4"
  raw-ink-dark: "#e5e5ea"
  actionbar-dark: "#1c1c1ef0"
typography:
  headline:
    fontFamily: "-apple-system, BlinkMacSystemFont, \"SF Pro Text\", \"Segoe UI Variable Text\", \"Segoe UI\", \"PingFang SC\", \"Microsoft YaHei UI\", \"Microsoft YaHei\", sans-serif"
    fontSize: "1.5rem"
    fontWeight: 600
    lineHeight: 1.35
    letterSpacing: "-.02em"
  title:
    fontFamily: "-apple-system, BlinkMacSystemFont, \"SF Pro Text\", \"Segoe UI Variable Text\", \"Segoe UI\", \"PingFang SC\", \"Microsoft YaHei UI\", \"Microsoft YaHei\", sans-serif"
    fontSize: "1rem"
    fontWeight: 600
    lineHeight: 1.4
  body:
    fontFamily: "-apple-system, BlinkMacSystemFont, \"SF Pro Text\", \"Segoe UI Variable Text\", \"Segoe UI\", \"PingFang SC\", \"Microsoft YaHei UI\", \"Microsoft YaHei\", sans-serif"
    fontSize: "0.875rem"
    fontWeight: 400
    lineHeight: 1.5
  label:
    fontFamily: "-apple-system, BlinkMacSystemFont, \"SF Pro Text\", \"Segoe UI Variable Text\", \"Segoe UI\", \"PingFang SC\", \"Microsoft YaHei UI\", \"Microsoft YaHei\", sans-serif"
    fontSize: "0.875rem"
    fontWeight: 500
    lineHeight: 1.5
  note:
    fontFamily: "-apple-system, BlinkMacSystemFont, \"SF Pro Text\", \"Segoe UI Variable Text\", \"Segoe UI\", \"PingFang SC\", \"Microsoft YaHei UI\", \"Microsoft YaHei\", sans-serif"
    fontSize: "0.8125rem"
    fontWeight: 400
    lineHeight: 1.55
  button:
    fontFamily: "-apple-system, BlinkMacSystemFont, \"SF Pro Text\", \"Segoe UI Variable Text\", \"Segoe UI\", \"PingFang SC\", \"Microsoft YaHei UI\", \"Microsoft YaHei\", sans-serif"
    fontSize: "0.8125rem"
    fontWeight: 500
    lineHeight: 1.5
  caption:
    fontFamily: "-apple-system, BlinkMacSystemFont, \"SF Pro Text\", \"Segoe UI Variable Text\", \"Segoe UI\", \"PingFang SC\", \"Microsoft YaHei UI\", \"Microsoft YaHei\", sans-serif"
    fontSize: "0.75rem"
    fontWeight: 400
    lineHeight: 1.5
  code:
    fontFamily: "\"SF Mono\", \"SFMono-Regular\", \"Cascadia Mono\", Consolas, \"Liberation Mono\", monospace"
    fontSize: "0.8125rem"
    fontWeight: 400
    lineHeight: 1.65
rounded:
  inline: "4px"
  segment: "5px"
  control: "6px"
  container: "8px"
  panel: "10px"
  dialog: "12px"
  pill: "999px"
spacing:
  compact: "4px"
  control-gap: "8px"
  row-gap: "12px"
  inset: "16px"
  field-gap: "20px"
  section-gap: "20px"
  workspace-gutter: "24px"
components:
  button-primary:
    backgroundColor: "{colors.accent}"
    textColor: "{colors.on-accent}"
    typography: "{typography.button}"
    rounded: "{rounded.control}"
    padding: "6px 16px"
  button-primary-hover:
    backgroundColor: "{colors.accent-hover}"
  button-secondary:
    backgroundColor: "{colors.raised}"
    textColor: "{colors.ink}"
    typography: "{typography.button}"
    rounded: "{rounded.control}"
    padding: "6px 12px"
  button-subtle:
    backgroundColor: "transparent"
    textColor: "{colors.subtle-ink}"
    typography: "{typography.button}"
    rounded: "{rounded.control}"
    padding: "6px 12px"
  input:
    backgroundColor: "{colors.field}"
    textColor: "{colors.ink}"
    rounded: "{rounded.control}"
    padding: "6px 10px"
  nav-item:
    backgroundColor: "transparent"
    textColor: "{colors.soft-ink}"
    rounded: "{rounded.control}"
    padding: "8px 10px"
  nav-item-active:
    backgroundColor: "{colors.selected}"
    textColor: "{colors.emphasis-ink}"
  settings-group:
    backgroundColor: "{colors.surface}"
    rounded: "{rounded.panel}"
  connection-panel:
    backgroundColor: "{colors.surface}"
    rounded: "{rounded.panel}"
    padding: "20px"
  mode-option:
    backgroundColor: "transparent"
    textColor: "{colors.muted}"
    rounded: "{rounded.segment}"
    padding: "5px 14px"
  mode-option-selected:
    backgroundColor: "{colors.selected}"
    textColor: "{colors.emphasis-ink}"
  status:
    textColor: "{colors.soft-ink}"
    typography: "{typography.caption}"
---
# Design System: TailSplit Desktop Configuration Panel

## Overview

**Creative North Star: "The Desktop Configuration Panel"**

TailSplit uses a compact, neutral desktop configuration panel with system typography, blue actions, and semantic connection states. This system applies to the embedded configuration UI in macOS WKWebView and Windows WebView2. It preserves the incumbent desktop world selected by the user.

System appearance changes the surfaces, text, borders, and state treatments through CSS media queries. Density supports repeated local configuration tasks; clear labels, separate draft states, and explicit actions carry the hierarchy.

**Key Characteristics:**

- Neutral surfaces that follow system appearance.
- Compact controls with visible keyboard focus.
- Text-backed semantic state colors.
- Native window chrome and practical configuration density.

Source of truth: `internal/configweb/assets/style.css`, `index.html`, and the exit-row renderer in `app.js`. The surface brief records the selected world and connection-center composition. Tokens and layout rules are derived from the implemented source. This document does not establish accessibility or native-runtime conformance.

## Colors

Neutral gray surfaces carry the interface; blue identifies actions and selected routing controls. The frontmatter preserves source CSS color strings. A `-dark` entry is the corresponding media-query override; absent overrides retain their base value. These are documentation keys, while the implementation uses the unsuffixed CSS custom properties inside each appearance.

### Primary

- **Action blue:** `accent` fills primary buttons, checked switches, and radio choices; `on-accent` provides their button text.
- **Action blue hover and ink:** `accent-hover` handles primary hover; `accent-ink` handles focus outlines, links, current exit labels, and proxy-route markers.
- **Selection wash:** `update-bg` lightly marks a selected exit without representing connection success.

### Neutral

- **Canvas and sidebar:** `canvas` and `sidebar` separate workspace from persistent navigation.
- **Surface, raised, field, code:** grouped settings and connection panels, buttons, editable fields, and diagnostic/configuration regions respectively.
- **Ink family:** `ink` is primary text; `secondary-ink`, `soft-ink`, `subtle-ink`, `muted`, and `faint` serve subordinate text and controls. `emphasis-ink` highlights active navigation.
- **Line family:** `line` divides groups and rows; `line-strong` defines editable controls and secondary buttons.
- **Interaction neutrals:** `hover`, `selected`, `wash`, `button-hover`, `field-hover`, and `subtle-hover` support existing interaction states. `actionbar` is the translucent settings footer surface.
- **Configuration syntax:** `code-key`, `code-value`, and `raw-ink` distinguish YAML syntax from ordinary interface text.

Green (`good`, `good-ink`) means connected/saved or direct routing; amber (`warn`) marks attention, pending choices, or unsaved work; red (`error`) marks failure. These are functional states rather than additional brand accents.

**The Appearance Rule.** Use the existing semantic CSS properties so light and dark appearance change together. The accent fill remains shared; accent text and hover colors adapt.

**The State Rule.** Pair semantic color with a readable state label; selected, pending, current, connected, warning, and error are distinct.

## Typography

The frontmatter records the full system interface and mono stacks, including Chinese fallbacks. There is no dedicated display face. At the root font size (100%), body is 14px, note/button/code 13px, caption 12px, section title 16px, and connection state 24px. The hierarchy is compact rather than a proportional marketing scale.

- **Headline:** the connection state, semibold and slightly tightened.
- **Title:** section headings, semibold with a compact line height.
- **Body and label:** ordinary text and medium-weight field labels. Navigation uses the body size with a tighter line height (1.4); active navigation is medium weight.
- **Note:** secondary explanation and hints, commonly limited to 60–75ch depending on context.
- **Button:** medium-weight compact action text.
- **Caption:** status, file metadata, and exit addresses; addresses use the mono family.
- **Code:** YAML preview/raw editor line height (1.65). Logs use the same size and family with a roomier line height (1.7).

Dialog titles use an observed local step (1.125rem); the brand uses a local step (0.9375rem, weight 650). These are component exceptions, not a second display system.

**The Desktop Type Rule.** Use the incumbent system text stack for interface text and the mono stack for configuration and diagnostic content.

## Layout

A persistent sidebar and flexible workspace share a sticky titlebar. The default sidebar is (176px) and titlebar (48px); macOS reserves traffic-light space with a wider sidebar (216px), shorter titlebar (40px), and brand left inset (88px). Windows controls fill the titlebar height with square-edged buttons (44px wide). The source comment explicitly identifies this fixed-height native chrome; its flush control edges do not establish a general content-spacing rule.

Workspace gutters, field gaps, and section gaps use the frontmatter spacing tokens. Pages have vertical padding (24px). Connection content centers within a maximum width (1000px). Connection and Logs occupy the upper sidebar; Settings is pinned below by the flexible sidebar layout. The connection page places identity, state/actions, routing selector, exit search/list, and staged confirmation together. This composition belongs to the desktop connection surface, not a universal page template.

Settings use a form/preview grid (2fr/1fr, preview minimum 320px) and label/control rows (150–190px label column). Exit rows have a minimum height (62px); their list scrolls within `min(36vh, 360px)`. Confirmation and settings actions stay near the bottom through sticky footers. The sidebar Settings footer follows the visible settings actionbar's measured height so their full-width dividers align, including when localized actions wrap.

- At (1199px) and below, hide the live YAML preview while retaining the full-width form and YAML editor tab.
- At (960px) and below, hide the header exit label.
- At (860px) and below, use a sidebar (172px; macOS 212px), gutters (20px), and stacked field rows; toggles retain a label/control arrangement.
- At (640px) and below, use an icon sidebar (52px), gutters (16px), wrapping actions, full-width mode segments, and one-column rule pairs. Navigation text remains available to assistive technology.
- At heights (680px) and below with width at least (641px), compact connection-page vertical spacing.
- Coarse pointers increase listed action targets and fields to at least (44px); inputs use text (16px). This is an input-mode adjustment rather than a new desktop density.

These responsive rules describe the stylesheet; their presence does not guarantee behavior across all window sizes, input modes, or native runtimes.

## Elevation & Depth

Panels are flat, separated by tone and fine borders. The settings actionbar layers a translucent canvas and blur (12px). Account menus use a border and surface fill. Toasts, dialogs, and switch thumbs use the small shadow vocabulary in the sidecar. Modal backdrops use explicit light/dark black scrims and blur (2px); the source avoids inherited custom properties there for Chromium 120 compatibility.

**The Flat Surface Rule.** Use tonal surfaces and hairline separators for ordinary panels; reserve ambient shadows for overlays and the small switch thumb.

Field border/shadow transitions last (.12s); switch background and thumb transitions last (.15s). Loading and update indicators rotate linearly (.8s and 1s respectively). Reduced-motion preference disables animations, transitions, and smooth scrolling.

## Shapes

Controls and navigation use modest rounded corners; grouped panels are gently rounder. The frontmatter records the reused radius steps. Mode segments use their tighter radius inside a rounded container. Pills, dots, and switch tracks remain fully rounded. Borders are ordinarily (1px). Titlebar window controls retain square edges. Interface icons are inline SVG strokes, normally (16px) with stroke width (1.7); compact action icons are commonly (14px).

## Components

### Buttons

Primary actions use blue fill and white text; secondary actions use raised neutral fill and a strong border; subtle actions use transparent fill. Normal buttons have a minimum height (34px), small buttons (32px). Hover changes fill; global focus is a visible accent outline (2px, offset 2px). Disabled buttons reduce opacity (.45). No transform-based hover elevation is present.

### Inputs / Fields

Fields use a strong border, field surface, control radius, and minimum height (34px). Focus changes the border and adds the accent outline. Placeholder text uses faint ink; disabled fields use opacity (.6). Multiline fields resize vertically. The exit search reserves space for its inline SVG search icon.

### Navigation

Primary navigation uses neutral hover and selected fills, medium active text, and a current-page attribute. Settings category buttons reuse this neutral selection language. Editor tabs and routing segments sit inside low-contrast rounded tracks; the selected segment is neutral rather than a primary action.

### Cards / Containers

Grouped settings share a surface and panel radius, with internal row dividers and row padding (12px 16px). The connection panel uses the same surface and radius with a larger inset. YAML and log regions use the code surface. Cards do not acquire shadows on hover.

### Status and Exit Selection

Header connection state pairs a small colored dot (7px) with text. Exit rows pair radio selection, device name, mono address, and a readable status. Blue current-state text and amber pending-state text remain distinct from the selected-row wash. Unavailable exits use disabled choices, not a fictional success treatment. A sticky confirmation footer identifies a staged change and its consequence; settings drafts have their own amber warning and sidebar dot.

### Switches and Overlays

Switches use a track (34px by 20px), thumb (14px), neutral off fill, and blue on fill. The invisible native input retains focus and checked semantics. Dialogs use the surface fill, dialog radius, strong border, and ambient shadow; toast and modal geometry remain separate from ordinary grouped panels.

## Do's and Don'ts

### Do:

- Do use semantic CSS properties for both system appearances.
- Do retain visible keyboard focus and reduced-motion behavior.
- Do allow English and Chinese labels, long addresses, and helper text to wrap where the source provides for it.
- Do keep current and pending selections visibly distinct and provide explicit confirmation.

### Don't:

- Don't apply this desktop panel system to the public website without inspecting that surface's own implementation.
- Don't replace the chosen neutral desktop world with a new visual identity or illustration.
- Don't imply a connection or saved configuration from selection styling alone.

Not canonized or repaired: the reported detector warning about titlebar children flush against the bottom border is limited to existing fixed-height native chrome, as explained by the source comment. It does not justify copying flush content spacing into ordinary panels.
