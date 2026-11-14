import 'dart:async';
import 'dart:io';

import 'package:flutter_test/flutter_test.dart';
import 'package:tailsplit_mobile/src/model/exit_profile.dart';
import 'package:tailsplit_mobile/src/model/settings.dart';
import 'package:tailsplit_mobile/src/services/storage.dart';
import 'package:tailsplit_mobile/src/services/vpn.dart';
import 'package:tailsplit_mobile/src/state/app_controller.dart';
import 'package:tailsplit_mobile/src/tailscale/tailscale.dart';

class ControlledVpn extends VpnService {
  final events = StreamController<VpnStatus>.broadcast(sync: true);
  final startEntered = Completer<void>();
  Completer<void>? startGate;
  Completer<void>? stopGate;
  Object? stopError;
  bool lingerOnStop = false;
  bool failFirstInitialization = false;
  int initializations = 0;
  int _nativeEpoch = 0;
  int starts = 0;
  int stops = 0;
  VpnConnectionState state = VpnConnectionState.disconnected;

  void emit(VpnConnectionState next) {
    state = next;
    events.add(VpnStatus(state: next));
  }

  @override
  Stream<VpnStatus> get status => events.stream;
  @override
  Stream<SingBoxLog> get logs => const Stream.empty();
  @override
  Future<void> initialize() async {
    if (initializations++ == 0 && failFirstInitialization) {
      throw StateError('initialization cancelled');
    }
  }
  @override
  Future<String> coreVersion() async => 'test';
  @override
  Future<VpnStatus> currentStatus() async => VpnStatus(state: state);
  @override
  Future<void> check(String config) async {}
  @override
  Future<bool> requestPermission() async => true;
  @override
  Future<void> start(String config) async {
    starts++;
    final epoch = ++_nativeEpoch;
    if (!startEntered.isCompleted) startEntered.complete();
    await startGate?.future;
    // Model the pinned native stop barrier: an older pending start cannot take
    // effect after stop, even if its method-channel reply is delivered late.
    if (epoch == _nativeEpoch) emit(VpnConnectionState.connected);
  }
  @override
  Future<void> stop() async {
    stops++;
    if (stopError != null) throw stopError!;
    final epoch = ++_nativeEpoch;
    await stopGate?.future;
    if (epoch == _nativeEpoch) {
      emit(lingerOnStop ? VpnConnectionState.disconnecting : VpnConnectionState.disconnected);
    }
  }
  @override
  Future<void> waitFor(VpnConnectionState state, {Duration timeout = const Duration(seconds: 15)}) =>
      super.waitFor(state, timeout: const Duration(milliseconds: 40));
}

class LifecycleClient implements TailscaleControlClient {
  LifecycleClient(this.snapshot);
  final TailnetSnapshot snapshot;
  int logouts = 0;

  @override
  Future<TailnetSnapshot> fetchSnapshot(TailscaleIdentity identity) async => snapshot;
  @override
  Future<void> logout(TailscaleIdentity identity) async { logouts++; }
  @override
  Future<void> close() async {}
  @override
  dynamic noSuchMethod(Invocation invocation) => super.noSuchMethod(invocation);
}

const node = TailnetPeer(stableId: 'one', name: 'one', fqdn: 'one.test.ts.net',
    addresses: ['100.64.0.1'], online: true, isExitNode: true);
const otherNode = TailnetPeer(stableId: 'two', name: 'two', fqdn: 'two.test.ts.net',
    addresses: ['100.64.0.2'], online: true, isExitNode: true);

