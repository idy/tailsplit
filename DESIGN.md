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
  latency-fast: "#3a8154"
  latency-medium: "#906d29"
  latency-slow: "#bd524b"
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
  log-background: "#272a32"
  log-ink: "#e5e5ea"
  log-muted: "#aeb4c0"
  log-cyan: "#70cbd6"
  log-magenta: "#dca0e6"
  log-selection: "#3b537c"
  log-line: "#ffffff1f"
  log-scroll-thumb: "#ffffff66"
  log-scroll-thumb-hover: "#ffffff88"
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
  latency-fast-dark: "#7ab58b"
  latency-medium-dark: "#c9b17c"
  latency-slow-dark: "#d49a94"
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
    fontSize: "1.25rem"
    fontWeight: 600
    lineHeight: 1.3
    letterSpacing: "-.01em"
  title:
    fontFamily: "-apple-system, BlinkMacSystemFont, \"SF Pro Text\", \"Segoe UI Variable Text\", \"Segoe UI\", \"PingFang SC\", \"Microsoft YaHei UI\", \"Microsoft YaHei\", sans-serif"
    fontSize: "0.9375rem"
    fontWeight: 600
    lineHeight: 1.4
  body:
    fontFamily: "-apple-system, BlinkMacSystemFont, \"SF Pro Text\", \"Segoe UI Variable Text\", \"Segoe UI\", \"PingFang SC\", \"Microsoft YaHei UI\", \"Microsoft YaHei\", sans-serif"
    fontSize: "0.8125rem"
    fontWeight: 400
    lineHeight: 1.5
  label:
    fontFamily: "-apple-system, BlinkMacSystemFont, \"SF Pro Text\", \"Segoe UI Variable Text\", \"Segoe UI\", \"PingFang SC\", \"Microsoft YaHei UI\", \"Microsoft YaHei\", sans-serif"
    fontSize: "0.8125rem"
    fontWeight: 500
    lineHeight: 1.4
  note:
    fontFamily: "-apple-system, BlinkMacSystemFont, \"SF Pro Text\", \"Segoe UI Variable Text\", \"Segoe UI\", \"PingFang SC\", \"Microsoft YaHei UI\", \"Microsoft YaHei\", sans-serif"
    fontSize: "0.75rem"
    fontWeight: 400
    lineHeight: 1.45
  button:
    fontFamily: "-apple-system, BlinkMacSystemFont, \"SF Pro Text\", \"Segoe UI Variable Text\", \"Segoe UI\", \"PingFang SC\", \"Microsoft YaHei UI\", \"Microsoft YaHei\", sans-serif"
    fontSize: "0.8125rem"
    fontWeight: 500
    lineHeight: 1.4
  caption:
    fontFamily: "-apple-system, BlinkMacSystemFont, \"SF Pro Text\", \"Segoe UI Variable Text\", \"Segoe UI\", \"PingFang SC\", \"Microsoft YaHei UI\", \"Microsoft YaHei\", sans-serif"
    fontSize: "0.6875rem"
    fontWeight: 400
    lineHeight: 1.5
  code:
    fontFamily: "\"SF Mono\", \"SFMono-Regular\", \"Cascadia Mono\", Consolas, \"Liberation Mono\", monospace"
    fontSize: "0.75rem"
    fontWeight: 400
    lineHeight: 1.65
rounded:
  inline: "4px"
  segment: "5px"
  control: "6px"
  container: "8px"
  panel: "16px"
  dialog: "20px"
  pill: "999px"
spacing:
  compact: "4px"
  control-gap: "8px"
  row-gap: "12px"
  inset: "16px"
  field-gap: "16px"
  section-gap: "16px"
  workspace-gutter: "24px"
