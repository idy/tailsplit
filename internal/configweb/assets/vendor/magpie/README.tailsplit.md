# Magpie Design System in TailSplit

Upstream: https://github.com/idy/magpie-design-system

Release: **[v0.4.3](https://github.com/idy/magpie-design-system/releases/tag/v0.4.3)**

Pinned source: `46ba098239979506a4ccc14721aac044483c66bc`

Upstream controls: https://github.com/idy/magpie-design-system/pull/1

Dropdown dismissal fixes: https://github.com/idy/magpie-design-system/pull/2

This release includes the upstream text-dropdown, preference-row and
stacked-row variants extracted from TailSplit, plus slim 20px update
faces and controlled button switches using the shared track and knob. The published tag is locked to the source commit above. TailSplit consumes
those shared classes directly instead of maintaining local copies.

License: MIT; bundled fonts and icons retain their own notices.

The `css/`, `js/`, `fonts/`, `icons/` and `LICENSE` files are unmodified
upstream assets. `manifest.json` records each vendored file's SHA-256 hash.
Serve the complete directory from the embedded panel's own origin; relative
CSS font and icon URLs must keep this layout. No CDN, package installation
or runtime build step is required.

`/ui.js` loads the application in dependency order. `/select.js` adapts
Magpie's `initSelect()` to TailSplit's existing render lifecycle; application
state, authenticated APIs and draft protection remain in `/app.js`.

`/style.css` contains only TailSplit layout constraints, connection and node
states, routing forms and the diagnostic terminal. Shared colours, typography,
controls and shell components come from Magpie. Product-specific overrides
are scoped to their application classes rather than modifying the vendor.

To restore or update assets, edit the pinned revision and hashes in
`manifest.json` as appropriate, then run `node scripts/vendor-magpie.mjs`.
To check the current copy without network access:

```sh
node scripts/vendor-magpie.mjs --check
```