Future<({AppController app, ControlledVpn vpn, LifecycleClient client, Directory root})> fixture({
  RoutingMode mode = RoutingMode.split,
  bool restoredConnection = false,
  bool failFirstInitialization = false,
}) async {
  final root = await Directory.systemTemp.createTemp('tailsplit-lifecycle');
  final snapshot = TailnetSnapshot(selfName: 'phone', selfFqdn: 'phone.test.ts.net',
      selfAddresses: const ['100.64.0.3'], networkName: 'test', magicDnsSuffix: 'test.ts.net',
      userLoginName: 'test@example.com', userDisplayName: 'Test', stableNodeId: 'phone',
      exitNodes: const [node, otherNode], fetchedAt: DateTime.now());
  final client = LifecycleClient(snapshot);
  final vpn = ControlledVpn()..failFirstInitialization = failFirstInitialization;
  if (restoredConnection) vpn.state = VpnConnectionState.connected;
  final app = AppController(paths: AppPaths.at(root, root), vpn: vpn, controlClient: () => client,
      nativeCommandTimeout: const Duration(milliseconds: 100));
  app.settings = MobileSettings(source: RuleSource.none, mode: mode, vpnDisclosureAccepted: true);
  app.exit = ExitProfile(identity: await TailscaleIdentity.generate(hostname: 'phone'),
      snapshot: snapshot, nodeId: node.stableId, nodeName: node.name, nodeAddress: node.addresses.first);
  addTearDown(() async {
    app.dispose();
    await vpn.events.close();
    await root.delete(recursive: true);
  });
  final refreshed = Completer<void>();
  void listener() {
    if (!app.refreshing && !refreshed.isCompleted) refreshed.complete();
  }
  await app.load();
  if (app.refreshing) {
    app.addListener(listener);
    await refreshed.future.timeout(const Duration(seconds: 5));
    app.removeListener(listener);
  }
  return (app: app, vpn: vpn, client: client, root: root);
}

