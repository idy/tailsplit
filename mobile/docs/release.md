# Mobile CI and iOS publication

## Verification

`.github/workflows/mobile-ci.yml` runs Flutter analysis/tests, an Android debug
APK build, and an iOS simulator build plus unsigned device archives for both
release channels. Native cores are built from the app's pinned
`sing_box_flutter` dependency and selected
capabilities. Pull requests do not receive signing credentials or publish apps.
The Dart identity test runs with both the default store identity and the beta
compile-time definition. The host and PacketTunnel subspecs keep distinct
archive-intermediate directories so their identically named frameworks and
resource bundles do not collide.

Android uses SDK 36, JDK 17, NDK 28.0.13004108 for the core and NDK
28.2.13676358 for Flutter packaging. Apple jobs use macOS 26, Flutter 3.47.2,
Python 3.13 and CocoaPods 1.17.0. The selected-core receipt is generated anew
for the checked-out pubspec before CocoaPods or Gradle consumes it.

## Organization and application identities

Both iOS applications use **TERMITOC PTE. LTD.**, Apple team **BGL4WMWD3W**,
the same organization account as `idy/letsolo`. The public
`mobile/release-identity.json` pins both channels:

| Channel | Display name | App bundle ID | Packet Tunnel bundle ID | App Group | Distribution |
| --- | --- | --- | --- | --- | --- |
| `beta` | TailSplit Beta | `dev.idy.tailsplit.beta` | `dev.idy.tailsplit.beta.PacketTunnel` | `group.dev.idy.tailsplit.beta` | TestFlight only |
| `app-store` | TailSplit | `dev.idy.tailsplit` | `dev.idy.tailsplit.PacketTunnel` | `group.dev.idy.tailsplit` | TestFlight and App Store |

The two apps can be installed side by side. Each app shares VPN state only with
its own extension; accounts, device keys, caches and system VPN profiles are
separate. iOS still allows only one active packet tunnel at a time. The existing
store bundle ID and App Group remain unchanged, preserving existing app data.

`ios/configure_project.rb [beta|app-store]` selects bundle identifiers, display
name, test target and App Group for every native build configuration. It defaults
to `app-store` when regenerating the project. Entitlements use the selected
`TAILSPLIT_APP_GROUP` build setting. `tool/build_ios_release.sh` always pairs this
native configuration with the matching `TAILSPLIT_IOS_CHANNEL` Dart definition,
so storage and the VPN select the correct App Group and provider bundle ID.
Use that wrapper for release archives; selecting only the native beta identity
without the Dart definition would leave the VPN pointing at the store identity.

Desktop Developer ID signing and its `release` environment are independent.
Mobile distribution must not fall back to personal-team, development, ad-hoc or
desktop credentials.

## One-time Apple and GitHub setup

1. In Termitoc's Apple Developer account, register all **four explicit bundle
   identifiers** and **two App Groups** from the table. Enable App Groups and
   Network Extension / Packet Tunnel on both apps and both extensions, and
   associate each app and extension with its own channel's App Group.
2. Create **two iOS app records** in Termitoc's App Store Connect account, one for
   each app bundle ID. Even a TestFlight-only application requires an App Store
   Connect record. Do not submit the beta record for public App Review. Complete
   required agreements and export-compliance information for TestFlight, and
   required screenshots, privacy information, review contact and store metadata
   before submitting the store application. Signing profiles do not create app
   records or complete store metadata.
