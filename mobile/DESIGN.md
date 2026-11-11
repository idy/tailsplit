---
name: "TailSplit Native Mobile"
description: "The TailSplit desktop identity adapted to native phone controls and a single exit."
colors:
  tint: "#2f62d5"
  tint-dark: "#5b8cf5"
  tint-high-contrast: "#2456c2"
  tint-dark-high-contrast: "#8ab4ff"
  good: "#167536"
  good-dark: "#30d158"
  warn: "#855700"
  warn-dark: "#f0c46b"
  error: "#b42318"
  error-dark: "#ff8a80"
  soft-good: "#3a8154"
  soft-good-dark: "#7ab58b"
  log-background: "#272a32"
  log-ink: "#e5e5ea"
  log-muted: "#aeb4c0"
  log-cyan: "#70cbd6"
  log-magenta: "#dca0e6"
  log-warn: "#f0c46b"
  log-error: "#ff8a80"
  log-good: "#7ab58b"
typography:
  headline:
    fontFamily: "Montserrat"
    fontSize: "32px"
    fontWeight: 600
    lineHeight: 1.15
    letterSpacing: "-0.4px"
  title:
    fontSize: "17px"
    fontWeight: 600
  body:
    fontSize: "15px"
    lineHeight: 1.35
  label:
    fontSize: "13px"
    fontWeight: 400
    letterSpacing: "-0.08px"
  code:
    fontFamily: "Menlo"
    fontSize: "12px"
    lineHeight: 1.6
rounded:
  group: "24px"
  primary-button: "14px"
  terminal: "16px"
  reconnect-bar: "26px"
spacing:
  compact: "4px"
  control-gap: "8px"
  row-gap: "12px"
  inset: "16px"
  section: "24px"
  ios-gutter: "20px"
components:
  primary-button:
    backgroundColor: "{colors.tint}"
    rounded: "{rounded.primary-button}"
  primary-button-dark:
    backgroundColor: "{colors.tint-dark}"
  group-label:
    typography: "{typography.label}"
  node-reachability:
    textColor: "{colors.soft-good}"
  node-reachability-dark:
    textColor: "{colors.soft-good-dark}"
  node-check:
    textColor: "{colors.good}"
    size: "20px"
  node-check-dark:
    textColor: "{colors.good-dark}"
  terminal:
    backgroundColor: "{colors.log-background}"
    textColor: "{colors.log-ink}"
    typography: "{typography.code}"
    rounded: "{rounded.terminal}"
    padding: "12px 14px 14px"
---

# Design System: TailSplit Native Mobile

## Overview

**Creative North Star: "The Desktop Configuration Panel, Native on Phone"**

TailSplit retains the user-pinned desktop identity: neutral grouped surfaces, blue actions, text-backed semantic states, a plain green node check, and a fixed deep-gray diagnostic terminal. This mobile boundary implements one embedded Tailscale device with one selected exit node. Native phone controls carry that identity; the desktop web typography and geometry are not transplanted into touch controls.

The implementation uses native UIKit Liquid Glass through `adaptive_platform_ui` for supported iOS controls on iOS 26+, with Cupertino fallback. The page content, inset grouped lists and node sheet include Flutter Cupertino widgets. Material 3 implementations also exist in source; the Android debug build passed CI, while Android device layouts remain unverified. This record describes the phone adaptation, not a new identity for TailSplit or a desktop redesign.

**Key Characteristics:**

- System appearance and native semantic surfaces.
- Native tab, toolbar, switch, segmented and menu controls where implemented.
- System text for controls and content, Montserrat for large page titles.
- Explicit connection state and staged node selection.
- A diagnostic terminal with a constant dark palette.

Sources: `lib/src/theme/tokens.dart`, `theme.dart`, and `lib/src/ui/{native,common,root_shell,exit_page,node_picker,settings_page,logs_page}.dart`. `../PRODUCT.md` supplies product truth; `../DESIGN.md` supplies the desktop identity; `../.impeccable/surfaces/mobile-lib-main-dart.md` supplies direction. Source overrides the direction where they differ. The local sidecar contains extensions and illustrative HTML translations of native patterns; it is not a native rendering reference.

Evidence is limited to the supplied iPhone 18 Pro / iOS 27 simulator preview images under `../.impeccable/review/`: `phone-ios-light.png`, `phone-ios-dark.png`, `phone-ios-picker-dark.png`, `phone-ios-settings-dark.png`, and `phone-ios-logs-dark.png`. The first two capture light and dark appearance respectively; the remaining three show dark appearance. All display simulated-data labels; these are visual evidence, not verification of real account authorization or VPN traffic. The settled Settings recapture shows white time and status indicators on black; the coordinator identified the earlier missing-indicator finding as capture timing, with no code change. This record issues no ship verdict.