void main() {
  TestWidgetsFlutterBinding.ensureInitialized();

  test('connect can retry cancelled initialization without losing native status events', () async {
    final f = await fixture(failFirstInitialization: true);
    expect(f.vpn.initializations, 1);
    await f.app.connect();
    expect(f.vpn.initializations, 2);
    expect(f.app.connected, isTrue);
    expect(f.app.problem, isNull);
  });

  test('cancel waits for an in-flight start then leaves the native service stopped', () async {
    final f = await fixture();
    f.vpn.startGate = Completer<void>();
    final connecting = f.app.connect();
    await f.vpn.startEntered.future.timeout(const Duration(seconds: 5));
    final stopping = f.app.disconnect();
    await pumpEventQueue();
    expect(f.vpn.stops, 0, reason: 'Stop cannot overtake the pending native start.');
    f.vpn.startGate!.complete();
    await connecting;
    expect(await stopping, isTrue);
    expect(f.vpn.state, VpnConnectionState.disconnected);
    expect(f.app.vpnState, VpnConnectionState.disconnected);
    expect(f.app.preparing, isFalse);
  });

  test('removal waits for pending start shutdown before logging out or deleting state', () async {
    final f = await fixture();
    f.vpn.startGate = Completer<void>();
    final connecting = f.app.connect();
    await f.vpn.startEntered.future.timeout(const Duration(seconds: 5));
    final state = File('${f.root.path}/tailscale/tailscaled.state');
    expect(await state.exists(), isTrue);
    final removing = f.app.removeExit();
    await pumpEventQueue();
    expect(f.client.logouts, 0);
    expect(await state.exists(), isTrue);
    f.vpn.startGate!.complete();
    await connecting;
    expect(await removing, isTrue);
    expect(f.vpn.state, VpnConnectionState.disconnected);
    expect(f.client.logouts, 1);
    expect(await state.exists(), isFalse);
    expect(f.app.exit, isNull);
  });

  test('a stop failure retains the exit and identity and blocks reconnect', () async {
    final f = await fixture();
    await f.app.connect();
    final state = File('${f.root.path}/tailscale/tailscaled.state');
    final original = await state.readAsString();
    f.vpn.stopError = StateError('native stop failed');
    expect(await f.app.removeExit(), isFalse);
    expect(f.client.logouts, 0);
    expect(f.app.exit!.nodeId, node.stableId);
    expect(await state.readAsString(), original);
    expect(f.app.problem, AppProblem.disconnect);
    await f.app.reconnect();
    expect(f.vpn.starts, 1);
    expect(f.app.needsReconnect, isTrue);
  });

  test('disconnecting timeout never permits identity removal or another start', () async {
    final f = await fixture();
    await f.app.connect();
    f.vpn.lingerOnStop = true;
    expect(await f.app.removeExit(), isFalse);
    expect(f.app.vpnState, VpnConnectionState.disconnecting);
    expect(f.client.logouts, 0);
    expect(f.app.exit, isNotNull);
    await f.app.connect();
    await f.app.reconnect();
    expect(f.vpn.starts, 1);
    expect(f.app.needsReconnect, isTrue);
  });

  test('failed forced stop blocks starts despite stale disconnected status and dismissed error', () async {
    final f = await fixture();
    f.vpn.stopError = StateError('native stop failed');
    expect(await f.app.disconnect(force: true), isFalse);
    expect(f.app.vpnState, VpnConnectionState.disconnected);
    f.app.dismissProblem();
    f.vpn.emit(VpnConnectionState.disconnected);
    expect(f.app.canConnect, isFalse);
    await f.app.connect();
    expect(f.vpn.starts, 0);
    f.vpn.stopError = null;
    expect(await f.app.disconnect(), isTrue);
    expect(f.app.canConnect, isTrue);
    await f.app.connect();
    expect(f.vpn.starts, 1);
  });

  test('confirmed stop releases a start whose acknowledgement never completes', () async {
    final f = await fixture();
    f.vpn.startGate = Completer<void>();
    final connecting = f.app.connect();
    await f.vpn.startEntered.future;
    expect(await f.app.disconnect().timeout(const Duration(seconds: 2)), isTrue);
    await connecting.timeout(const Duration(seconds: 2));
    expect(f.vpn.startGate!.isCompleted, isFalse);
    expect(f.app.preparing, isFalse);
    expect(f.app.canConnect, isTrue);
    expect(await f.app.removeExit(), isTrue);
    expect(f.client.logouts, 1);
    expect(f.app.exit, isNull);
  });

  test('a retired late start acknowledgement cannot stop a replacement tunnel', () async {
    final f = await fixture();
    final oldReply = Completer<void>();
    f.vpn.startGate = oldReply;
    await f.app.connect().timeout(const Duration(seconds: 2));
    expect(f.app.canConnect, isTrue);
    f.vpn.startGate = null;
    await f.app.connect();
    expect(f.vpn.starts, 2);
    final stops = f.vpn.stops;
    oldReply.complete();
    await pumpEventQueue();
    expect(f.vpn.stops, stops);
    expect(f.vpn.state, VpnConnectionState.connected);
    expect(f.app.connected, isTrue);
  });

  test('a failed barrier retains identity until a successful stop retry', () async {
    final f = await fixture();
    final oldReply = Completer<void>();
    f.vpn.startGate = oldReply;
    f.vpn.stopError = StateError('stop failed');
    await f.app.connect().timeout(const Duration(seconds: 2));
    expect(f.app.canConnect, isFalse);
    expect(await f.app.removeExit(), isFalse);
    expect(f.client.logouts, 0);
    f.vpn.stopError = null;
    expect(await f.app.disconnect(force: true), isTrue);
    expect(oldReply.isCompleted, isFalse);
    expect(await f.app.removeExit(), isTrue);
    oldReply.complete();
    await pumpEventQueue();
    expect(f.vpn.state, VpnConnectionState.disconnected);
    expect(f.app.exit, isNull);
  });

  test('a late unretired start still receives compensating shutdown', () async {
    final f = await fixture();
    f.vpn.startGate = Completer<void>();
    f.vpn.stopError = StateError('stop failed');
    await f.app.connect().timeout(const Duration(seconds: 2));
    expect(f.app.canConnect, isFalse);
    f.vpn.stopError = null;
    f.vpn.startGate!.complete();
    await pumpEventQueue();
    expect(f.vpn.state, VpnConnectionState.disconnected);
    expect(f.app.canConnect, isTrue);
  });

  test('a retired late start failure is consumed without disturbing a new tunnel', () async {
    final f = await fixture();
    final oldReply = Completer<void>();
    f.vpn.startGate = oldReply;
    await f.app.connect().timeout(const Duration(seconds: 2));
    f.vpn.startGate = null;
    await f.app.connect();
    oldReply.completeError(StateError('late start failure'));
    await pumpEventQueue();
    expect(f.app.problem, isNull);
    expect(f.vpn.state, VpnConnectionState.connected);
  });

  test('a newer stop barrier recovers a lost stop reply without affecting the next tunnel', () async {
    final f = await fixture();
    await f.app.connect();
    final oldReply = Completer<void>();
    f.vpn.stopGate = oldReply;
    expect(await f.app.removeExit().timeout(const Duration(seconds: 2)), isFalse);
    expect(f.app.canRetryDisconnect, isTrue);
    f.vpn.stopGate = null;
    expect(await f.app.disconnect(force: true), isTrue);
    await f.app.connect();
    expect(f.vpn.starts, 2);
    expect(f.client.logouts, 0);
    oldReply.complete();
    await pumpEventQueue();
    expect(f.vpn.state, VpnConnectionState.connected);
  });

  test('saved Global routing can connect without an OS kill-switch capability', () async {
    final f = await fixture(mode: RoutingMode.global);
    expect(f.app.canConnect, isTrue);
    await f.app.connect();
    expect(f.vpn.starts, 1);
    expect(f.app.settings.mode, RoutingMode.global);
    expect(f.app.connected, isTrue);
    expect(f.app.problem, isNull);
  });

  test('selecting Global from Split applies routing after confirmed shutdown', () async {
    final f = await fixture();
    await f.app.connect();
    await f.app.setRoutingMode(RoutingMode.global);
    expect(f.app.settings.mode, RoutingMode.global);
    expect(f.vpn.starts, 2);
    expect(f.vpn.stops, 1);
    expect(f.app.connected, isTrue);
    expect(f.app.problem, isNull);
  });

  test('Global settings remain usable while idle', () async {
    final f = await fixture();
    await f.app.updateSettings(f.app.settings.copyWith(mode: RoutingMode.global));
    expect(f.app.settings.mode, RoutingMode.global);
    expect(f.app.canConnect, isTrue);
    await f.app.connect();
    expect(f.vpn.starts, 1);
    expect(f.app.problem, isNull);
  });

  test('Global node and mode applies keep protection until explicit consent', () async {
    final f = await fixture(mode: RoutingMode.global);
    await f.app.connect();
    expect(f.app.reconnectRequiresConsent, isTrue);
    expect(await f.app.confirmNode(otherNode), isTrue);
    await pumpEventQueue();
    expect(f.vpn.stops, 0);
    expect(f.app.needsReconnect, isTrue);
    await f.app.setRoutingMode(RoutingMode.split);
    expect(f.vpn.stops, 0, reason: 'The running Global mode wins over the newly saved mode.');
    expect(f.app.reconnectRequiresConsent, isTrue);
    await f.app.reconnect(allowProtectionGap: true);
    expect(f.vpn.stops, 1);
    expect(f.vpn.starts, 2);
    expect(f.app.reconnectRequiresConsent, isFalse);
    expect(f.app.needsReconnect, isFalse);
  });

  test('Split applies still reconnect automatically after a confirmed stop', () async {
    final f = await fixture();
    await f.app.connect();
    await f.app.setRoutingMode(RoutingMode.global);
    expect(f.vpn.stops, 1);
    expect(f.vpn.starts, 2);
    expect(f.app.reconnectRequiresConsent, isTrue);
  });

  test('unexpected Global tunnel loss reports normal-network exposure', () async {
    final f = await fixture(mode: RoutingMode.global);
    await f.app.connect();
    f.vpn.emit(VpnConnectionState.disconnecting);
    f.vpn.emit(VpnConnectionState.disconnected);
    expect(f.app.problem, AppProblem.tunnelStopped);
    expect(f.app.settings.mode, RoutingMode.global);
    expect(f.vpn.starts, 1, reason: 'A UI warning must not claim a protective restart.');
    await f.app.connect();
    expect(f.app.problem, isNull);
    f.vpn.emit(VpnConnectionState.error);
    expect(f.app.problem, AppProblem.tunnelStopped);
  });

  test('a deliberate stop does not show an unexpected-loss warning', () async {
    final f = await fixture(mode: RoutingMode.global);
    await f.app.connect();
    expect(await f.app.disconnect(), isTrue);
    expect(f.app.problem, isNull);
  });

  test('unexpected loss of a restored unknown-mode tunnel also warns', () async {
    final f = await fixture(restoredConnection: true);
    f.vpn.emit(VpnConnectionState.disconnecting);
    f.vpn.emit(VpnConnectionState.disconnected);
    expect(f.app.problem, AppProblem.tunnelStopped);
  });

  test('restored connections with unknown runtime mode require consent', () async {
    final f = await fixture(restoredConnection: true);
    await f.app.updateSettings(f.app.settings.copyWith(fakeIp: false), applyNow: true);
    expect(f.vpn.stops, 0);
    expect(f.app.needsReconnect, isTrue);
    expect(f.app.reconnectRequiresConsent, isTrue);
  });
}