components:
  button-primary:
    backgroundColor: "{colors.accent}"
    textColor: "{colors.on-accent}"
    typography: "{typography.button}"
    rounded: "{rounded.control}"
    padding: "3px 14px"
  button-primary-hover:
    backgroundColor: "{colors.accent-hover}"
  button-secondary:
    backgroundColor: "{colors.raised}"
    textColor: "{colors.ink}"
    typography: "{typography.button}"
    rounded: "{rounded.control}"
    padding: "3px 12px"
  button-subtle:
    backgroundColor: "transparent"
    textColor: "{colors.subtle-ink}"
    typography: "{typography.button}"
    rounded: "{rounded.control}"
    padding: "3px 12px"
  input:
    backgroundColor: "{colors.field}"
    textColor: "{colors.ink}"
    rounded: "{rounded.control}"
    padding: "3px 8px"
  nav-item:
    backgroundColor: "transparent"
    textColor: "{colors.soft-ink}"
    rounded: "{rounded.control}"
    padding: "4px 8px"
  nav-item-active:
    backgroundColor: "{colors.selected}"
    textColor: "{colors.emphasis-ink}"
  settings-group:
    backgroundColor: "{colors.surface}"
    rounded: "{rounded.panel}"
  mode-option:
    backgroundColor: "transparent"
    textColor: "{colors.muted}"
    rounded: "{rounded.segment}"
    padding: "2px 12px"
  mode-option-selected:
    backgroundColor: "{colors.surface}"
    textColor: "{colors.emphasis-ink}"
  status:
    textColor: "{colors.soft-ink}"
    typography: "{typography.caption}"
---
# Design System: TailSplit Desktop Configuration Panel

## Overview

**Creative North Star: "The Desktop Configuration Panel"**

TailSplit uses a compact, neutral desktop configuration panel with the platform UI typeface, magpie-like information density, blue actions, and semantic connection states. This system applies to the embedded configuration UI in macOS WKWebView and Windows WebView2. It preserves the incumbent desktop world selected by the user.

System appearance changes the surfaces, text, borders, and state treatments through CSS media queries. Density supports repeated local configuration tasks; clear labels, separate draft states, and explicit actions carry the hierarchy.

**Key Characteristics:**

- Neutral surfaces that follow system appearance.
- Compact controls with visible keyboard focus.
- Text-backed semantic state colors.
- Native window chrome and practical configuration density.

Source of truth: `internal/configweb/assets/style.css`, `index.html`, and the exit-row renderer in `app.js`. The surface brief records the selected world and connection-center composition. Tokens and layout rules are derived from the implemented source. This document does not establish accessibility or native-runtime conformance.

## Colors

Neutral gray surfaces carry the interface; blue identifies primary actions and keyboard focus, while a plain green check identifies the selected node. The frontmatter preserves source CSS color strings. A `-dark` entry is the corresponding media-query override; absent overrides retain their base value. These are documentation keys, while the implementation uses the unsuffixed CSS custom properties inside each appearance.

### Primary

- **Action blue:** `accent` fills primary buttons and checked switches; `on-accent` provides their button text. Node rows reserve an empty checkmark slot when unselected and show a plain `good`-green check when selected; no radio control is rendered.
- **Action blue hover and ink:** `accent-hover` handles primary hover; `accent-ink` handles keyboard focus outlines, links, current exit labels, and proxy-route markers. Pointer selection of a node leaves no blue focus box.
- **Selection treatment:** node rows have no selected or hover fill. Navigation and active dropdown options retain their neutral selection surfaces; `update-bg` serves update indicators.

### Neutral

- **Canvas and sidebar:** `canvas` and `sidebar` separate workspace from persistent navigation.
- **Surface, raised, field, code:** grouped settings and exit lists, buttons, editable fields, and diagnostic/configuration regions respectively.
- **Ink family:** `ink` is primary text; `secondary-ink`, `soft-ink`, `subtle-ink`, `muted`, and `faint` serve subordinate text and controls. `emphasis-ink` highlights active navigation.
- **Line family:** `line` divides groups and rows; `line-strong` defines editable controls and secondary buttons.
- **Interaction neutrals:** `hover`, `selected`, `wash`, `button-hover`, `field-hover`, and `subtle-hover` support existing interaction states. `actionbar` is the translucent settings footer surface.
- **Configuration syntax:** `code-key`, `code-value`, and `raw-ink` distinguish YAML syntax from ordinary interface text.