English and Simplified Chinese are implemented. Source allows system text scaling, but no Dynamic Type screenshot evidence, tablet evidence, accessibility conformance claim, or real account/VPN traffic verification is established here. This documentation pass inspects existing artifacts and does not claim a fresh simulator run.

## Colors

The desktop's action blue and semantic colors sit within native system neutrals. Frontmatter records explicit reused source values; appearance suffixes describe dynamic variants, not separate Dart constants. High-contrast tint values are implemented tokens, not evidence of a tested contrast configuration.

### Primary

- **Action blue:** `tint` identifies interactive controls. Its dynamic dark and contrast variants come from `TsColors.tint`.
- **Selection green:** `good` identifies the chosen node and usable/connected states, with readable accompanying text. `soft-good` is the quieter Online readout; it does not document a latency feature.
- **Attention amber and failure red:** `warn` and `error` distinguish pending/attention and failed states. iOS destructive rows use the platform's `systemRed`, rather than the custom error token.

### Neutral

- **Grouped canvas and grouped surface:** Cupertino system grouped backgrounds and `CupertinoListSection.insetGrouped` own iOS neutrals and separators. Do not freeze their resolved colors into brand primitives.
- **Primary, secondary and disabled text:** native `label`, `secondaryLabel` and `tertiaryLabel` roles resolve in context. Native widgets supply their remaining defaults.
- **Diagnostic terminal:** `log-background`, `log-ink` and `log-muted` remain constant across appearances. Cyan identifies core sources; magenta identifies other app sources. Green, amber and red support readable severity labels.

**The Native Appearance Rule.** Resolve semantic platform surfaces and labels in context; reserve TailSplit's explicit colors for actions, state and the terminal.

**The State Rule.** Keep reachability, saved node choice and VPN connection distinct. A green reachable-node dot is not evidence that the VPN is connected.

Material source uses `ColorScheme.fromSeed` with a static TailSplit-blue seed and `tonalSpot`. Only light appearance overrides `primary` to the exact tint and `onPrimary` to white; dark Material primary remains generated. Its palette has no Android runtime evidence.

## Typography

**Display Font:** Montserrat, with platform fallback for unsupported glyphs, including Chinese.

**Body Font:** native system text on iOS through Cupertino/native controls. Do not inherit the desktop Montserrat/Monda/Lato role mapping.

**Label/Mono Font:** system labels; Menlo for iOS logs (`monospace` in the Android branch).

### Hierarchy

- **Headline:** frontmatter `headline` is the actual iOS large title passed by `NativePage`; the helper's unused default size is not the page title token. Its weight includes the variable-font `wght` setting. Android source passes a 30px large title instead.
- **Title:** explicit empty-state headings use the frontmatter `title` role. Connection text is semibold, saved exit names medium, and a staged selected node name semibold; ordinary list row sizing otherwise comes from native widgets.
- **Body:** the frontmatter body role is explanatory empty-state text. Reachability and reconnect messages also use 15px; inherited list text retains platform defaults.
- **Label:** inset-group section headers use the frontmatter label role in secondary text. Footers and identity notes also use 13px; picker identity notes explicitly use line height 1.4. These headers are native grouped-list labels, not decorative eyebrows.
- **Code:** the terminal uses the frontmatter code role, wrapping selectable text. Severity labels use weight 600; timestamps use muted ink.

**The Native Text Rule.** Use the system face for controls, sheet navigation and content; reserve Montserrat for large page titles. An inline toolbar title is native text. Preserve scaling supplied by the text widgets without claiming tested large-text layouts.

Numbers here are Flutter logical units at the base text scale, represented as `px` for portable token tooling; they are not screenshot device pixels. Native component metrics and inherited type sizes remain platform-owned.

## Layout

The app has three tabs: Exit Node, Logs and Settings. Each retains its own navigator in an `IndexedStack`; inactive tabs disable animation tickers through `TickerMode`. Selecting the active tab returns its stack to its first route. This does not establish that every background operation stops.

Top-level iOS pages use a scrolling large title, with the inline toolbar title enabled after a source scroll offset of 36 logical units. Content begins below the top safe inset plus 56; page title padding is 20 horizontal, 4 top and 6 bottom. Inset-grouped sections use directional margins of 20 horizontal and 10 bottom; native list sections own internal spacing and row metrics. Page scroll content reserves bottom safe inset plus 96 for the tab region. The shared source spacing constants are available in frontmatter; actual native grouping is not a uniform 24-unit grid.

