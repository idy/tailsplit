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

    final subscription = status.listen(observe, onError: fail);
    try {
      // A matching event also completes the wait if the snapshot query stalls.
      unawaited(currentStatus().then(observe, onError: fail));
      await reached.future.timeout(timeout);
    } finally {
      await subscription.cancel();
    }
  }
}
