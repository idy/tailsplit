# Mobile CI and iOS publication

## Verification

`.github/workflows/mobile-ci.yml` runs Flutter analysis/tests, an Android debug
APK build, and iOS simulator plus unsigned device builds. Native cores are built
from the app's pinned `sing_box_flutter` dependency and selected capabilities.
Pull requests do not receive signing credentials or publish applications.

The Android job installs SDK 36, JDK 17, NDK 28.0.13004108 for the core and NDK
28.2.13676358 for Flutter packaging. Apple jobs use macOS 26, Flutter 3.47.2,
Python 3.13 and CocoaPods 1.17.0. The selected-core receipt is generated anew
for the checked-out pubspec before CocoaPods or Gradle consumes it.

## Organization identity

Mobile iOS distribution belongs to **TERMITOC PTE. LTD.**, Apple team
**BGL4WMWD3W**, as in `idy/letsolo`'s application release workflow. The public
`mobile/release-identity.json` records:

- App: `dev.idy.tailsplit`
- Packet Tunnel extension: `dev.idy.tailsplit.PacketTunnel`
- Shared App Group: `group.dev.idy.tailsplit`

Both Xcode targets and the regeneration script select this team. Desktop
Developer ID signing and its `release` environment are independent; do not reuse
the desktop's personal-team credentials for mobile App Store distribution.

## One-time Apple and GitHub setup

1. Register both explicit bundle identifiers and the shared App Group under
   Termitoc. Enable App Groups and the Network Extension / Packet Tunnel
   capability on both targets, and associate the group with both identifiers.
2. Create the TailSplit iOS application record in Termitoc's App Store Connect
   account. Complete the required agreements and app metadata there. Creating
   signing profiles alone does not create an App Store application record.
3. Populate the GitHub **mobile-release** environment's
   `BGL4WMWD3W_APPLE_CREDENTIALS` secret with the same organization account used
   by LetSolo. It is a JSON object with these fields:

   | Field | Meaning |
   | --- | --- |
   | `issuer_id` | App Store Connect API issuer |
   | `api_key_id` | API key identifier |
   | `api_private_key` | PEM App Store Connect API private key |
   | `certificate_private_key` | PEM private key matching the Apple distribution certificate |

   An optional `team_id` must equal `BGL4WMWD3W`. GitHub repository secrets are
   scoped to their repository: the secret in `idy/letsolo` is **not inherited**
   by `idy/tailsplit`, and the API cannot read it back. Transfer the original
   value through the approved credential source or GitHub secret settings,
   never through the PR, chat or logs.
4. Restrict `mobile-release` to protected branches. The workflow also requires
   the protected default branch, so dispatching a feature branch cannot use
   release credentials. Add environment reviewers if release approval is desired.

At introduction, the Termitoc secret was confirmed to exist in `idy/letsolo` but
was absent from `idy/tailsplit`. No credentials or Apple registrations have been
created or copied by opening this PR. The signed workflow stops with a setup
message until the secret and Apple prerequisites are available.

## Build and upload

Run **iOS release (Termitoc)** from the protected default branch after merging.
The `publish` input defaults to **false**: it signs and verifies an IPA and
retains it as a GitHub Actions artifact. Set `publish=true` explicitly to upload
that IPA to App Store Connect for internal TestFlight processing. This does not
submit a public App Store release or external beta review.

The workflow uses the same Codemagic signing approach as LetSolo:

1. Fetch/create exact App Store signing profiles for both bundle identifiers.
2. Verify the team, app identifiers, App Group, Packet Tunnel entitlement,
   distribution type and expiry before building.
3. Import certificates into a temporary keychain and apply only these profiles.
4. Build a release IPA without preview flags, using the Actions run number.
5. Inspect the exported app and extension, validate their embedded profiles,
   verify code signatures, and require the actual signer team to be Termitoc.
   Decode each bundle's signed entitlements independently of its profile: require
   the exact application/team identity, shared App Group and Packet Tunnel
   capability, and reject debug-enabled signatures.
6. Upload only when requested, then remove temporary signing material/keychain.

Flutter, Xcode and read-only verification commands receive an environment without
Apple API private keys, certificate private keys or the keychain password. Only
signing-file retrieval receives both API and certificate credentials; upload
receives API credentials, and keychain operations receive the temporary password.
The build signs through the prepared keychain without inheriting those secrets.

Missing or mismatched inputs fail the release. There is no fallback to personal
signing, ad-hoc signing, a development profile or a different organization. Only
the verified IPA is uploaded as a workflow artifact; keys and profiles are not.

## Current limits

Signed distribution and App Store upload cannot be validated without the
Termitoc credential and application setup. A physical iOS device is required
for real VPN traffic verification. Android CI currently verifies debug builds;
Android release builds deliberately do not fall back to debug signing, and
Google Play publication is not configured by this iOS workflow.
