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

Both apps and their PacketTunnel extension identifiers are registered under
Termitoc. App Groups and Network Extension capabilities are enabled on all four
identifiers. Each app/extension pair's exact App Group association was saved and
read back from the Developer Portal.

The two iOS App Store Connect records were created and independently verified
through the Apple API on 2026-11-12:

| Channel | ASC app | Apple ID | SKU |
| --- | --- | --- | --- |
| `app-store` | [TailSplit](https://appstoreconnect.apple.com/apps/6833766298) | `6833766298` | `dev.idy.tailsplit` |
| `beta` | [TailSplit Beta](https://appstoreconnect.apple.com/apps/6833766631) | `6833766631` | `dev.idy.tailsplit.beta` |

Both records use primary locale `en-US`. Their initial iOS draft versions are
`0.1.0`, matching `mobile/pubspec.yaml`, with `MANUAL` release and
`PREPARE_FOR_SUBMISSION` state. Creating these records does not upload a build
or submit an App Store review.
Screenshots, privacy information and review details remain separate publication
requirements. On 2026-11-12 the operator confirmed that TailSplit is exempt from
providing encryption export documentation. `Runner/Info.plist` records this as
`ITSAppUsesNonExemptEncryption=false` for future builds. The same confirmed
declaration was applied to the first two uploaded builds in ASC, changing their
internal state from `MISSING_EXPORT_COMPLIANCE` to `READY_FOR_BETA_TESTING`.

## First-use VPN privacy disclosure

Production builds show a bundled English/Simplified Chinese **VPN & Privacy**
page before the normal interface. It explains local storage, Tailscale device
registration and connection metadata, exit/DNS operators, rule downloads and user
controls, and links to the same privacy policy recorded in App Store Connect.
**Agree and continue** must save successfully before network actions are enabled.
Acceptance does not authorize a Tailnet or start a VPN. Existing preferences
without the acceptance flag require this step. While preferences are loading,
the app shows a neutral startup screen. Automatic node refresh is blocked until
consent is saved; saved Tailnets then receive one background startup refresh while
the VPN is idle, without delaying the consent button. An existing OS tunnel can
still be disconnected from this page.
Settings > About keeps both the disclosure and Privacy Policy available later.
Design-preview builds use simulated data and bypass this first-use gate.

This addresses observable gaps against App Review Guidelines 5.1.1(i) and 5.4;
it does not establish the reason for a particular App Review rejection.

## Resubmitting the initial store version after rejection

If the initial store version is rejected before first release, keep its marketing
version (`0.1.0`) and upload a new build. The release workflow sets the app and
extension build numbers from its new Actions run number; do not reuse an archive
that predates the reviewed source change.

1. Read the actual App Review message under the store app's **App Review** page,
   **In Progress > Resolve**. Public review-submission status APIs do not provide
   its message body. Resolve every cited issue and obtain an independent source
   review before release. A privacy change alone cannot prove every issue resolved.
2. Check the reviewer can test the VPN on a physical device. The interface can
   open without an account, but VPN testing still requires a usable Tailscale
   account, authorized Tailnet and reachable approved exit node. UI captures with
   simulated data do not provide that access. Put valid review-access instructions
   or approved credentials in App Review Information, never in Git or public logs.
3. Merge the reviewed PR and wait for the mobile checks. Build and upload only the
   store channel from protected `main`, without submitting yet:

   ```bash
   gh workflow run mobile-release.yml --repo idy/tailsplit --ref main \
     -f channel=app-store -f destination=testflight
   ```

   Omit `archive_run_id` to compile the new source. Record the run ID, source SHA,
   run number and verified `testflight.json` receipt. The `mobile-release`
   environment already supplies `TAILSPLIT_MOBILE_RELEASE_AGE_KEY`; do not replace
   it with the desktop key or unrelated injected Apple credentials.
4. Once the exact new `0.1.0` build is `VALID` and export-compliance ready, open
   **Resolve > Edit** for the rejected app-version item. Select the new build,
   update Notes for Review to describe the first-use privacy page and changes
   addressing Apple's actual message, and save any other required metadata.
   Keep the existing confirmed export declaration and **Manual release** choice.
   Click **Add for Review**, then **Resubmit to App Review** on the submission page.
   Do not remove the rejected item as a workaround; Apple does not let a removed
   item be added back to that same submission.
5. Read back the app version's selected build and the review submission/item
   states from ASC. Record the new submission time and confirm `WAITING_FOR_REVIEW`
   or `IN_REVIEW`. A TestFlight-ready build, successful CLI exit, or a receipt's
   requested `submitted_for_review` flag does not independently confirm submission.

The workflow's `destination=app-store` also requests public review during upload.
For an existing **Unresolved Issues** submission, the upload-only path above lets
an operator edit and resubmit the rejected item in Apple's documented order.
See [Apple's unresolved-issues instructions](https://developer.apple.com/help/app-store-connect/manage-submissions-to-app-review/manage-a-submission-with-unresolved-issues).
Do not dispatch or submit the separate Beta app as part of store recovery.

## Build, TestFlight and App Store review

Run **iOS release (Termitoc)** on the protected default branch after merging:

| Input | Values | Default |
| --- | --- | --- |
| `channel` | `beta`, `app-store` | `beta` |
| `destination` | `artifact`, `testflight`, `app-store` | `testflight` |
| `archive_run_id` | Optional completed release run on `main` with a successful unsigned build | Empty: build a new archive |

| Selection | Result |
| --- | --- |
| Either channel + `artifact` | Explicit signing/export check only; retain the verified IPA without uploading to Apple. |
| `beta` + `testflight` | Upload TailSplit Beta, wait for Apple processing, add it to its internal test group, and verify readiness and membership. |
| `app-store` + `testflight` | Upload the store app, wait for Apple processing, add it to its internal test group, and verify readiness and membership. |
| `app-store` + `app-store` | Upload the store application, wait for processing and submit that build to public App Review. The same upload is available to internal TestFlight. |
| `beta` + `app-store` | Reject before building or accessing credentials. The signing helper independently rejects this combination. |

Each app has its own `Internal Testing` group, pinned by app ID and group UUID
in `release-identity.json`. The TestFlight publisher verifies the group belongs
to the selected app and is internal before uploading. It passes `--beta-group`
to Codemagic, then independently reads the exact build's version, processing
state, internal readiness and group membership from Apple. A successful upload
without verified group assignment is a failed release, not a successful delivery.
Only then is a nonsecret `testflight.json` receipt written alongside the IPA.

The groups and automatic CI assignment do not invite or manage testers. Add
people in ASC separately. External beta review is not requested by this flow.
Public App Review uses Codemagic's `--app-store --platform IOS --release-type MANUAL`: Apple still has
to approve the application, then the owner releases it manually in App Store
Connect. This CI does not automatically make an approved version public. The
beta restriction is enforced by this workflow/helper; it is not an Apple account
permission that prevents someone from submitting the beta record manually.

Each run releases one channel. Channels have separate concurrency groups and
artifact names; two runs of the same channel are serialized. Compilation uses
the Actions run number for both the app and extension's build number.

To retry signing or delivery without recompiling, set `archive_run_id` to the
original release run. The source must be a completed dispatch of this workflow
on the same repository's default branch, with a successful unsigned build for
the selected channel. Its commit must already be an ancestor of the protected
release revision. The workflow verifies the source job, unexpired channel/run
artifact name, artifact owner run and source SHA before download. Signing still
runs on a fresh runner with the current trusted helpers and the original archive
build number. This also supports recovery when the source run's build succeeded
but its signing job failed; arbitrary artifacts, fork/PR runs and divergent
source revisions are rejected.

For a validated archive retry, the publisher also checks for an existing exact
app/version/build-number upload before invoking Codemagic. A ready, unexpired
build resumes group distribution with package upload and validation skipped;
there is no second binary upload with the same build number. If it is already
assigned, the publisher performs only the final independent readback. An empty
Apple response permits a normal fresh upload, while ambiguous, malformed,
expired, processing or compliance-blocked existing builds fail before retrying.
The receipt records `resumed_upload` and the verified build ID. Fresh release
runs continue to validate and upload their package normally.

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
   `mobile-release` environment gates. It checks out only the signing and
   TestFlight publisher helpers, identity and SOPS ciphertext from the workflow's
   exact commit. It installs
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
6. Sign embedded frameworks, executable resource bundles and dynamic libraries
   first, without app entitlements. Then sign the PacketTunnel extension and
   host with their own validated profiles and explicit pinned application/team,
   App Group and Packet Tunnel entitlements. Never use deep signing to copy app
   entitlements onto libraries. This makes the required grants available to
   Xcode even when the source archive has never been signed.
   Run `xcodebuild -exportArchive` with generated options: `app-store-connect`,
   `destination=export`, manual signing, `Apple Distribution`, team `BGL4WMWD3W`,
   explicit profile UUIDs and no automatic version rewriting. No project/scheme
   build, `xcode-project use-profiles`, dependency script or provisioning-update
   flag participates.
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

Real Apple API authentication, a matching Termitoc distribution certificate,
SOPS decryption, distribution profiles, bundle identifiers, App Group associations
and both ASC records have been verified. Live delivery is established only by a
successful Actions run and its verified TestFlight receipt; exporting or uploading
an IPA alone does not prove it is available to internal testers. Public App Store
review still requires its own metadata and approval. Physical iOS devices are
required to verify VPN traffic. Android CI verifies debug builds; Google Play
publication is not configured here.
