import 'dart:async';

import 'package:sing_box_flutter/sing_box_flutter.dart';

import 'storage.dart';

export 'package:sing_box_flutter/sing_box_flutter.dart'
    show VpnConnectionState, VpnStatus, SingBoxLog;

/// The system VPN, provided by `sing_box_flutter`. Packet processing runs in the
/// native service; closing the UI never stops it.
class VpnService {
  final _plugin = SingBoxFlutter();
  bool _initialized = false;

  Stream<VpnStatus> get status => _plugin.status;
  Stream<SingBoxLog> get logs => _plugin.logs;

  Future<void> initialize() async {
    if (_initialized) return;
    await _plugin.initialize(
      appGroup: AppIdentifiers.appGroup,
      providerBundleIdentifier: AppIdentifiers.packetTunnel,
      notificationTitle: AppIdentifiers.vpnName,
    );
    _initialized = true;
  }

  Future<VpnStatus> currentStatus() => _plugin.getStatus();

  Future<String> coreVersion() => _plugin.getVersion();

  Future<void> check(String config) => _plugin.checkConfig(config);

  Future<bool> requestPermission() => _plugin.requestPermission();

  Future<void> start(String config) =>
      _plugin.start(config: config, name: AppIdentifiers.vpnName);

  /// Neither pinned backend exposes verified OS lockdown. Global must remain
  /// unavailable until the native layer can enforce it across tunnel loss.
  bool get providesSystemKillSwitch => false;

  /// Native command barrier, not just a status hint. The pinned Android worker
  /// orders start before stop, invalidates pending starts and awaits service
  /// destruction. iOS serializes start/stop in its manager command queue.
  /// Once this succeeds AND the OS reports disconnected, earlier start commands
  /// cannot start another tunnel even if their Flutter acknowledgement was lost.
  Future<void> stop() => _plugin.stop();

  /// Completes when the service reports [state]; throws on a status-query
  /// failure, stream error or timeout. Subscribe before querying so a fast
  /// transition cannot fall between the snapshot and the event listener.
  Future<void> waitFor(
    VpnConnectionState state, {
    Duration timeout = const Duration(seconds: 15),
  }) async {
    final reached = Completer<void>();
    void observe(VpnStatus status) {
      if (status.state == state && !reached.isCompleted) reached.complete();
    }
    void fail(Object error, StackTrace stack) {
      if (!reached.isCompleted) reached.completeError(error, stack);
    }

    var receivedEvent = false;
    final subscription = status.listen((status) {
      receivedEvent = true;
      observe(status);
    }, onError: fail);
    try {
      // Events take precedence over a snapshot captured before they arrived.
      // A matching event also completes the wait if the snapshot query stalls.
      unawaited(currentStatus().then((snapshot) {
        if (!receivedEvent) observe(snapshot);
      }, onError: (Object error, StackTrace stack) {
        if (!receivedEvent) fail(error, stack);
      }));
      await reached.future.timeout(timeout);
    } finally {
      await subscription.cancel();
    }
  }
}