The first tab separates the connection/routing group from the one saved exit or Add exit group. A device identity footer remains subordinate. The picker uses a Cupertino sheet and its supplied scroll controller, with one list for nodes, identity and removal. It does not introduce a nested node-list scroller. While the sheet is presented, `NativeChrome.sheetPresented` removes underlying page toolbars so native actions cannot float over the sheet's navigation.

Logs use a 16-unit horizontal gutter and the terminal's internal padding. Settings use grouped sections and pushed editors rather than the desktop's sidebar, form/preview columns or YAML workspace. Reconnect guidance is pinned above the tab region, with a horizontal inset of 16 and bottom position at safe inset plus 64.

No tablet arrangement or phone breakpoint scale has been demonstrated. Material branch geometry is source-only: inset groups use 16-unit horizontal padding, 24-unit corners and platform list tiles; the modal picker occupies 92% of available height. Do not treat these branches as device-tested layouts.

## Elevation & Depth

Ordinary content uses native grouped tonal layering and fine native separators. Liquid Glass belongs to native navigation and supported controls, not decorative translucent cards everywhere. UIKit toolbar, tab bar, switch and menus are reached through `adaptive_platform_ui`; the sheet navigation and content use Cupertino widgets.

`GlassBar` uses `AdaptiveBlurView` with `systemMaterial` on iOS 26+. Its older-iOS fallback clips a backdrop blur (sigma 20 in each axis) over secondary grouped background at alpha .78. This is a source-defined fallback, not an inspected older-iOS runtime. The Android branch uses inverse-surface Material at elevation 3. There are no custom offset shadows on ordinary phone groups.

**The Native Material Rule.** Keep translucency in navigation and the reconnect bar; use native grouped surfaces for ordinary content.

Transitions and activity indicators are platform widgets. No bespoke motion scale is established. Inactive tab tickers are disabled; that fact alone does not establish complete Reduce Motion behavior.

## Shapes

Card-list groups use the shared 24-unit `TsSpace.groupRadius` on both platforms. iOS clips the entire group, including native row press feedback, with continuous corners; Android uses the same radius on its Material surface. Switches, menus and sheets retain platform geometry. The terminal keeps its separate 16-unit radius.

Every card-list row has a plain 21-unit platform icon within its 22-unit leading slot. Connection and exit icons carry their semantic state colours. Picker rows show a device icon when unselected and a green check when selected. iOS 26 toolbar/tab symbols use SF Symbol identifiers; fallback widgets use Cupertino icons, with Material icons in the Android branch. These are functional platform icon APIs, not Unicode glyph icons.

## Components

### Buttons

Add exit uses a full-width `CupertinoButton.filled`, a dynamic tint, semibold text and the primary-button radius. Busy state replaces text with a Cupertino activity indicator and disables activation. This action uses Flutter Cupertino rendering; do not prescribe native UIKit UIButton rendering for it. The Material branch uses a filled button with a 48-unit minimum height.

Small iOS `TextAction` buttons use 12-unit horizontal padding and a 44-by-44 minimum size. Sheet Cancel/Confirm are Cupertino navigation actions; Confirm is enabled only for a changed, online choice when not saving. Native menu and toolbar state handling remains defined by their adapters.

### Cards / Containers

Inset-grouped sections are native-feeling, tonal and separated internally by hairlines. They contain list rows rather than desktop bordered cards. Group headers and footers use secondary labels; body content remains primary. Problem cards explain failures in place with a recovery action. Preview builds use the same layout without an in-app preview banner; review captions disclose simulated data.

### Inputs / Fields

Settings use trailing native menus, switches and disclosure rows. Editable values open native-style text prompts or pushed list editors. Retain the platform field, focus and validation behavior; there is no extracted custom field shadow or border scale to transfer from desktop. Rules/DNS/network/general settings remain grouped by purpose.

### Navigation

The native tab bar orders Exit Node, Logs, Settings. The selected iOS tab uses dynamic action tint. Page actions use native Liquid Glass toolbars where supported; sheet navigation temporarily takes precedence through `NativeChrome`. Each retained tab disables tickers when inactive. Native fallback controls are part of the system, with no older-iOS screenshot verification implied.

### Connection and Routing

Connection state pairs readable text with a neutral, green, amber or red power icon and an explicit switch. Routing uses an unboxed current-value menu. In Settings, the adaptive Rule-based / Global segmented control sits independently above the routing card list, which starts with Rule source. Global mode hides the Split-only card and shows its explanation below the standalone control. Signing in, selecting a node and connecting remain separate steps. The app has a single exit; desktop multi-exit controls are outside this mobile implementation.

