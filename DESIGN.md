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
    fontFamily: "Montserrat, \"PingFang SC\", \"Microsoft YaHei UI\", \"Microsoft YaHei\", system-ui, sans-serif"
    fontSize: "1.25rem"
    fontWeight: 600
    lineHeight: 1.3
    letterSpacing: "-.01em"
  title:
    fontFamily: "Monda, \"PingFang SC\", \"Microsoft YaHei UI\", \"Microsoft YaHei\", system-ui, sans-serif"
    fontSize: "0.9375rem"
    fontWeight: 600
    lineHeight: 1.4
  body:
    fontFamily: "Lato, \"PingFang SC\", \"Microsoft YaHei UI\", \"Microsoft YaHei\", system-ui, sans-serif"
    fontSize: "0.8125rem"
    fontWeight: 400
    lineHeight: 1.5
  label:
    fontFamily: "Monda, \"PingFang SC\", \"Microsoft YaHei UI\", \"Microsoft YaHei\", system-ui, sans-serif"
    fontSize: "0.8125rem"
    fontWeight: 500
    lineHeight: 1.4
  note:
    fontFamily: "Lato, \"PingFang SC\", \"Microsoft YaHei UI\", \"Microsoft YaHei\", system-ui, sans-serif"
    fontSize: "0.75rem"
    fontWeight: 400
    lineHeight: 1.45
  button:
    fontFamily: "Montserrat, \"PingFang SC\", \"Microsoft YaHei UI\", \"Microsoft YaHei\", system-ui, sans-serif"
    fontSize: "0.8125rem"
    fontWeight: 500
    lineHeight: 1.4
  caption:
    fontFamily: "Lato, \"PingFang SC\", \"Microsoft YaHei UI\", \"Microsoft YaHei\", system-ui, sans-serif"
    fontSize: "0.6875rem"
    fontWeight: 400
    lineHeight: 1.5
  brand:
    fontFamily: "Montserrat, \"PingFang SC\", \"Microsoft YaHei UI\", \"Microsoft YaHei\", system-ui, sans-serif"
    fontSize: "0.875rem"
    fontWeight: 600
    lineHeight: 1.25
  dialog-title:
    fontFamily: "Montserrat, \"PingFang SC\", \"Microsoft YaHei UI\", \"Microsoft YaHei\", system-ui, sans-serif"
    fontSize: "1rem"
    fontWeight: 600
    lineHeight: 1.4
  touch-input:
    fontFamily: "Lato, \"PingFang SC\", \"Microsoft YaHei UI\", \"Microsoft YaHei\", system-ui, sans-serif"
    fontSize: "16px"
    fontWeight: 400
    lineHeight: 1.4
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

TailSplit uses a compact, neutral desktop configuration panel with the website's Montserrat/Monda/Lato family roles, magpie-like information density, blue actions, and semantic connection states. This system applies to the embedded configuration UI in macOS WKWebView and Windows WebView2. It preserves the incumbent desktop world selected by the user.

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

The panel follows the user's website-family mapping: Montserrat for the wordmark, prominent headings and button labels; Monda for compact page/section titles and field labels; Lato for body copy, values, notes and metadata. PingFang SC and Microsoft YaHei UI provide Chinese fallback. At the root font size (100%), body, labels, navigation and buttons remain 13px; notes, hints, latency and small buttons 12px; captions and the sidebar footer 11px; the wordmark 14px; section titles 15px; and dialog titles 16px. Family and weight distinguish the roles without changing the established desktop density.

- **Titlebar path:** the titlebar names the page as a muted parent, a faint slash and the current page (for example "Settings / Network"; top-level pages show only their name). Visible in-page h2 headings are removed and remain for assistive technology; the page's one-line description leads the content instead.
- **Ping:** a text-only 12px action in `accent-ink`, underlined on hover, with muted disabled text.
- **Latency:** 12px regular with tabular numbers and softened semantic colors.
- **Code:** the existing mono stack at 12px for YAML, diagnostic logs, filenames and code-like inputs. YAML uses line height 1.65 and logs 1.7.