3. Keep the Apple account in the tracked SOPS ciphertext
   `mobile/credentials/apple-distribution.json.sops`. Its decrypted JSON contains
   `issuer_id`, `api_key_id`, `api_private_key` (PEM),
   `certificate_private_key` (PEM) and `team_id` (`BGL4WMWD3W`). The file excludes
   LetSolo's unrelated macOS and Android credential records. Both channels use
   this account but fetch distinct App Store profiles for their bundle IDs.

   Configure only `TAILSPLIT_MOBILE_RELEASE_AGE_KEY` in GitHub's **mobile-release**
   environment. It is a dedicated age identity for this mobile ciphertext, not
   the shared LetSolo operator identity or the desktop release age key. The
   tracked `.sops.yaml` permits both the dedicated CI recipient and the existing
   operator recovery recipient. Plaintext Apple values stay in signing-process
   memory and are masked before signing/upload tools run; they are not written
   to the checkout, logs or artifacts. Only the SOPS subprocess receives the age
   key. Source/dependency build scripts run on a separate uncredentialed VM.
4. Restrict `mobile-release` to protected branches. The workflow additionally
   requires the protected default branch, so feature-branch dispatches cannot
   use release credentials. Add environment reviewers if desired.

The original Termitoc iOS credential record was recovered from LetSolo's recent
secure release migration, reduced to the required fields and encrypted for
TailSplit. Apple API authentication and a matching Termitoc `DISTRIBUTION`
certificate/private key were verified on 2026-11-12; the certificate expires on
2027-11-11. The dedicated `mobile-release` age secret is configured and a real
SOPS round-trip through the release helper passed. Private key recovery metadata
is maintained in the Workspace signing catalog, outside the product checkout.

Existing store app/extension identifiers were found under Termitoc, while no
TailSplit App Store Connect app record was returned by the API during setup.
Beta registration, App Group associations and app records must be completed
before upload. Credential availability alone does not establish store readiness.

## Build, TestFlight and App Store review

Run **iOS release (Termitoc)** on the protected default branch after merging:

| Input | Values | Default |
| --- | --- | --- |
| `channel` | `beta`, `app-store` | `beta` |
| `destination` | `artifact`, `testflight`, `app-store` | `artifact` |

| Selection | Result |
| --- | --- |
| Either channel + `artifact` | Sign and verify an IPA; retain it as an Actions artifact without uploading to Apple. Use this for the first credentialed run. |
| `beta` + `testflight` | Upload TailSplit Beta for internal TestFlight processing. |
| `app-store` + `testflight` | Upload the store application for internal TestFlight processing. |
| `app-store` + `app-store` | Upload the store application, wait for processing and submit that build to public App Review. The same upload is available to internal TestFlight. |
| `beta` + `app-store` | Reject before building or accessing credentials. The signing helper independently rejects this combination. |

The TestFlight destination does not submit external beta review or assign tester
groups. Those can be configured in App Store Connect. Public App Review uses
Codemagic's `--app-store --platform IOS --release-type MANUAL`: Apple still has
to approve the application, then the owner releases it manually in App Store
Connect. This CI does not automatically make an approved version public. The
beta restriction is enforced by this workflow/helper; it is not an Apple account
permission that prevents someone from submitting the beta record manually.

Each run releases one channel. Channels have separate concurrency groups and
artifact names; two runs of the same channel are serialized. Compilation uses
the Actions run number for both the app and extension's build number.

For a local unsigned archive, resolve packages, build the selected Apple core
and install pods as described in the mobile README, then run from `mobile/`:

```bash
bash tool/build_ios_release.sh beta --build-number=123
```

```bash
bash tool/build_ios_release.sh app-store --build-number=124
```

The wrapper changes the local Xcode project to the chosen channel. Run
`ruby ios/configure_project.rb app-store` to restore its default identity before
regular development. It requires the `xcodeproj` gem installed with CocoaPods.

## Signing isolation and validation

Compilation and signing run in **separate, fresh GitHub-hosted macOS jobs**:

1. `build` has no `mobile-release` environment or Apple secrets. It resolves and
   tests packages, builds the selected native core, installs CocoaPods, and runs
   the channel wrapper with `flutter build ipa --release --no-codesign --no-pub`.
   All Flutter, CocoaPods, dependency and Xcode build scripts finish on this
   runner. The unsigned archive ZIP is retained for one day for transfer.
