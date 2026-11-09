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
a same-origin link. No inline-style CSP exception is needed. Additional
adaptations in `scripts/peetty-vendor-tools.mjs` place the figure immediately
at the nearest safe spot instead of falling or jumping under reduced motion,
keep explicit moves available through immediate repositioning, interrupt motion
when that preference changes, and treat oversized text as one clipped obstacle so long
logs do not invalidate a complete page scan.

The builder normalizes checked-out source line endings, invokes npm's JavaScript
entry through Node on Windows, and stages
all output until the build succeeds. The host integration in `../../companion.js`
uses the bundled Miru preset and disables chat. Chat chunks and provider packages
are not included; do not enable chat using this bundle.