All font files are bundled locally under `internal/configweb/assets/fonts/` and are byte-identical to the website assets in `site/shared/`. Montserrat supports weights 400–800 and Monda 400–700; Lato uses separate regular 400 and bold 700 faces. The three first-viewport families are preloaded, with Lato bold loaded on demand and `font-display: swap` retaining visible fallback text. No external font service is used. Compound exit buttons keep their Monda name and Lato description, while dropdown triggers retain Montserrat despite their font shorthand.

**The Desktop Type Rule.** Use the approved website families by role at the documented compact sizes; keep the mono stack for configuration and diagnostics. The latest user direction restores the website-family pairing while retaining native chrome, layout, zoomable rem sizing and Chinese fallback.

## Layout

The experimental MyGo host uses the same embedded page and platform chrome selectors. Its hidden native titlebar preserves macOS traffic lights and the existing Windows HTML caption buttons; MyGo-only CSS drag regions exclude interactive controls. It introduces no replacement icon, type or spacing system.

Keyboard focus on a saved exit outlines the complete rounded row, including its trailing chevron and space. The inner trigger has no separate square outline; pointer interactions do not add a row fill.

A persistent sidebar and flexible workspace share a sticky titlebar. The default sidebar is (200px) and titlebar (44px); macOS reserves traffic-light space with a wider sidebar (216px), shorter titlebar (40px), and brand left inset (88px). Windows controls fill the titlebar height with square-edged buttons (44px wide). The brand link keeps its pointer home action but is excluded from sequential Tab navigation and has no focus outline; the Home navigation button supplies the keyboard home action. Action controls and exit rows retain their visible keyboard focus. The source comment explicitly identifies this fixed-height native chrome; its flush control edges do not establish a general content-spacing rule.

Home is the default destination. With no saved exits and a policy needing an exit, a primary Set up button replaces the connection switch. It starts the shared guided setup flow and becomes Retry setup after an incomplete attempt. During setup the cancel control remains available; once an exit is configured, the normal switch returns. Starting setup or connecting uses the existing busy, revision, draft, valid-config and desktop-capability guards. Cancel remains available while another action is busy or state polling has failed; only an in-flight cancellation disables duplicate clicks. Four compact steps show when setup is needed. Below it, a GizMoth-style topology places the computer at the left, cubic fan-out wires in the middle and one 44px row per used destination at the right. Split includes Direct and each exit referenced by saved policy; Global shows only its selected exit. Hollow endpoints and dashed wires indicate planned routes; confirmed routes use solid green wires and filled endpoints. Row hover and keyboard focus highlight the corresponding wire. The first line names the node. The second line starts at the same left edge with upload, download and smaller muted policy detail, separated by 8px gaps. Each arrow sits 4px from its rate, and adjacent destination rows have a 4px gap. The row takes its intrinsic width instead of stretching to the page edge. Rates use tabular numerals and ordinary secondary ink. Narrow layouts retain this two-line structure and truncate long labels with their full text available in titles. Native Connect intents are consumed by the native MyGo panel, or by the specific browser fallback tagged with that request in its fragment, and claimed once at the controller boundary; ordinary browser panels cannot drain them. Showing the native window explicitly wakes its retained page. An asynchronously failed visible window opens the same designated browser fallback, deduplicated with direct Show failures; an unrequested background failure opens nothing. Unclaimed fallback markers are scoped to the panel capability in tab-session storage until claim or confirmed cancellation, preserving intent across reloads while busy. Browser panels share pending cancellations in origin storage, and acknowledged request tombstones prevent late claim responses from restarting setup after the originating panel closes. Native WebView storage is isolated, so its cancellation uses the registered main-page MyGo IPC binding to reach the shared app controller directly, independently of the HTTP channel. Closing or losing that renderer after IPC delivery does not discard host revocation. The host also records which pending Connect request was offered before native interaction; native state reads cover a visible page polling before its Show wakeup. If neither IPC nor HTTP cancellation arrives, a designated browser fallback retires that unconfirmed offered request instead of replaying it, and explains the explicit turn-on/cancel recovery. Cold failures before request delivery and fresh explicit Connect requests after the window has failed can still continue in the browser. One operation ID is assigned before dispatch and retained through preparation, login, saving and connection; controller cancellation and retries target that owner or the admitted owner bound to the canceled native request, leaving unrelated later connections intact. Disconnected quick saves receive a fresh operation identity instead of reusing a canceled connection owner. Until acknowledgment, the banner shows Canceling connection with a retry action. Native browser-login workers capture intent and helper-owner identities on the UI loop, then revalidate both identities and the current authorization URL under the controller cancellation lock at the actual launcher process Start boundary. Process completion and browser timeouts run outside that lock. Cancellation winning first prevents a launcher from starting; a dispatch committed first may complete afterward. Pending device approval never relaunches sign-in. If no helper is monitoring, Check approval performs one bounded read-only refresh; polling does not start repeated checks. Confirming a recovered, unchanged node can continue setup without a redundant configuration write. The map compares the saved document with the immutable running revision; externally edited routes remain planned until applied. Offline secondary nodes use unavailable styling even while the default stays connected, and implicit private-domain owners appear in the topology. Active owner IDs come from the immutable compiled session, including preflight-discovered metadata and blocked namespace reservations; later SDK suffix changes cannot reassign ownership. Saved-only previews normalize wildcard prefixes, dots, case and IDNA like the compiler. Rates update without replacing focused rows, and use successive byte samples from the same engine session. Missing, stale, reset and disconnected samples show an em dash; a measured idle interval may show zero. Directional pulses appear only on a confirmed route with positive measured upload or download: outward for upload, inward for download. Idle, unavailable, reset, blocked and expired samples stop the pulses. Motion pauses away from Home or when the document is hidden; reduced motion retains static activity marks. The topology has no visible footer, unmatched-traffic summary or explanatory paragraph. Screen readers retain the route-state description, and interface previews identify themselves in the header preview badge.

