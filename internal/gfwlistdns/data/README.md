# Embedded GFWList source

- Upstream: <https://github.com/gfwlist/gfwlist>
- Revision: `3fd14db3df72cbc2ed3bbd51310927dfd45893f9`
- Source: <https://raw.githubusercontent.com/gfwlist/gfwlist/3fd14db3df72cbc2ed3bbd51310927dfd45893f9/gfwlist.txt>
- Retrieved again from the latest official `master` revision:
  2026-09-22T00:02:40Z (2026-09-22 Asia/Shanghai).
- Upstream commit time: 2026-09-21T12:28:01Z. The fresh download is byte-identical
  to the previous bundled snapshot; missing application dependencies are not
  caused by an older upstream version.
- `list.txt` is the unmodified base64-decoded source, including upstream notices.
- SHA-256 of `list.txt`: `28e264c9e23feaa4d5ad725113a2b7ed1160982706e0e81b359ea0a9d6592627`
- Upstream license: GNU LGPL 2.1; see the accompanying unmodified [COPYING.txt](COPYING.txt).

The domain parser and projection are implemented in `../rules.go` and
`../projection.go`; they do not modify the source list. To update the embedded
snapshot, fetch `gfwlist.txt` at a specific upstream commit, base64-decode it into
`list.txt`, retain the license, and update `BuiltinRevision` and this manifest.
Run `go test ./internal/gfwlistdns` from the project root before committing.
This package contains only parsing and projection; it does not run a DNS server.

## Application dependency supplement

[supplemental.txt](supplemental.txt) is a separately authored, curated set of 42
DNS rules. It does not modify or claim to be part of the upstream GFWList.
Sources checked on 2026-09-21/22 UTC (2026-09-22 Asia/Shanghai):

| Group | Source and interpretation |
| --- | --- |
| ChatGPT app/resources/support | [OpenAI network recommendations](https://help.openai.com/en/articles/9247338): first-party domains, attachments, WorkOS hosts, verification, Intercom, email links, named diagnostic hosts and `humb.apple.com`. First-party suffixes also cover the listed desktop/auth/API hosts. |
| Claude desktop/content | [Claude Desktop network access](https://code.claude.com/docs/en/desktop#network-access-requirements): Anthropic/Claude domains, `claude.app`, user content and MCP content. This documentation describes the Code tab, not every Chat/Cowork dependency. |
| Claude community additions | [v2fly Anthropic data](https://github.com/v2fly/domain-list-community/blob/8dee9683b99554b777eecb54a894670af46db9b1/data/anthropic): `clau.de`, `claudemcpclient.com`, and the exact `servd-anthropic-website.b-cdn.net` host, alongside the first-party domains. |
| Stripe payment/resources | [Stripe domains and IPs](https://docs.stripe.com/ips): checkout, billing, invoice, JS/API and CDN hosts, `m.stripe.network` and hCaptcha. Suffix policies cover `stripe.com`, `stripe.network`, `stripecdn.com` and `hcaptcha.com`. |
| Link payments | [v2fly Stripe data](https://github.com/v2fly/domain-list-community/blob/8dee9683b99554b777eecb54a894670af46db9b1/data/stripe) includes `link.com`. The generic Stripe Terminal/developer/event domains are not imported. |
| Community auth/feature/voice dependencies | [blackmatrix7 OpenAI rules](https://github.com/blackmatrix7/ios_rule_script/blob/51d2e1dd8429a91be575157d6f8ed051c93559f8/rule/Clash/OpenAI/OpenAI.list) list Auth0, Arkose, Statsig/feature configuration and LiveKit hosts. Broad shared-provider rules are narrowed to the named hosts where possible. The source file's stated update date is 2025-06-06; these are compatibility candidates, not fresh traffic captures. |

Community discussion corroborates the reason for a supplement:
[LINUX DO](https://linux.do/t/topic/208375) (2024) includes Auth0 and Stripe in
AI routing, and [V2EX](https://v2ex.com/t/963124) (2023) names Stripe invoice
traffic. These historical reports are not desktop acceptance tests.

`||domain` covers the root plus subdomains; `|hostname|` covers only that exact
host. All entries are proxy-only; active GFWList exceptions always win. Shared
service suffixes affect every application using those domains, because DNS
does not carry the requesting app or initiating website. Membership does not
assert that every dependency is required for every feature or app version.

`ProjectActiveRules` reapplies the supplement whenever TailSplit loads the
bundled, cached, or downloaded rules. The supplement's source bytes are never
mixed into the downloaded cache. It has separate rule counts and a normalized
SHA-256 in `RulesInfo`. Maintain it through reviewed source changes: verify the
affected service/vendor/community source, update this manifest, and run the
offline package and TailSplit routing tests. Projection tests preserve label
boundaries, exact-host scope, and direct-exception precedence.

No provider ASN, shared CDN block, Stripe webhook source IP or historical
community IP is converted into an additional real route here. This package
projects domains; TailSplit's routing engine handles DNS and IP routes.