Ordinary settings save immediately and may expose Reconnect to apply while connected. Routing mode and node changes reconnect a known Split session after confirmed shutdown. A running Global session, or a restored session whose runtime mode is unknown, retains its tunnel and marks the change pending. Reconnect and reauthentication require confirmation explaining that stopping the VPN restores normal networking and traffic may go direct until connection succeeds, including on failure. Cancelling the prompt retains the tunnel. A shutdown failure keeps the identity and exposes a retryable problem instead of removing the exit or starting a replacement. Global is a routing mode, not a system kill switch: its no-direct-fallback policy only applies while the VPN tunnel exists. Both routing surfaces explain that a system stop or service failure may restore ordinary networking. An unexpected loss of an observed active tunnel shows a localized warning and Retry; the warning cannot block traffic, and process death may prevent it from appearing immediately. The pinned plugin does not support Android Always-on/lockdown or configure equivalent iOS protection. These UI behaviors do not verify real VPN traffic protection.

While a connection is preparing, the group footer names the step (checking the exit node, loading the rule list, starting the VPN) and says the switch cancels; the switch stays enabled for that purpose. The preflight refreshes a netmap older than a minute and refuses to connect through a node Tailscale reports offline or no longer offers, or while the device awaits admin approval. It never substitutes another node.

### Problems and Notices

One problem card at a time, at the top of Exit Node (Settings shows only rule-list and storage problems). Its title names the failure, its body explains the cause in the user's language (offline, timeout, TLS, rate limit, server response, refusal, invalid list), and one text action under the body offers the recovery: Retry, Sign in, Choose node, Refresh or Open sign-in page. Raw English detail goes to Logs. Awaiting approval uses the amber clock; failures use the red exclamation. A 44-point dismiss button closes the card; the card is a live region.

Short confirmations (Copied 12 lines, Removed example.com · Undo, Rule list updated) are notices: a Material snackbar on Android, and on iOS a small grouped-surface capsule above the tab bar with a soft offset shadow, announced to screen readers and shown without a fade when animations are reduced.

### Rule List

The rule list downloads on first use and is reused from its per-source saved copy. Its single-line row shows the download time beside Update; that timestamp does not describe the independently configured proxy/direct domain and IP entries. A failed update keeps the saved copy and shows an explicit rule-list error below the group.

### Accessibility Text Sizes

Card-list rows stay on one line at every text size. Their title, value and control share a baseline with a required leading icon; long text truncates visually while text semantics retain the full value. There are no row subtitles or stacked row controls. Explanations and connection progress wrap in the section footer below the card. Rule-list download status stays beside its Update action on the same row. Large page titles grow with text size up to 1.5×.

### Node Picker

Selection is a plain green check with a semibold name and no persistent selected-row fill. Unselected rows show a device icon in the same leading slot. Native press feedback remains platform behavior. The trailing column shows Online, Offline or last seen, with tabular numerals; it is not a Ping button or measured latency. Only online nodes can be newly selected; offline rows remain visible and disabled. A saved offline choice is retained rather than silently replaced.

A tap stages a node. Confirm saves the changed online choice; Cancel and sheet dismissal discard it. The source blocks popping while saving. Account/device identity follows the list; Remove exit is a separate destructive section below it and opens confirmation. No Ping action is implemented because the app has no Tailscale data plane outside the VPN extension.

### Diagnostic Terminal

Logs preserve the desktop's constant dark palette with native Menlo text, newest entries ordered last, wrapped selectable lines and readable severity labels. Filter, copy and clear live in the page toolbar. The log source uses slivers in the page scroll; do not import the desktop's independent terminal scroller or four-second file-tail behavior.

## Do's and Don'ts

### Do:

- Do preserve the desktop's blue, semantic state and diagnostic-terminal identity through native roles.
- Do use native semantic surfaces and system content text in both appearances.
- Do distinguish connection state, reachability and a staged node choice with readable labels.
- Do give every card-list row a platform icon, keep rows single-line and put supporting text below the group. Keep the green selected-node check and explicit confirmation.
- Do preserve English/Chinese labels and source text scaling without claiming unverified accessibility conformance.
- Do hide underlying native page toolbars while the picker sheet owns navigation, and disable tickers in inactive tabs.

### Don't:

- Don't introduce the desktop multi-exit list or a giant VPN power-button hero into this single-exit native adaptation.
- Don't add persistent selected-node row fill, fabricated latency, or a Ping action unsupported by the mobile data plane.
- Don't equate simulated connected logs with verified account authorization or real VPN traffic.
- Don't infer Android, tablet, large-text or older-iOS runtime verification from source branches.

The source includes text scaling and platform transitions, but no accessibility conformance is claimed. The node picker shows reachability, not measured latency. The earlier Settings status-indicator concern was resolved by a settled recapture. Desktop design artifacts and their pre-existing sidecar drift remain outside this mobile documentation boundary.