Workspace gutters, field gaps, and section gaps use the frontmatter spacing tokens. Pages have vertical padding (20px). Exit Nodes, Logs, Settings (including all form categories and the YAML editor), notices, and the settings actionbar share a centered content column capped at 1040px. Narrower windows use the available width between the page gutters. The actionbar background and divider span the full workspace; its controls align with the content column. The titlebar remains full-width. Only the workspace body below it scrolls; the save bar stays at the bottom. A stable scrollbar gutter keeps short and long pages aligned. Exit Nodes and Logs occupy the upper sidebar; Settings is pinned below by the flexible sidebar layout. While Settings is the current page it expands in place into a second-level submenu of its categories, indented under a 1px guide line aligned with the Settings icon; there is no in-page category tab bar. The Form/YAML segmented control sits to the left of the shared titlebar actions while Settings is open. Exit Nodes, Settings and Logs keep the same right-aligned order: connection status, routing mode, connection switch, the update action (when visible), and an icon-only refresh action at the far right. Check, download and install states share that update slot immediately to the left of Refresh, with equal visible gaps to the connection switch and the Refresh glyph; account for the glyph's inset inside its 32px button. At compact desktop widths, the sidebar already identifies the page, so the breadcrumb yields space to the actions; connection status keeps its visible dot and accessible text. That final slot refreshes exits, reloads configuration with draft confirmation, or refreshes logs according to the current page; no extra exit-name label shifts the shared controls. Logs has no duplicate in-page refresh button. A sidebar footer shows the saved local proxy listener with a dot that turns green while connected; its top divider follows the visible settings actionbar's measured height so both full-width dividers align.