Green (`good`, `good-ink`) means connected/saved or direct routing; amber (`warn`) marks attention, pending choices, or unsaved work; red (`error`) marks failure. These are functional states rather than additional brand accents.

**The Appearance Rule.** Use the existing semantic CSS properties so light and dark appearance change together. The accent fill remains shared; accent text and hover colors adapt.

**The State Rule.** Pair semantic color with a readable state label; selected, pending, current, connected, warning, and error are distinct.

## Typography

The panel uses one platform UI face, following the user's magpie reference: SF Pro on macOS and Segoe UI Variable/Segoe UI on Windows, with PingFang SC and Microsoft YaHei for Chinese. At the root font size (100%), body, labels, navigation and buttons are 13px; notes, hints, latency and small buttons 12px; captions and the sidebar footer 11px; section titles 15px; dialog titles 16px; and the connection state 20px. Weight, not family, carries hierarchy: 500 for labels, selected navigation and the current page, 600 for titles and the connection state.

- **Titlebar path:** the titlebar names the page as a muted parent, a faint slash and the current page (for example "Settings / Network"; top-level pages show only their name). Visible in-page h2 headings are removed and remain for assistive technology; the page's one-line description leads the content instead.
- **Ping:** a text-only 12px action in `accent-ink`, underlined on hover, with muted disabled text.
- **Latency:** 12px regular with tabular numbers and softened semantic colors.
- **Code:** the existing mono stack at 12px for YAML, diagnostic logs, filenames and code-like inputs. YAML uses line height 1.65 and logs 1.7.

The panel no longer loads Montserrat, Monda or Lato. Their WOFF2 files and OFL notices remain embedded and served under `internal/configweb/assets/fonts/` for compatibility with the existing server routes; the public website still uses them from `site/shared/`.

**The Desktop Type Rule.** Use the platform face at the documented sizes; keep the mono stack for configuration and diagnostics. The user chose this over the earlier website pairing, so generic display-face advisories do not override it.

## Layout

A persistent sidebar and flexible workspace share a sticky titlebar. The default sidebar is (200px) and titlebar (44px); macOS reserves traffic-light space with a wider sidebar (216px), shorter titlebar (40px), and brand left inset (88px). Windows controls fill the titlebar height with square-edged buttons (44px wide). The source comment explicitly identifies this fixed-height native chrome; its flush control edges do not establish a general content-spacing rule.

Workspace gutters, field gaps, and section gaps use the frontmatter spacing tokens. Pages have vertical padding (20px). The exit list fills the available workspace width between the page gutters. Exit Nodes and Logs occupy the upper sidebar; Settings is pinned below by the flexible sidebar layout. While Settings is the current page it expands in place into a second-level submenu of its categories, indented under a 1px guide line aligned with the Settings icon; there is no in-page category tab bar. The Form/YAML segmented control and an icon-only Reload action sit in the titlebar while Settings is open. A sidebar footer shows the saved local proxy listener with a dot that turns green while connected; its top divider follows the visible settings actionbar's measured height so both full-width dividers align.

Exit Nodes opens directly to the **Exits** card: the optional legacy `tailscale` profile (`main`) first when present, followed by additional exits. A toolbar above the card places Add exit at the left and a compact shared text dropdown showing the saved Split default proxy exit or Global exit, or Not set, at the right. Clicking opens its menu directly, with no edit dialog. Choosing a gateway saves immediately; an idle app stays disconnected, while active traffic reconnects. Keeping the same selection does not write or reconnect. Drafts and revision conflicts block changes; a failed save restores the saved selection. There is no saved-exit count. The titlebar orders connection status, an unboxed routing-mode text dropdown, the master connection switch, and Refresh at the far right, with no default-exit summary. The shared dropdown offers Rule-based / Global (Kill-Switch), or 规则分流 / 全局（Kill-Switch） in Chinese. Peer discovery during connection preflight remains in a checking state until ready, rather than prematurely prompting for a new exit. There is no separate protection toggle or confirmation dialog. It edits the existing Global/TUN mode, saves immediately and reconnects an active session; turning it off returns to Split while retaining policy. Drafts, busy state and an unconfigured target gateway during active traffic disable the shortcut. Its tooltip explains that manual disconnect restores system networking. The list's default display keeps its caption visible and truncates long names with a full-value title on narrow windows. Turning on without a default opens a compact dialog with a shared custom select and no preselected option. Confirm saves the user's choice before connecting; Cancel/Escape changes nothing. The dialog preserves failed choices for retry, rejects revision conflicts and prevents dismissal while saving. With no ready exits it offers a link back to Exit Nodes. Settings drafts block connection. A configured offline choice is retained, never silently replaced. No connection card appears above the list. The document scrolls with the list, never inside its card. A short list fits the viewport without a scrollbar; visually hidden live regions use a negative margin so their one-pixel boxes cannot overflow the document. Each two-line row shows a status dot (green when its node is reachable, amber when signed in without a usable node, hollow when signed out), the exit name and its node · Tailnet. The Default badge follows `rules.default_exit` in Split; the Global exit badge follows `rules.global_exit` in Global. Neither is fixed to `main`. Signed-out additional exits show an inline Sign in button instead of the chevron. Rows open that exit's picker.

