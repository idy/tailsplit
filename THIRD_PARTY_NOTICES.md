# Third-party notices

TailSplit is distributed under GPL-3.0-or-later. Its embedded networking core is
[sing-box](https://github.com/SagerNet/sing-box), Copyright 2022 nekohasekai,
licensed under GPL-3.0-or-later; its upstream notice is preserved in
`LICENSES/sing-box.txt`. TailSplit uses its own project name and does not imply
endorsement by the upstream project.

GFWList is downloaded from upstream on demand and cached on disk. The upstream
list is not included in this repository or release packages. Its upstream license
is [LGPL-2.1](https://www.gnu.org/licenses/old-licenses/lgpl-2.1.html).

The Tailscale implementation and other Go modules retain their respective
licenses; exact module versions and source locations are in `go.mod` and `go.sum`.

Windows CLI builds include the unmodified, signed Wintun 0.14.1 DLL for the
selected architecture, embedded by sing-tun. Those prebuilt binaries have separate
redistribution terms from the MIT-licensed Go bindings. Their complete license,
official source URL and archive/DLL hashes are preserved in `LICENSES/wintun.txt`
and included in the generated Windows notices. No extra driver download is made.

Release builds generate detailed dependency notices for the target platform.
The project license, dependency notices and build metadata accompany CLI
archives and Debian packages. The macOS app also carries the networking-core and
dependency notices in its Resources directory. The full corresponding project
source is available in this repository.

## Magpie Design System

The desktop configuration panel bundles
[Magpie Design System](https://github.com/idy/magpie-design-system) v0.4.3,
Copyright (c) idy, under the MIT License. The unmodified CSS, JavaScript,
fonts, icons and full license are in `internal/configweb/assets/vendor/magpie/`.
Source revision: `46ba098239979506a4ccc14721aac044483c66bc`.
The local manifest pins file checksums; `scripts/vendor-magpie.mjs` restores
or verifies the pinned assets. The upstream Magpie MIT notice is retained at
`vendor/magpie/css/Magpie-MIT.txt`. Its visual language is inspired by
[Magpie](https://github.com/yetone/magpie) by yetone.

TailSplit's adapter connects the shared dropdown to the existing configuration
render lifecycle. Application layout overrides remain outside the vendor files.
Bundled fonts retain the SIL Open Font License notices and the Phosphor icons
retain their MIT notice beside the upstream assets.

## Page companion

The configuration panel bundles [Peetty](https://github.com/idy/peetty) 0.2.0,
Copyright (c) 2026 Yan, under the MIT License. The vendored browser bundle,
stylesheet and complete license are in
`internal/configweb/assets/vendor/peetty/`. Source revision:
`ba28bf822a1fee9f1961b2427b38548f23f27612`.

TailSplit adaptations move the overlay's shadow-root stylesheet into a same-origin
CSS file to preserve the restrictive Content Security Policy, make reduced-motion
placement immediate, and bound long-text scans using a clipped bounding rectangle.
The deterministic adaptations are in `scripts/peetty-vendor-tools.mjs`.
Chat is disabled; no chat or provider dependencies are shipped. Regenerate the
assets with `node scripts/vendor-peetty.mjs` (Node.js 22.19+, Git, npm and network
access are needed only when updating the vendored assets).

The desktop can independently download compatible stable Peetty 0.3.x classic
browser releases from jsDelivr. These unmodified files retain their MIT license
and any published `THIRD_PARTY_NOTICES.txt` in the private local cache, and both
notices are served beside the selected version's script. The embedded 0.2.0
adaptation remains the offline and compatibility fallback. Dynamic releases are
verified against source bytes fetched directly from the publisher's GitHub
repository at the resolved tag commit, independently of the delivery CDN. Exact
stylesheet CSP hashes permit the upstream shadow-root stylesheet without
inline-style exceptions. Served scripts wrap the verified upstream bytes in a
TailSplit document facade that gives each instance a private custom-element
host tag and reports creation. DOM copies preserve that tag for lifecycle
ownership; the cached upstream files remain unmodified. Script integrity covers the complete
served wrapper and upstream code.

## Publisher verification certificate roots

Peetty publisher verification uses the unmodified Mozilla public CA bundle from
[`github.com/breml/rootcerts/embedded` v0.3.8](https://github.com/breml/rootcerts/tree/v0.3.8/embedded),
pinned through Go module checksums and compiled into the application. It does
not import the root package's global certificate-store behavior. The project
retains its [BSD-2-Clause license](https://github.com/breml/rootcerts/blob/v0.3.8/LICENSE)
and the CA data's [MPL-2.0 license](https://github.com/breml/rootcerts/blob/v0.3.8/LICENSE.certificates).
The unmodified source and certificate data are available at the linked version.

BSD 2-Clause License

Copyright (c) 2020, Lucas Bremgartner
All rights reserved.

Redistribution and use in source and binary forms, with or without
modification, are permitted provided that the following conditions are met:

1. Redistributions of source code must retain the above copyright notice, this
   list of conditions and the following disclaimer.
2. Redistributions in binary form must reproduce the above copyright notice,
   this list of conditions and the following disclaimer in the documentation
   and/or other materials provided with the distribution.

THIS SOFTWARE IS PROVIDED BY THE COPYRIGHT HOLDERS AND CONTRIBUTORS "AS IS"
AND ANY EXPRESS OR IMPLIED WARRANTIES, INCLUDING, BUT NOT LIMITED TO, THE
IMPLIED WARRANTIES OF MERCHANTABILITY AND FITNESS FOR A PARTICULAR PURPOSE ARE
DISCLAIMED. IN NO EVENT SHALL THE COPYRIGHT HOLDER OR CONTRIBUTORS BE LIABLE
FOR ANY DIRECT, INDIRECT, INCIDENTAL, SPECIAL, EXEMPLARY, OR CONSEQUENTIAL
DAMAGES (INCLUDING, BUT NOT LIMITED TO, PROCUREMENT OF SUBSTITUTE GOODS OR
SERVICES; LOSS OF USE, DATA, OR PROFITS; OR BUSINESS INTERRUPTION) HOWEVER
CAUSED AND ON ANY THEORY OF LIABILITY, WHETHER IN CONTRACT, STRICT LIABILITY,
OR TORT (INCLUDING NEGLIGENCE OR OTHERWISE) ARISING IN ANY WAY OUT OF THE USE
OF THIS SOFTWARE, EVEN IF ADVISED OF THE POSSIBILITY OF SUCH DAMAGE.

## Interface fonts

The desktop configuration panel embeds the same unmodified Latin WOFF2 assets
as the website: Montserrat, Monda, and Lato. They retain the SIL Open Font License
1.1 below. Their original notices are also embedded beside the fonts under
`internal/configweb/assets/fonts/` and served by the local panel at
`/fonts/Montserrat-OFL.txt`, `/fonts/Monda-OFL.txt`, and `/fonts/Lato-OFL.txt`.

- Montserrat: Copyright 2024 The Montserrat.Git Project Authors
  (https://github.com/JulietaUla/Montserrat.git)
- Monda: Copyright 2021 The Monda Project Authors
  (https://github.com/googlefonts/mondaFont)
- Lato: Copyright (c) 2010-2014 by tyPoland Lukasz Dziedzic
  (team@latofonts.com) with Reserved Font Name "Lato"

### SIL OPEN FONT LICENSE Version 1.1 - 26 February 2007

PREAMBLE
The goals of the Open Font License (OFL) are to stimulate worldwide
development of collaborative font projects, to support the font creation
efforts of academic and linguistic communities, and to provide a free and
open framework in which fonts may be shared and improved in partnership
with others.

The OFL allows the licensed fonts to be used, studied, modified and
redistributed freely as long as they are not sold by themselves. The
fonts, including any derivative works, can be bundled, embedded,
redistributed and/or sold with any software provided that any reserved
names are not used by derivative works. The fonts and derivatives,
however, cannot be released under any other type of license. The
requirement for fonts to remain under this license does not apply
to any document created using the fonts or their derivatives.

DEFINITIONS
"Font Software" refers to the set of files released by the Copyright
Holder(s) under this license and clearly marked as such. This may
include source files, build scripts and documentation.

"Reserved Font Name" refers to any names specified as such after the
copyright statement(s).

"Original Version" refers to the collection of Font Software components as
distributed by the Copyright Holder(s).

"Modified Version" refers to any derivative made by adding to, deleting,
or substituting -- in part or in whole -- any of the components of the
Original Version, by changing formats or by porting the Font Software to a
new environment.

"Author" refers to any designer, engineer, programmer, technical
writer or other person who contributed to the Font Software.

PERMISSION & CONDITIONS
Permission is hereby granted, free of charge, to any person obtaining
a copy of the Font Software, to use, study, copy, merge, embed, modify,
redistribute, and sell modified and unmodified copies of the Font
Software, subject to the following conditions:

1) Neither the Font Software nor any of its individual components,
in Original or Modified Versions, may be sold by itself.

2) Original or Modified Versions of the Font Software may be bundled,
redistributed and/or sold with any software, provided that each copy
contains the above copyright notice and this license. These can be
included either as stand-alone text files, human-readable headers or
in the appropriate machine-readable metadata fields within text or
binary files as long as those fields can be easily viewed by the user.

3) No Modified Version of the Font Software may use the Reserved Font
Name(s) unless explicit written permission is granted by the corresponding
Copyright Holder. This restriction only applies to the primary font name as
presented to the users.

4) The name(s) of the Copyright Holder(s) or the Author(s) of the Font
Software shall not be used to promote, endorse or advertise any
Modified Version, except to acknowledge the contribution(s) of the
Copyright Holder(s) and the Author(s) or with their explicit written
permission.

5) The Font Software, modified or unmodified, in part or in whole,
must be distributed entirely under this license, and must not be
distributed under any other license. The requirement for fonts to
remain under this license does not apply to any document created
using the Font Software.

TERMINATION
This license becomes null and void if any of the above conditions are
not met.

DISCLAIMER
THE FONT SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND,
EXPRESS OR IMPLIED, INCLUDING BUT NOT LIMITED TO ANY WARRANTIES OF
MERCHANTABILITY, FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT
OF COPYRIGHT, PATENT, TRADEMARK, OR OTHER RIGHT. IN NO EVENT SHALL THE
COPYRIGHT HOLDER BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER LIABILITY,
INCLUDING ANY GENERAL, SPECIAL, INDIRECT, INCIDENTAL, OR CONSEQUENTIAL
DAMAGES, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING
FROM, OUT OF THE USE OR INABILITY TO USE THE FONT SOFTWARE OR FROM
OTHER DEALINGS IN THE FONT SOFTWARE.

## Interface icons

The configuration panel embeds regular SVG paths from
[Phosphor Icons 2.1.1](https://cdn.jsdelivr.net/npm/@phosphor-icons/core@2.1.1/),
including the CheckCircle selection marker. The inline symbol IDs retain the
upstream icon names as `data-phosphor` attributes. Only the used icons are
bundled; no icon service or runtime package download is required.

### MIT License

Copyright (c) 2023 Phosphor Icons

Permission is hereby granted, free of charge, to any person obtaining a copy
of this software and associated documentation files (the "Software"), to deal
in the Software without restriction, including without limitation the rights
to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
copies of the Software, and to permit persons to whom the Software is
furnished to do so, subject to the following conditions:

The above copyright notice and this permission notice shall be included in all
copies or substantial portions of the Software.

THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
SOFTWARE.