Exit Nodes opens directly to the **Exits** card: the optional legacy `tailscale` profile (`main`) first when present, followed by additional exits. A toolbar above the card places Add exit at the left and a compact shared text dropdown showing the saved Split default proxy exit or Global exit, or Not set, at the right. Clicking opens its menu directly, with no edit dialog. Choosing a gateway saves immediately; an idle app stays disconnected, while active traffic reconnects. Keeping the same selection does not write or reconnect. Drafts and revision conflicts block changes; a failed save restores the saved selection. There is no saved-exit count. The titlebar orders connection status, an unboxed routing-mode text dropdown, the master connection switch, and Refresh at the far right, with no default-exit summary. The shared dropdown offers Rule-based / Global (Kill-Switch), or 规则分流 / 全局（Kill-Switch） in Chinese. Peer discovery during connection preflight remains in a checking state until ready, rather than prematurely prompting for a new exit. The routing shortcut has no separate protection toggle or extra confirmation dialog. It edits the existing Global/TUN mode, saves immediately and reconnects an active session; turning it off returns to Split while retaining policy. Drafts, busy state and an unconfigured target gateway during active traffic disable the shortcut. Its tooltip explains that manual disconnect restores system networking. The list's default display keeps its caption visible and truncates long names with a full-value title on narrow windows. Turning on with no saved devices starts guided Add exit and sign-in, then opens the node picker. Its Save & connect action saves the confirmed node and this mode's default exit together before continuing the requested connection. A saved device missing login or a usable node enters that step. Multiple saved devices without a default use the shared custom select with no preselected option; unready choices continue into setup rather than remaining disabled. A temporary status notice explains installation, sign-in, node selection and errors, with Cancel and explicit Retry. Cancel/Escape drops the pending connection; failed saves and unrelated configuration revisions never start traffic. No provisional device row appears while authorization is pending. A guided empty picker offers Reload nodes when the device is signed in but its peer list has not arrived. Settings drafts block connection. A configured offline choice is retained, never silently replaced. No connection card appears above the list. The document scrolls with the list, never inside its card. A short list fits the viewport without a scrollbar; visually hidden live regions use a negative margin so their one-pixel boxes cannot overflow the document. Each two-line row shows a status dot (green when its node is reachable, amber when signed in without a usable node, hollow when signed out), the exit name and its node · Tailnet. The Default badge follows `rules.default_exit` in Split; the Global exit badge follows `rules.global_exit` in Global. Neither is fixed to `main`. Signed-out additional exits show an inline Sign in button instead of the chevron. Rows open that exit's picker.

The picker's device bar names the device and shows Sign in only when needed. There are no standalone switch-network or sign-out controls. The picker has no toolbar, search, manual entry or bulk Ping action; every node retains its own Ping action. Remove exit sits in a separate section at the very bottom for every saved profile, including `main` and the last exit. Its confirmation explains that removal disconnects and restores system networking, logs out only that client, removes its local profile, and retains both its explicit rule references and the remote Tailnet device record. Revision checks protect the operation; removed default/global selections become explicitly unset and require a new confirmed choice. Explicit final and route-group references remain as dormant policy and block Split reconnection until their missing targets are replaced or edited. The app stays stopped. Removing `main` retains its private DNS ownership and former custom domains as unresolved policy, without promoting another profile or changing remaining IDs/state paths. Split remains blocked until those references are explicitly resolved. Removing the last exit preserves the remaining routing policy as a dormant draft. Add exit creates a new additional profile and, if no profiles remain, leaves both traffic exit selectors unset for explicit confirmation. Add requires desktop action capability and a valid saved document. In active Global mode it first requests consent to disconnect protection and restore system networking; Cancel/Escape preserves the session and configuration, and a later save or sign-in failure leaves the app disconnected until manual reconnection. Otherwise Add opens authorization directly without a name dialog. Authorization uses a private staged configuration. No provisional profile or row is saved or shown while waiting; only a terminal authenticated result followed by helper cleanup commits the profile. Cancellation, failure or a configuration revision conflict leaves no new row. Add remains retryable, and success is announced only once after authentication. Network choice occurs during browser authorization; confirming the first node binds the Tailnet guard and saves the label `<exit-device-name>@<network name>`. Before a node is chosen, an unnamed row shows the known network or New exit. Automatic labels follow later node choices without changing the profile ID; existing custom names are retained. Backdrop clicks and Escape cancel a staged node choice, except while a save is in progress; saving successfully closes the picker.

