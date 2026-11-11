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

  /// Completes once the service reports [state], or after [timeout].
  Future<void> waitFor(
    VpnConnectionState state, {
    Duration timeout = const Duration(seconds: 15),
  }) async {
    final current = await currentStatus();
    if (current.state == state) return;
    await status
        .firstWhere((event) => event.state == state)
        .timeout(timeout, onTimeout: () => current);
  }
}
