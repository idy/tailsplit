---
version: 1
slug: "internal-configweb-assets-index-html"
primary_target: "internal/configweb/assets/index.html"
related_targets: ["internal/configweb/assets/app.js", "internal/configweb/assets/style.css", "internal/configweb/assets/select.js", "internal/configweb/assets/ui.js", "internal/configweb/assets/i18n.js"]
---

# Desktop connection center

Visitor mode: Operate. Desktop users authorize embedded Tailscale devices, choose exits, configure routing and explicitly connect.

## Direction contract

THESIS: Migrate the existing connection center to the user's specified idy/magpie-design-system, preserving its tasks and navigation.

OWN-WORLD: Self-host unmodified Magpie source pinned to aefce96c4022b692bf7e2f4e05d64901a130964f (v0.4.2), including shared text-dropdown, preference-row, stacked-row and compact toolbar variants. Controlled switches use the shared light knob; responsive update pills draw 20px capsule faces aligned with switch tracks inside transparent click targets, distinct update/progress/error glyphs, and equal 8px intervals between all visible header regions. Use its --mds-* tokens and mds-* components: neutral system appearances, blue actions, 14px Lato body, Montserrat actions, regular Monda headings, 34px controls and shared radius scale. Keep monospace for YAML and logs.

STORY: Add or sign in to an exit, open its picker, test nodes individually, confirm selection and connect. Existing authentication, save/reconnect, revision checks and draft protections remain authoritative.

FIRST VIEWPORT: Persistent sidebar, centered workspace capped at 1040px and fixed native chrome. Add exit and current gateway precede the saved-exit list. Settings remains at the sidebar bottom. The titlebar remains a single row on every page. YAML editing opens from the file-information row below Settings and returns through an action inside the source editor. At 640px and below, use the icon sidebar and category picker.

FORM: Existing desktop connection center and list-only node picker, migrated directly to the pinned library; no new identity or concept round. Previous incumbent reference: seed:413a3804ca48439d.

FINISH: Verify English/Chinese, light/dark and narrow/wide layouts, shared dropdowns, per-node Ping and save/connect interactions. Finish review and documentation describe the shipped build. No raster assets or product release is part of the change. Reusable component variants are contributed upstream by PR.

## Boundaries and states

Only the workspace or whole picker scrolls; the diagnostic terminal retains its own bounded scroller. Preserve MyGo drag regions, native window controls, keyboard focus, reduced motion and accessible announcements. Shared selects retain native values and input/change events, clean up removed route controls and stay open through unchanged state polls. Keep unavailable configured nodes visible and prevent their selection; never silently replace an exit. Ping is explicit and independent of selection. Node rows retain their plain green check, 80px latency column and separate Ping action. Preview data is explicitly simulated. Preserve all PRODUCT.md networking, identity, rule-retention and authorization boundaries.