The picker's device bar names the device and shows Sign in only when needed. There are no standalone switch-network or sign-out controls. The picker has no toolbar, search, manual entry or bulk Ping action; every node retains its own Ping action. Remove exit sits in a separate section at the very bottom for every saved profile, including `main` and the last exit. Its confirmation explains that removal disconnects, logs out only that client, removes its local profile and explicit route groups, and retains the remote Tailnet device record. Revision checks protect the operation; removed default/global selections become explicitly unset and require a new confirmed choice, and an explicit final-route reference to the removed exit resets to `direct`. The app stays stopped. Removing `main` also clears its MagicDNS suffixes without promoting another profile or changing remaining IDs/state paths. Removing the last exit preserves the remaining routing policy as a dormant draft. Add exit creates a new additional profile and, if no profiles remain, leaves both traffic exit selectors unset for explicit confirmation. Add opens authorization directly without a name dialog. Network choice occurs during browser authorization; confirming the first node binds the Tailnet guard and saves the label `<exit-device-name>@<network name>`. Before a node is chosen, an unnamed row shows the known network or New exit. Automatic labels follow later node choices without changing the profile ID; existing custom names are retained. Backdrop clicks and Escape cancel a staged node choice, except while a save is in progress; saving successfully closes the picker.

Settings › Routing rules groups the Split/Global segments and routing controls into one card. Its visible rows share equal grid tracks, at least 56px high, growing together when descriptions wrap. Builtin/custom rule-source help and validation messages sit below the card, rather than increasing a single row's height. Global shows one exit selector and runs only that device. Split shows an independent default proxy exit, the unmatched-traffic choice and rule groups. `rules.default_exit` carries proxy matches from builtin/custom rule sources, `proxy_domains`, `proxy_ips`, `final: proxy`, and bare Tailnet addresses without a more specific route. `rules.final` chooses direct, the default proxy exit, or a named additional exit for unmatched traffic. Split configuration remains saved while hidden in Global. Mode and exit target changes use the settings Save & apply action. Rule groups are bordered surfaces, each with an exit select, Domains/CIDR fields and Remove.

Settings use a form/preview grid (2fr/1fr, preview minimum 300px). Ordinary settings use equal-height rows within each card (56px minimum, 8px by 14px padding), growing together when notes wrap. Mixed cards group their ordinary rows separately from multiline lists. Labels and notes sit on the left; the compact control column is 160–220px, with toggles right-aligned. Exit selects show names without appended IP addresses. MTU uses a short numeric input with its unit outside the field so native steppers cannot overlap it; its range hint sits below the card. Multiline lists and the update panel remain content-sized. The exit picker is at most 640px wide with 16px viewport clearance and one dialog scrollbar when needed; its node card has no independent scroller. The picker's card list uses compact 34px minimum rows and keeps every item on a single line: a leading plain green checkmark only on the selected node, the node name, an 80px right-aligned latency column, and a dedicated Ping text button at the far right, with no border or background. IP addresses, server icons, separate status/path columns, and radio controls are omitted. Names truncate with native titles instead of wrapping, so both lists fit narrow windows without horizontal scrolling. The dedicated Ping button tests or retries independently; clicking the name or leading checkmark selects the node through the existing confirmation flow. Relay details and error reasons remain available in native titles and accessible descriptions. Confirmation stays inside the picker; mode changes use the settings actionbar. Node rows have no hover or selected background fill, including the latency and Ping columns. Pointer selection moves focus to the dialog heading so no blue box remains around the choice; keyboard navigation retains the visible focus outline.

