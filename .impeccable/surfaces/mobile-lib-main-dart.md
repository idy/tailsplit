---
version: 1
slug: "mobile-lib-main-dart"
primary_target: "mobile/lib/main.dart"
related_targets: ["mobile/lib/src/ui","mobile/lib/src/theme"]
---

# Mobile connection app (iOS and Android)

Visitor mode: Operate. Technically comfortable phone users sign in one embedded Tailscale device, choose its exit node, choose Rule-based or Global routing, and explicitly connect. The phone app supports exactly one exit; sing-box runs through `sing_box_flutter` as the system VPN, and Tailscale sign-in and exit discovery are implemented in the app.

## Direction contract

THESIS: The desktop connection center, translated into each platform's own controls rather than ported as a web page. One exit, one switch, one routing choice: the first tab answers "am I connected, and through which node?" in a single inset-grouped card, and the node picker is a sheet. It refuses the VPN-category hero (giant power button, globe, speed dial) and the multi-exit desktop list.

OWN-WORLD: Desktop TailSplit tokens carried into native roles: action blue #2f62d5 as the one tint, desktop green/amber/red as text-backed states, neutral grouped surfaces that follow system appearance, the plain green check for the chosen node, softened latency colors, and the fixed deep-gray terminal for Logs. iOS 26+ uses native Liquid Glass (UITabBar, toolbar, UISwitch, UISegmentedControl, UIMenu pull-downs) with Cupertino fallback; Android uses Material 3 navigation bar, switches, segmented buttons and bottom sheets with a static TailSplit-blue scheme. System fonts with Dynamic Type carry all UI text; Montserrat appears only in the wordmark and top-level titles.

STORY: Add exit opens Tailscale sign-in in an in-app browser; on authorization the picker opens with the tailnet's exit nodes; confirming names the exit `<node>@<network>`; the switch connects. Settings changes save immediately; while connected a glass bar offers Reconnect to apply. Remove exit signs out the device and leaves the app stopped.

FIRST VIEWPORT: Top-level title "Exit Node" in Montserrat over the system background. Card-list groups have 24-unit continuous corners, single-line rows and a platform icon before every label. Card one: connection row (power icon, Connected/Disconnected/Connecting text, native switch), then Routing (branch icon and trailing Rule-based/Global pull-down). Connection steps and supporting text sit below the group. Card two: the exit row (globe icon, `<node>@<network>`, chevron), or an Add exit empty state. Device identity and unavailable-node details stay in the group footer. Settings places the Rule-based / Global control independently above the Card List; the list begins with Rule source and hides in Global mode without an empty card. Settings and node-picker rows use the same single-line icon layout; the selected node keeps its green check and other nodes show device icons. Native glass tab bar: Exit Node, Logs, Settings.

FORM: Native grouped-list connection screen, inheriting the desktop world (seed:413a3804ca48439d remains the desktop's reference); no concept roll was made because the user pinned the world (desktop) and the material (native controls and Liquid Glass). seed: none (unavailable)

FINISH: unreviewed and undocumented is unfinished; this build ends with the finish review, the verdict, DESIGN.md, and every shipping raster carrying its provenance

## Boundaries and states

Signed out, signing in (browser open), authorized without a node, node offline, ready, connecting, connected, disconnecting, error, and VPN-permission-denied stay distinct. Only online exit nodes can be chosen; a saved offline node is retained, never silently replaced. Ping is not offered: the app has no Tailscale data plane outside the VPN extension, so the picker shows Online/Offline and last seen instead of invented latency. Auth URLs and private keys are never logged or shown. English and Simplified Chinese, light and dark, Dynamic Type / font scale, Reduce Motion and screen readers are supported. Android cannot be built on this machine (no SDK); iOS is verified in the simulator, where the VPN itself cannot run.
