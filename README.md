# TailSplit website

This repository is a **site-only scaffold** for the TailSplit product website at [tailsplit.idy.dev](https://tailsplit.idy.dev/). It does not currently contain the working TailSplit application source, build system, or application releases.

TailSplit is a macOS menu bar application for domain and IP routing rules, application HTTP/SOCKS proxy use, optional system-traffic routing, and configured Tailscale destinations. Its connection modes use a privileged background network service.

The existing application remains in `idy/letsolo`:

- [Application source](https://github.com/idy/letsolo/tree/master/tailsplit)
- [Application documentation and build instructions](https://github.com/idy/letsolo/blob/master/tailsplit/README.md)
- [Existing downloads and release notes](https://github.com/idy/letsolo/releases)

These repositories are private. Access to the website does not grant access to the source or downloads. Check a distribution's release notes for architecture and signing status; local and pull-request builds are ad-hoc development builds.

## Website development

The site is self-contained HTML, CSS, JavaScript, and SVG. It has no package dependencies or build step. Edit files in `site/` and preview them as static files. For an HTTP preview, Python's standard library is sufficient:

```sh
python3 -m http.server 8000 --directory site
```

Open `http://localhost:8000/`. The route selector is an illustrative client-side demo; it does not make network connections or change network settings. The copy button uses the browser clipboard API and selects the command for manual copying when clipboard access is unavailable.

Key files:

- `site/index.html`: product content, accessible page structure, and routing illustration.
- `site/styles.css`: responsive layout, typography, color, focus states, and reduced-motion support.
- `site/script.js`: conceptual route selection and command copying.
- `site/favicon.svg`: self-contained TailSplit icon.
- `site/CNAME`: the intended custom domain, `tailsplit.idy.dev`.
- `site/.nojekyll`: keeps the deployment as a plain static site.
- `.github/workflows/pages.yml`: uploads `site/` and deploys with GitHub Pages.

## GitHub Pages

The Pages workflow runs on `main` when the site or its workflow changes, and supports manual dispatch from `main`. It uploads the static site directly, with no secrets or generated application artifacts. All referenced Actions are pinned to commit hashes, with version comments identifying their major releases.

The public site is intended to use `https://tailsplit.idy.dev/`; the source repository remains private. Repository Pages configuration, domain verification, DNS, and publication are separate setup steps. The `CNAME` file records the intended domain and does not configure DNS by itself.

## Companion project

[TailRelay](https://tailrelay.idy.dev/) provides a SOCKS5-backed Tailscale exit node. Its independent source repository is [idy/tailrelay](https://github.com/idy/tailrelay), which also requires repository access.
