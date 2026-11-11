# Mobile CI and iOS publication

## Verification

`.github/workflows/mobile-ci.yml` runs Flutter analysis/tests, an Android debug
APK build, and iOS simulator plus unsigned device archive builds. Native cores are built
from the app's pinned `sing_box_flutter` dependency and selected capabilities.
Pull requests do not receive signing credentials or publish applications.
The host and PacketTunnel subspecs keep distinct archive-intermediate directories
so their identically named frameworks/resource bundles do not collide. A local
unsigned archive was built, packaged, and accepted by the signing-input validator;
this did not use credentials or validate signed export.

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

Compilation and signing run in **separate, fresh GitHub-hosted macOS jobs**:

1. `build` has no `mobile-release` environment or Apple secrets. It resolves and
   tests packages, builds the selected native core, installs CocoaPods, and runs
   `flutter build ipa --release --no-codesign --no-pub` with the Actions run
   number. Flutter produces `Runner.xcarchive` and skips IPA export when signing
   is disabled. All Flutter, CocoaPods, dependency and Xcode build scripts finish
   on this runner. The unsigned archive ZIP is retained for one day for transfer.
2. `sign` starts on another VM behind the protected-default-branch and
   `mobile-release` environment gates. It checks out only the release helper and
   identity from the workflow's exact commit and installs pinned Codemagic signing
   tools in a fresh virtual environment. It does not restore build caches, install
   application dependencies, check out an Xcode project, or invoke Flutter or a
   project build. Never combine these jobs or move signing to a persistent runner.
3. The signing job fetches the raw Actions artifact ZIP by its artifact ID, checks
   that its run, commit and name match this run, and leaves it unextracted. The
   helper validates both the outer ZIP and its single `unsigned.xcarchive.zip`
   before copying regular files into new private directories. It rejects symlinks,
   special files, absolute/traversing paths, duplicate and case/Unicode-aliased
   names, file/directory conflicts and oversized contents. Artifact data cannot
   overwrite the helper, tools, home directory or runner command files.
4. Validate the exact app/extension identities and build number, require local
   Mach-O executables, and reconstruct archive metadata with the fixed
   `Applications/Runner.app` path. Reject unexpected archive products and nested
   applications/extensions. Embedded resource files are data; no archive-supplied
   script or executable is run. Even framework executable paths must stay within
   their bundles. Archives containing symlinks fail closed, including otherwise
   legitimate layouts that would need deliberate future support.
5. Fetch/create exact App Store signing profiles for both bundle identifiers with
   Codemagic. Verify the team, application identifiers, App Group, Packet Tunnel
   entitlement, distribution type and expiry. Install profiles by validated UUID
   in Xcode's profile directory and import certificates into a temporary keychain.
6. Run only `xcodebuild -exportArchive` on the validated archive, using generated
   export options: `app-store-connect`, `destination=export`, manual signing,
   `Apple Distribution`, team `BGL4WMWD3W`, and explicit UUID profile mappings for
   both bundles. No project, scheme build, `xcode-project use-profiles`, dependency
   script or provisioning-update flag participates in this step.
7. Safely unpack the exported IPA, validate both embedded profiles, verify both
   code signatures, and require the actual signer team to be Termitoc. Decode
   each bundle's signed entitlements independently of its profile: require the
   exact application/team identity, shared App Group and Packet Tunnel capability,
   and reject debug-enabled signatures. Only then copy the IPA to the output
   artifact directory and upload to Apple if `publish=true`.
8. Remove installed profiles and restore/delete the temporary signing keychain in
   cleanup. The ephemeral signing VM also discards the temporary material at job
   termination. Signed IPA artifacts are retained for seven days; signing keys
   and profiles are not uploaded as separate artifacts.

The runner boundary prevents a build script from reading a credentialed parent
process or accessing its signing keychain. Limiting each trusted tool's environment
is additional hygiene, not isolation: profile retrieval receives API/certificate
credentials, upload receives API credentials, and keychain setup receives its
password. Commands use trusted absolute tool paths and a working directory outside
artifact contents. The workflow still trusts GitHub-hosted runners, the checked-out
protected release helper, pinned signing tools and Apple's export/verification
tools to process archive data safely. Signing does not establish that compiled
application code is benign; source/dependency review remains necessary.

Missing or mismatched inputs fail the release. There is no fallback to personal
signing, ad-hoc signing, a development profile or a different organization. The
unsigned transfer artifact is never published to Apple, and only the verified IPA
is retained as the signed release artifact.

## Current limits

Local release validation covers malicious archive fixtures, workflow isolation
contracts, and mocked export/signature/upload orchestration; it invokes no real
Apple API, signing identity or keychain operation. Installed Flutter source and
`xcodebuild -help` confirm unsigned archive and manual export syntax. Signed
archive export on the release runner and App Store upload remain **unverified**
until the Termitoc credential and application setup are available; the first
protected-branch run should keep `publish=false` to check the real export and
entitlements. A physical iOS device is required for real VPN traffic verification.
Android CI currently verifies debug builds;
Android release builds deliberately do not fall back to debug signing, and
Google Play publication is not configured by this iOS workflow.
