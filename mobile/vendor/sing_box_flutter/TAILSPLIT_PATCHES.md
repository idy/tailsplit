# TailSplit plugin patch provenance

This directory vendors `idy/sing-box-flutter` 0.3.0 from commit
`05872f4558823bc19b5c6767c853504a9cedd544`:
https://github.com/idy/sing-box-flutter/tree/05872f4558823bc19b5c6767c853504a9cedd544

Upstream source, licenses and third-party notices are retained. Generated SDKs,
receipts, downloaded toolchains and build caches are not versioned. TailSplit
uses this local dependency so the native fix is reviewable in the application PR
without changing a shared pub cache or requiring a separate upstream publication.
The sing-box/Tailscale core pins and selected runtime features are unchanged.

## iOS command cancellation

The host plugin's stop method is a priority cancellation barrier. It retires
active validation, initialization, permission and provider-status commands and
cancels queued replies with a fixed `operation_cancelled` error. A command identity
must still be current before an asynchronous continuation can change manager or
status state, save a tunnel configuration, or call `startVPNTunnel`. Repeated stops
coalesce and later ordinary commands cannot overtake a stop barrier.

Validation workers can run independently, so a retired synchronous validation
cannot block fresh work behind one serial queue. Stop independently reloads known
VPN profiles and requests their shutdown, with a five-second `stop_unconfirmed`
failure if discovery does not finish. A successful stop reply is not proof of
OS disconnection; the app also waits for and queries disconnected status.

Permission saves are explicitly passive (on-demand disabled). Their transaction
markers allow a fresh preferences read to recognize a committed save whose callback
was lost. A late callback may settle its transaction ledger, but cannot adopt a
manager, start the VPN, or complete a newer command.

Apple does not expose cancellation of an already submitted consent/save operation:
a passive profile can appear after cancellation. If neither a callback nor a
persisted transaction marker is observable, another conflicting permission save
remains fenced with `preferences_pending`; stop and unrelated commands remain
available. Retired synchronous validation cannot be forcibly terminated and may
continue consuming CPU. No OS kill-switch capability is added by this patch.

Changed native sources are `SingBoxFlutterPlugin.swift`, the new
`NativeCommandQueue.swift`, and the CocoaPods source glob. Android and the public
Dart API remain upstream-compatible.

## Tunnel runtime recovery

- iOS DNS refresh reapplies the complete network settings without first removing
  TUN routes. A failed settings update still reports an actionable tunnel failure.
- The selected core's `CommandServer.Close` cancels its context before waiting for
  startup. Android stop and iOS provider stop can request it on an independent
  cancellation executor; normal teardown remains serialized behind startup.
  Late success cannot publish connected state after stop has been requested.
  The legacy core retains serialized cleanup because its Close API does not have
  the same cancellation guarantee. A native operation that ignores cancellation
  cannot be forcibly destroyed safely, and shutdown is never confirmed early.
- Android's initial network snapshot cannot overwrite a newer registration
  callback. A failed core interface enumeration/lookup clears the previous
  default interface and DNS rather than retaining a stale path.
- Configured core logs must stay below the host's private working root. Validation
  rejects escaping and symlinked paths; opening uses directory-relative descriptors,
  rejects symlinks/non-regular files/hardlinks and applies private permissions.

`tests/test_packet_tunnel_runtime.py` compiles the production provider against inert
OS/core stubs and executes blocked-start cancellation and DNS-setting checks.
`tests/test_android_network_monitor.py` executes production Kotlin network seeding
and cancellation logic with inert callbacks using the compiler cached by Gradle.
It requires Java and skips locally when unavailable; CI sets
`SINGBOX_REQUIRE_ANDROID_RUNTIME_TESTS=1` to make missing prerequisites an error.
Native Go regressions cover cancellation, log containment and interface handover.
These checks do not substitute for device traffic verification.

## Native tool hardening

TailSplit's production CI builds its selected cores from pinned source on fresh
hosted runners; iOS signing uses a separate fresh VM. It does not invoke the
legacy prebuilt installer, selected-core restore helper, or Android WGC smoke
script. The changes below harden those optional tools and locally reused source
caches without changing core pins, feature selections, or runtime behavior.

- `build_libbox.py` never opens cached Git configuration or executes cached hooks,
  filters, filesystem monitors, or pagers. It imports only regular loose/packed
  objects into fresh Git metadata, checks object integrity against the pinned
  commit, and hashes actual tracked file bytes, types and executable modes against
  that tree. Index flags, cached refs, replacement objects, alternates, ignore
  rules and clean filters cannot approve changed source. Unexpected source files
  fail validation. Verified metadata replaces the old `.git` before build helpers
  can invoke Git. Fresh fetches still check the official tag against the commit
  pin; offline caches use that pin rather than mutable local refs.
- `install_libbox.py` requires `--manifest-sha256` for an installation from either
  a release or `--from-dir`. The value must be the independently trusted SHA-256
  of `SHA256SUMS`. `--dry-run` remains usable without a digest. Release tags and
  self-supplied archive/metadata checksums alone do not authenticate binaries.
- `restore_core_artifact.py --from-dir` requires `--manifest-sha256` for reuse,
  binding the complete `artifact-sha256.json` inventory to an approved producer.
  Missing or mismatched trust falls back to the existing source-build path without
  publishing a receipt. `--write-manifest` prints the inventory digest for an
  independently trusted handoff. A digest copied from the same untrusted bundle
  or release is not an independent trust input; approving a producer/run and
  conveying its digest remain the caller's responsibility. No signing service or
  automatic producer approval is introduced. Existing structural, recipe, license
  and ABI checks still apply after the trust check.
- `run_android_wgc_smoke.sh` reuses `bootstrap_go(..., offline=True)` under the
  project lock, rehashing the cached compiler archive against the checked-in lock
  and re-extracting it before execution. An executable plus a forged provenance
  receipt cannot select a compiler. Missing archives require a native build first.
  Each emulator launch uses a private AVD directory and unique guest property;
  device writes require that identity and a live launched process. An existing
  `emulator-5554` is refused. Cleanup signals only the child it launched, never an
  ADB serial, including failed launches and port collisions.

Uninitialized client submodule entries in the pinned upstream tree are allowed as
absent or empty directories; populated or symlinked submodule paths are rejected.
The Android library and example explicitly apply the external Kotlin plugin,
matching their disabled AGP built-in Kotlin setting.

The focused Python suites use temporary Git repositories, forged fixture bundles,
compiler archives and inert SDK executables. They require no real SDK caches,
credentials, downloads, releases, device access or native compilation:

```bash
python3 -I -B -m unittest discover -s vendor/sing_box_flutter/tests -p test_native_tools.py
python3 -I -B -m unittest discover -s vendor/sing_box_flutter/tests -p test_restore_core_artifact.py
```

These changes preserve source-build behavior but change the build-tool recipe
fingerprint, so previously selected native artifacts must be rebuilt. Full device
compilation and a real Linux/KVM emulator smoke run remain separate checks.

## Checks

Run the actual host-plugin source against inert OS/channel stubs on macOS:

```bash
python3 -I vendor/sing_box_flutter/tests/test_ios_cancellation.py
```

The scenarios cover stalled operations, priority/coalesced stop, late callbacks,
fresh command progress, lost preference callbacks, pending saves, and failed stop
reconciliation. They never start a real VPN or access credentials. The application
simulator/device builds compile against the real Apple and Flutter frameworks;
physical-device network behavior remains a separate verification requirement.