Settings › Routing rules groups the Split/Global segments and routing controls into one card. Its visible rows share equal grid tracks, at least 56px high, growing together when descriptions wrap. Builtin/custom rule-source help and validation messages sit below the card, rather than increasing a single row's height. Global shows one exit selector and runs only that device. Split shows an independent default proxy exit, the unmatched-traffic choice and rule groups. `rules.default_exit` carries proxy matches from builtin/custom rule sources, `proxy_domains`, `proxy_ips`, `final: proxy`, and bare Tailnet addresses without a more specific route. `rules.final` chooses direct, the default proxy exit, or any configured exit, including `main`, for unmatched traffic. Split configuration remains saved while hidden in Global. Mode and exit target changes use the settings Save & apply action. Rule groups are bordered surfaces, each with an exit select, Domains/CIDR fields and Remove.

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

### Page Companion

The configuration panel includes the vendored Peetty Miru companion at 88 CSS
pixels, with lively local walking and jumping between safe visible edges. Its
shadow-root overlay leaves the page click-through outside the figure and menu;
the titlebar is explicitly excluded, and native modal dialogs remain above it.
Dragging, the hover/context menu and Peetty's keyboard actions are retained.
Chat is disabled, and all runtime assets are embedded and served locally.

Settings › General includes an immediate Page companion switch, independent of
network configuration and settings drafts. The menu's Hide action updates that
switch too. Visibility is saved in the app's persistent UI preferences, preserving
language and network configuration across ports, restarts and updates. Mounting
waits for that preference to load; failed saves restore the saved choice and show
a localized error with an explicit Retry action. Preference reads also retry when
the panel becomes visible. Hiding the document or leaving the page destroys the companion
and its listeners; returning restores it if enabled. Reduced motion uses immediate
placement at the nearest safe spot instead of falling, walking or jumping,
including input-focus avoidance, drag release and live preference changes.
The explicit move action immediately chooses another safe surface. Companion controls follow the panel's
English/Chinese language in place. Long log text is measured as one conservative
clipped obstacle, retaining safe landing spots without enumerating every line.

The build extracts the upstream shadow-root CSS into a same-origin stylesheet;
the panel keeps its existing restrictive Content Security Policy. Provenance and
regeneration instructions are in the vendored README and third-party notices.

### Network Service Installation

An explicit macOS Add exit, Sign in or Connect checks service readiness before
opening a device session. When the bundled service needs installation or an
update, the panel says Waiting for network service installation and asks the
user to complete the system Installer. It continues the original action after
the exact app build is ready, and cancellation never proceeds with sign-in.
Background discovery, Ping and logout do not open Installer. This state is
separate from Tailscale browser authorization and Tailnet device approval.

### Device Approval

The Tailscale `NeedsMachineAuth` state is presented as Awaiting device approval
for the primary and focused additional devices, with an instruction to ask the
Tailnet administrator to approve the device in the Tailscale admin console. Picker
empty states and saved-exit rows use the same distinction. Repeated browser sign-in
is disabled while approval is pending. The existing bounded readiness wait remains;
a timeout identifies the affected device and required administrator action.

### Configuration Recovery

An invalid saved configuration keeps its original diagnostic visible and offers
Edit YAML and Back up & rebuild in the same banner. When removing the obsolete
`dns.listen` field produces a strictly valid document, a primary Repair
configuration action appears first. Its confirmation explains the specific field
removal, source backup, preserved exits/rules/settings, discarded unsaved edits
and explicit disconnect including Global protection. Repair never guesses unknown
fields or repairs broken syntax, and a successful repair retains the user's
configuration instead of restoring desktop defaults. The success notice identifies
the removed field and backup path. The recovery dialog explains
that it backs up the original file in the same folder, restores desktop defaults,
discards drafts and resets saved exits and routing rules while retaining device
login data. Cancel or Escape leaves the file and connection untouched. Confirmed
recovery disconnects first, including Global protection, then uses the shared
operation gate and a configuration revision check. It stays disconnected and
returns to the empty Exit Nodes page with the backup path in a persistent success
notice. Failed operations retain the original source and a retryable error;
external revision conflicts offer Reload. The action is absent for valid saved
configuration, including invalid unsaved drafts of an otherwise valid file.
Banner actions wrap at compact widths and use existing button, dialog and
semantic color treatments in English/Chinese and both appearances.

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