2. `sign` starts on another VM behind the protected-default-branch and
   `mobile-release` environment gates. It checks out only the release helper,
   identity and SOPS ciphertext from the workflow's exact commit. It installs
   Codemagic CLI tools 0.70.2 in a fresh virtual environment and a SOPS 3.13.3
   binary verified against its pinned SHA-256. It does not restore build caches,
   install application dependencies, check out an Xcode project, invoke Flutter,
   or build a project. Never combine these jobs or use a persistent signing runner.
3. The signing job fetches the raw Actions artifact ZIP by artifact ID, verifies
   its run, commit and channel-specific name, and leaves it unextracted. The
   helper validates the outer ZIP and its single `unsigned.xcarchive.zip` before
   copying regular files into new private directories. It rejects links, special
   files, absolute/traversing paths, duplicate and case/Unicode-aliased names,
   file/directory conflicts and oversized contents. Artifact data cannot
   overwrite the helper, tools, home directory or runner command files.
4. Validate the selected channel's exact app/extension identities and build
   number, require local Mach-O executables, and reconstruct archive metadata
   with the fixed `Applications/Runner.app` path. Reject unexpected archive
   products and nested apps/extensions. No archive-supplied script or executable
   is run, and framework executable paths must stay within their bundles.
5. Fetch/create exact App Store signing profiles for the selected app and
   extension. Verify team, application identifiers, App Group, Packet Tunnel
   entitlement, distribution type and expiry. Install profiles by validated UUID
   and import certificates into a temporary keychain.
6. Run only `xcodebuild -exportArchive` with generated options:
   `app-store-connect`, `destination=export`, manual signing, `Apple Distribution`,
   team `BGL4WMWD3W`, explicit profile UUIDs and no automatic version rewriting.
   No project/scheme build, `xcode-project use-profiles`, dependency script or
   provisioning-update flag participates.
7. Safely unpack the IPA, validate embedded profiles and both code signatures,
   and require the actual signer team to be Termitoc. Independently decode each
   bundle's signed entitlements: require the selected application/team identity,
   App Group and Packet Tunnel capability, and reject debug-enabled signatures.
   Only then copy the IPA to the output directory and optionally upload/submit it.
8. Remove installed profiles and restore/delete the temporary keychain during
   cleanup. The ephemeral VM discards its remaining temporary files. Verified
   IPAs are retained for seven days, including when later upload/review fails;
   signing keys and profiles are not uploaded as separate artifacts.

The runner boundary prevents build scripts from reading a credentialed parent
process or accessing its signing keychain. Trusted tool environments further
limit incidental exposure: profile retrieval receives API/certificate credentials,
upload receives API credentials, and keychain setup receives its password.
Commands use trusted absolute tool paths outside artifact contents. The workflow
still trusts GitHub-hosted runners, the protected release helper, pinned signing
tools and Apple's export/verification tools. Signing does not establish that
compiled app code is benign; source/dependency review remains necessary.

Missing or mismatched inputs fail the release. The unsigned transfer artifact is
never uploaded to Apple. Only the verified IPA is retained as a signed artifact.

## Current limits

Automated release tests cover both channel identities, cross-channel archive
rejection, beta public-review rejection, store review arguments, malicious ZIP
fixtures, job isolation, and mocked export/signature/upload orchestration. They
invoke no real Apple API, signing identity or keychain operation. Local checks
also verify native Debug/Profile/Release settings for both channels and Dart VPN
identifiers with both compile-time definitions.

Real Apple API authentication, a matching Termitoc distribution certificate and
SOPS decryption have been verified. Signed archive export, TestFlight upload and
App Store submission remain **unverified** on the protected release runner.
Complete the remaining Apple identifiers, App Groups, profiles and app records
before upload. Physical iOS devices are required to verify VPN traffic. Android
CI verifies debug builds; Google Play publication is not configured here.
