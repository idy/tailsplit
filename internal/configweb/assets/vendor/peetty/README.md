# Vendored Peetty

- Upstream: https://github.com/idy/peetty
- Package: `@idy/peetty` 0.2.0
- Revision: `ba28bf822a1fee9f1961b2427b38548f23f27612`
- License: MIT; see `LICENSE.txt`.
- Build: esbuild 0.28.2, browser IIFE, ES2022, minified.

Run `node scripts/vendor-peetty.mjs` from the repository root to regenerate these
assets. Normal TailSplit builds embed the committed assets and need no Node.js,
npm, GitHub Packages authentication or CDN access.

The build moves the upstream shadow-root stylesheet into `peetty.css`, loaded by
a same-origin link. No inline-style CSP exception is needed. Other upstream
behavior is retained. The host integration in `../../companion.js` uses the
bundled Miru preset and disables chat. Chat chunks and provider packages are not
included; do not enable chat using this bundle.
