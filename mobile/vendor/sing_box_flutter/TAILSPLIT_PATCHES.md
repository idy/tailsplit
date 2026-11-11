# TailSplit plugin patch provenance

This directory vendors `idy/sing-box-flutter` 0.3.0 from commit
`05872f4558823bc19b5c6767c853504a9cedd544`:
https://github.com/idy/sing-box-flutter/tree/05872f4558823bc19b5c6767c853504a9cedd544

Upstream source, licenses and third-party notices are retained. Generated SDKs,
receipts, downloaded toolchains and build caches are not versioned. TailSplit
uses this local dependency so the native fix is reviewable in the application PR
without changing a shared pub cache or requiring a separate upstream publication.
The sing-box/Tailscale core pins and selected-core build recipe are unchanged.

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