- At (1199px) and below, hide the live YAML preview while retaining the full-width form and YAML editor tab.
- At (960px) and below, hide the header exit label.
- At (860px) and below, use a sidebar (184px; macOS 212px), gutters (20px), and a narrower settings control column (160–200px).
- At (640px) and below, use an icon sidebar (52px), gutters (16px), stacked field rows (toggles keep a label/control arrangement), wrapping actions, full-width mode segments, and one-column rule pairs. The Settings submenu is replaced by an in-page category picker, and the sidebar footer shows only its status dot. Navigation text remains available to assistive technology.
- At heights (680px) and below with width at least (641px), compact connection-page vertical spacing.
- Coarse pointers increase listed action targets, node rows and fields to at least (44px); inputs use text (16px). This is an input-mode adjustment rather than a new desktop density.

These responsive rules describe the stylesheet; their presence does not guarantee behavior across all window sizes, input modes, or native runtimes.

### Shared Dropdowns

`assets/select.js` upgrades every single-select field through `TailSplitSelect.sync()` after form renders. The native select remains hidden as the value/event model; a shared ARIA combobox and listbox provide the visible UI. Dynamic option lists, disabled fields, localization and existing input/change handlers stay connected. Document click/focus changes and Escape close the menu; arrows and Home/End move through enabled options, Enter selects, and Tab keeps normal focus order.

Fields use the existing field, line, ink and selection tokens. `data-select-variant="text"` gives Log level an unboxed text trigger with a caret. Menus use the app's surface, 12px radius, overlay shadow and a green plain check for the selected option. They are positioned within the viewport and scroll only when their option list requires it. No operating-system select popup is shown after upgrade.

## Elevation & Depth

Panels are flat, separated by tone and fine borders. The settings actionbar layers a translucent canvas and blur (12px). Shared dropdown menus use the surface fill and an overlay shadow. Toasts, dialogs, and switch thumbs use the small shadow vocabulary in the sidecar. Modal backdrops use explicit light/dark black scrims and blur (2px); the source avoids inherited custom properties there for Chromium 120 compatibility.

**The Flat Surface Rule.** Use tonal surfaces and hairline separators for ordinary panels; reserve ambient shadows for overlays and the small switch thumb.

Field border/shadow transitions last (.12s); switch background and thumb transitions last (.15s). Loading and update indicators rotate linearly (.8s and 1s respectively). Reduced-motion preference disables animations, transitions, and smooth scrolling.

## Shapes

Controls and navigation use modest rounded corners; grouped panels are gently rounder. The frontmatter records the reused radius steps. Mode segments use their tighter radius inside a rounded container. Pills, dots, and switch tracks remain fully rounded. Borders are ordinarily (1px). Titlebar window controls retain square edges. Functional interface icons use vendored Phosphor Icons 2.1.1 regular SVG paths, normally (16px); compact actions and window controls use (14px), and node selection marks use (16px). The paths encode their own regular stroke geometry and render with `fill: currentColor; stroke: none`. Side navigation, search, refresh, close, disclosure, select, loading, and custom window-control icons share this family. The TailSplit brand mark remains its own artwork. Source names are recorded on the inline symbols, with the upstream MIT notice in THIRD_PARTY_NOTICES.md.

## Components

### Buttons

Primary actions use blue fill and white text; secondary actions use raised neutral fill and a strong border; subtle actions use transparent fill. Normal buttons have a minimum height (28px), small buttons (26px). Hover changes fill; global focus is a visible accent outline (2px, offset 2px). Disabled buttons reduce opacity (.45). No transform-based hover elevation is present.

