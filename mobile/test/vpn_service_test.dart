import 'dart:async';

import 'package:flutter_test/flutter_test.dart';
import 'package:tailsplit_mobile/src/services/vpn.dart';

class StatusVpn extends VpnService {
  final updates = StreamController<VpnStatus>.broadcast(sync: true);
  late Future<VpnStatus> Function() readSnapshot;

  @override
  Stream<VpnStatus> get status => updates.stream;

  @override
  Future<VpnStatus> currentStatus() => readSnapshot();
}

void main() {
  late StatusVpn vpn;
  const connected = VpnStatus(state: VpnConnectionState.connected);
  const disconnected = VpnStatus(state: VpnConnectionState.disconnected);

  setUp(() => vpn = StatusVpn());
  tearDown(() => vpn.updates.close());

  test('already disconnected completes and releases its subscription', () async {
    vpn.readSnapshot = () async => disconnected;
    await vpn.waitFor(VpnConnectionState.disconnected);
    expect(vpn.updates.hasListener, isFalse);
  });

  test('a fast disconnect is observed even when the query returns a stale state', () async {
    vpn.readSnapshot = () async {
      vpn.updates.add(disconnected);
      return connected;
    };
    var completed = false;
    final waiting = vpn.waitFor(VpnConnectionState.disconnected)
        .then((_) => completed = true);
    await pumpEventQueue();
    expect(completed, isTrue, reason: 'The event must not be lost before subscribing.');
    await waiting;
    expect(vpn.updates.hasListener, isFalse);
  });

  test('a matching event can finish while the snapshot query is pending', () async {
    final snapshot = Completer<VpnStatus>();
    vpn.readSnapshot = () => snapshot.future;
    final waiting = vpn.waitFor(VpnConnectionState.disconnected);
    vpn.updates.add(disconnected);
    await waiting.timeout(const Duration(seconds: 1));
    expect(vpn.updates.hasListener, isFalse);
    // A late snapshot failure must not become an unhandled error.
    snapshot.completeError(StateError('late status query failure'));
    await pumpEventQueue();
  });

  test('a late disconnected snapshot cannot override a newer transition event', () async {
    final snapshot = Completer<VpnStatus>();
    vpn.readSnapshot = () => snapshot.future;
    var completed = false;
    final waiting = vpn.waitFor(VpnConnectionState.disconnected)
        .then((_) => completed = true);
    vpn.updates.add(const VpnStatus(state: VpnConnectionState.disconnecting));
    snapshot.complete(disconnected);
    await pumpEventQueue();
    expect(completed, isFalse);
    vpn.updates.add(disconnected);
    await waiting;
    expect(vpn.updates.hasListener, isFalse);
  });

  test('a newer stream state supersedes a failed snapshot query', () async {
    final snapshot = Completer<VpnStatus>();
    vpn.readSnapshot = () => snapshot.future;
    final waiting = vpn.waitFor(VpnConnectionState.disconnected);
    vpn.updates.add(connected);
    snapshot.completeError(StateError('superseded query'));
    await pumpEventQueue();
    vpn.updates.add(disconnected);
    await waiting;
    expect(vpn.updates.hasListener, isFalse);
  });

  test('nonmatching events do not finish the wait', () async {
    vpn.readSnapshot = () async => connected;
    var completed = false;
    final waiting = vpn.waitFor(VpnConnectionState.disconnected)
        .then((_) => completed = true);
    vpn.updates.add(const VpnStatus(state: VpnConnectionState.disconnecting));
    await pumpEventQueue();
    expect(completed, isFalse);
    vpn.updates.add(disconnected);
    await waiting;
    expect(completed, isTrue);
  });

  test('timeout fails explicitly and cancels the listener even if query hangs', () async {
    final snapshot = Completer<VpnStatus>();
    vpn.readSnapshot = () => snapshot.future;
    await expectLater(
      vpn.waitFor(VpnConnectionState.disconnected, timeout: const Duration(milliseconds: 20)),
      throwsA(isA<TimeoutException>()),
    );
    expect(vpn.updates.hasListener, isFalse);
    snapshot.complete(connected);
    await pumpEventQueue();
  });

  test('snapshot and stream failures propagate with listener cleanup', () async {
    final queryError = StateError('status unavailable');
    vpn.readSnapshot = () => Future.error(queryError);
    await expectLater(vpn.waitFor(VpnConnectionState.disconnected), throwsA(same(queryError)));
    expect(vpn.updates.hasListener, isFalse);

    vpn.readSnapshot = () async => connected;
    final streamError = StateError('events unavailable');
    final waiting = expectLater(
      vpn.waitFor(VpnConnectionState.disconnected), throwsA(same(streamError)),
    );
    vpn.updates.addError(streamError);
    await waiting;
    expect(vpn.updates.hasListener, isFalse);
  });
}