### Inputs / Fields

Fields use a strong border, field surface, control radius, and minimum height (28px). Focus changes the border and adds the accent outline. Placeholder text uses faint ink; disabled fields use opacity (.6). Multiline fields resize vertically.

### Navigation

Primary navigation items are 28px high with 15px icons, neutral hover and selected fills, medium active text, and a current-page attribute. Settings submenu items (26px) use muted text and the same selected fill; in YAML mode none is highlighted. Editor tabs and routing segments sit inside low-contrast rounded tracks; the selected segment is lifted with a light surface and a small shadow (a translucent white lift in dark appearance) rather than tinted as a primary action.

### Cards / Containers

Grouped settings and exit lists share a 16px panel radius, with internal row dividers. Settings rows use 8px by 14px padding. Dialogs use a 20px radius. Where supported, `corner-shape: superellipse(1.6)` smooths the transition from straight edges into corners; older WebKit keeps the same radius as a rounded fallback. YAML regions follow the app's code surface. Cards do not acquire shadows on hover.

### Diagnostic Terminal

The log viewport uses the user-selected terminal reference: a fixed deep gray
background (`log-background`) and light foreground (`log-ink`) in both app
appearances. Its scoped CSS properties override the code surface, primary and
secondary ink, muted ink, borders, selection and scrollbars. ANSI foreground
colors map to readable semantic colors; cyan and magenta identify informational
output and connection groups alongside the existing red, amber, green and blue
roles. Text labels preserve the meaning independently of color. Log content is
inserted as text, with unsupported terminal commands discarded.

The viewport fills the remaining workspace height below its heading and log-level
control, keeping the page gutter at the bottom. A visible settings actionbar or
notice consumes workspace height before the log viewport. Overflow scrolls inside
the viewport instead of growing the window; the other app pages retain their
existing scrolling behavior. While the Logs page is visible, the file tail refreshes
every four seconds. Initial display and updates follow the latest entry at the
bottom. Scrolling up retains the displayed snapshot and selection while background
reads cache the latest bounded tail. Returning to the bottom, refreshing, or
reopening Logs resumes live following. Read errors appear in a separate status
without replacing the last successful content or changing the following state.
Unchanged output keeps its existing DOM nodes to preserve reading and selection.

### Status and Exit Selection

Header connection state pairs a small colored dot (7px) with text. Each row of the Exit Nodes page's Exits card shows its exit's selected node and Tailnet and opens that exit's picker dialog; node rows never appear on the home page.

The picker uses the reference's **card list**: one rounded surface, internal hairline dividers, a leading checkmark and name, then right-aligned latency and a separate Ping button. Native buttons with pressed state expose the selection to assistive technology; no radio is rendered. Unselected rows reserve an empty icon slot; selected rows show the `good`-green plain Phosphor Check and medium-weight name, with no selected or hover row fill. Pointer selection leaves no blue focus box; keyboard selection retains visible focus. Its canvas is subtly distinct from the card surface. Unavailable exits remain visible with disabled selection. The modal footer identifies the staged choice and reconnect consequence; Cancel and Escape discard that choice. Settings drafts block applying and have their own warning and navigation action inside the dialog. Remove exit remains below the confirmation footer for every saved exit.

The fixed-width latency readout uses right-aligned tabular numbers; the adjacent explicit Ping button handles testing and retry. Values at or below 500ms use softened `latency-fast` green; values above 500ms through 1000ms use `latency-medium` amber; values above 1000ms use `latency-slow` red, with matching muted dark-theme tokens. These readouts use regular 400 weight. Failed pings retain the standard error color. Numeric labels preserve meaning without relying on color. Untested latency cells show a dash, pending cells say Testing, and unavailable cells show Offline or Not found. The Ping action remains a separate labeled button. Failure details and direct/relay paths are exposed in titles and accessible descriptions. Ping never changes selection, and results are kept per exit device. One polite live region announces the requested result. Preview latency is explicitly labeled simulated. Network or device-login changes invalidate remembered results. Routing mode changes are settings drafts and apply only when saved.

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
