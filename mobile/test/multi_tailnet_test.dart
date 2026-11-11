import 'dart:async';
import 'dart:convert';
import 'dart:io';

import 'package:flutter_test/flutter_test.dart';
import 'package:tailsplit_mobile/src/model/exit_profile.dart';
import 'package:tailsplit_mobile/src/model/settings.dart';
import 'package:tailsplit_mobile/src/model/tailnet_account.dart';
import 'package:tailsplit_mobile/src/services/storage.dart';
import 'package:tailsplit_mobile/src/services/vpn.dart';
import 'package:tailsplit_mobile/src/state/app_controller.dart';
import 'package:tailsplit_mobile/src/state/preview.dart';
import 'package:tailsplit_mobile/src/tailscale/tailscale.dart';
import 'package:tailsplit_mobile/src/tailscale/models.dart' show TailnetDiscoveryDiagnostics;

import 'vpn_lifecycle_review_test.dart' show ControlledVpn;

TailnetSnapshot snapshot(String network, {String? name, DateTime? at}) => TailnetSnapshot(
      selfName: 'phone', selfFqdn: 'phone.$network.ts.net',
      selfAddresses: const ['100.64.0.3'], networkName: name ?? network,
      magicDnsSuffix: '$network.ts.net', userLoginName: 'user@$network.example',
      userDisplayName: 'User', stableNodeId: 'phone-$network',
      exitNodes: [TailnetPeer(stableId: 'exit-$network', name: 'exit-$network',
          fqdn: 'exit.$network.ts.net', addresses: ['100.64.0.${network == 'a' ? 1 : 2}'],
          online: true, isExitNode: true)],
      fetchedAt: at ?? DateTime.now(),
      discoveryDiagnostics: const TailnetDiscoveryDiagnostics(peerCount: 1, exitRoutePeerCount: 1,
          advertisedExitRoutePeerCount: 1, primaryExitRoutePeerCount: 0, missingAllowedIpsPeerCount: 0,
          changedPeerCount: 0, peerListPresent: true),
    );

class AccountVpn extends ControlledVpn {
  final configs = <Map<String, dynamic>>[];
  @override
  Future<void> start(String config) async {
    configs.add(jsonDecode(config) as Map<String, dynamic>);
    await super.start(config);
  }
}

class AccountClient implements TailscaleControlClient {
  TailnetSnapshot loginSnapshot = snapshot('a');
  final snapshots = <String, TailnetSnapshot>{};
  final logins = <TailscaleIdentity>[];
  final logouts = <TailscaleIdentity>[];
  Future<TailnetSnapshot> Function(TailscaleIdentity)? fetch;
  Stream<LoginProgress>? loginEvents;

  @override
  Stream<LoginProgress> login(TailscaleIdentity identity) {
    logins.add(identity);
    snapshots[identity.machinePrivateKey] = loginSnapshot;
    return loginEvents ?? Stream.value(LoginAuthorized(loginSnapshot, identity: identity));
  }
  @override
  Future<TailnetSnapshot> fetchSnapshot(TailscaleIdentity identity) async =>
      fetch != null ? await fetch!(identity) : snapshots[identity.machinePrivateKey]!;
  @override
  Future<void> logout(TailscaleIdentity identity) async => logouts.add(identity);
  @override
  Future<void> close() async {}
  @override
  dynamic noSuchMethod(Invocation invocation) => super.noSuchMethod(invocation);
}

class Fixture {
  Fixture(this.root) : paths = AppPaths.at(Directory('${root.path}/app'), Directory('${root.path}/shared'));
  final Directory root;
  final AppPaths paths;
  final vpn = AccountVpn();
  final client = AccountClient();
  late final app = AppController(paths: paths, vpn: vpn, controlClient: () => client,
      nativeCommandTimeout: const Duration(milliseconds: 200));

  Future<void> initialize() async {
    await app.load();
    app.settings = const MobileSettings(source: RuleSource.none);
  }

  Future<String> add(String network, {String? name}) async {
    client.loginSnapshot = snapshot(network, name: name);
    await app.signInExit();
    await idle();
    expect(app.problem, isNull);
    return app.pickerRequestedTailnetId!;
  }

  Future<void> idle() async {
    if (app.signIn == SignInPhase.idle) return;
    final done = Completer<void>();
    void listener() {
      if (app.signIn == SignInPhase.idle && !done.isCompleted) done.complete();
    }
    app.addListener(listener);
    try {
      await done.future.timeout(const Duration(seconds: 5));
    } finally {
      app.removeListener(listener);
    }
  }

  Future<void> choose(String id) async {
    expect(await app.confirmNode(app.tailnetById(id)!.snapshot!.exitNodes.first, tailnetId: id), isTrue);
  }

  Future<AppController> reload({bool active = false}) async {
    final otherVpn = AccountVpn();
    if (active) otherVpn.state = VpnConnectionState.connected;
    final other = AppController(paths: paths, vpn: otherVpn, controlClient: () => client);
    addTearDown(() async { other.dispose(); await otherVpn.events.close(); });
    await other.load();
    // Loading can start bounded fake refreshes; settle them before teardown.
    for (var i = 0; other.refreshing && i < 100; i++) {
      await Future<void>.delayed(const Duration(milliseconds: 5));
    }
    return other;
  }
}

Future<Fixture> fixture({bool load = true}) async {
  final f = Fixture(await Directory.systemTemp.createTemp('tailsplit-accounts'));
  addTearDown(() async {
    f.app.dispose();
    await f.vpn.events.close();
    await f.root.delete(recursive: true);
  });
  if (load) await f.initialize();
  return f;
}

void main() {
  TestWidgetsFlutterBinding.ensureInitialized();

  test('migration preserves the chosen node and native files in place while VPN is live', () async {
    final f = await fixture(load: false);
    final identity = await TailscaleIdentity.generate(hostname: 'phone');
    final snap = snapshot('a');
    final profile = ExitProfile(identity: identity, snapshot: snap, nodeId: 'exit-a',
        nodeName: 'exit-a', nodeAddress: '100.64.0.1', label: 'Custom', autoName: false);
    await JsonStore.write(f.paths.exitFile, profile.toJson());
    await TsnetState.write(stateDirectory: f.paths.tailscaleState, identity: identity, snapshot: snap);
    final native = File('${f.paths.tailscaleState.path}/${TsnetState.stateFileName}');
    final before = await native.readAsBytes();
    final modified = await native.lastModified();
    await File(f.paths.cacheFile).writeAsString('legacy cache');
    f.vpn.state = VpnConnectionState.connected;
    await f.initialize();
    final account = f.app.tailnets.single;
    expect(account.usesLegacyState, isTrue);
    expect(f.paths.tailscaleStateFor(account).path, f.paths.tailscaleState.path);
    expect(f.paths.cacheFileFor(account), f.paths.cacheFile);
    expect(f.app.selectedTailnetId, account.id);
    expect(f.app.exit!.nodeId, 'exit-a');
    expect(f.app.exit!.label, 'Custom');
    expect(await native.readAsBytes(), before);
    expect(await native.lastModified(), modified);
    expect(await File(f.paths.cacheFile).readAsString(), 'legacy cache');
    expect(f.vpn.stops, 0);
    final reloaded = await f.reload(active: true);
    expect(reloaded.tailnets.single.id, account.id);
    expect(reloaded.tailnets.single.identity.machinePrivateKey, identity.machinePrivateKey);
  });

  test('adding accounts preserves first identity and selected active Global exit', () async {
    final f = await fixture();
    final a = await f.add('a');
    expect(f.app.exit, isNull);
    expect(f.app.canConnect, isFalse, reason: 'Sign-in alone never chooses a node.');
    await f.choose(a);
    await f.app.updateSettings(f.app.settings.copyWith(mode: RoutingMode.global));
    await f.app.connect();
    final identity = f.app.tailnetById(a)!.identity;
    final state = File('${f.paths.tailscaleStateFor(f.app.tailnetById(a)!).path}/${TsnetState.stateFileName}');
    final before = await state.readAsString();
    final b = await f.add('b');
    expect(f.app.tailnets.length, 2);
    expect(f.app.tailnetById(a)!.identity, same(identity));
    expect(f.app.tailnetById(b)!.identity.machinePrivateKey, isNot(identity.machinePrivateKey));
    expect(f.app.selectedTailnetId, a);
    expect(f.app.exit!.nodeId, 'exit-a');
    expect(f.app.pickerRequestedTailnetId, b);
    expect(f.app.connected, isTrue);
    expect(f.vpn.stops, 0);
    expect(await state.readAsString(), before);
    f.app.pickerRequested = false;
    expect(f.app.pickerRequestedTailnetId, isNull);
  });

  test('switching tailnets changes only global selection and uses isolated state cache and DNS', () async {
    final f = await fixture();
    final a = await f.add('a');
    final b = await f.add('b');
    final firstAccount = f.app.tailnetById(a)!;
    await f.choose(a);
    await f.app.connect();
    expect(await f.app.disconnect(), isTrue);
    await f.choose(b);
    await f.app.connect();
    expect(f.app.tailnets.length, 2);
    expect(f.app.tailnetById(a), same(firstAccount));
    expect(f.app.selectedTailnetId, b);
    expect(f.app.exit!.nodeId, 'exit-b');
    expect(f.vpn.configs.length, 2);
    for (final (i, id) in [a, b].indexed) {
      final account = f.app.tailnetById(id)!;
      final config = f.vpn.configs[i];
      final endpoint = (config['endpoints'] as List).single as Map;
      expect(endpoint['state_directory'], f.paths.tailscaleStateFor(account).path);
      expect(endpoint['exit_node'], account.snapshot!.exitNodes.single.addresses.single);
      expect((config['experimental'] as Map)['cache_file']['path'], f.paths.cacheFileFor(account));
      final dnsRules = ((config['dns'] as Map)['rules'] as List).cast<Map>();
      expect(dnsRules.any((rule) => (rule['domain_suffix'] as List?)?.contains(account.snapshot!.magicDnsSuffix) == true), isTrue);
    }
    expect(f.paths.cacheFileFor(firstAccount), isNot(f.paths.cacheFileFor(f.app.tailnetById(b)!)));
  });

  test('same display name does not merge tailnets; duplicate stable network preserves identity', () async {
    final f = await fixture();
    final a = await f.add('a', name: 'Shared name');
    await f.choose(a);
    final original = f.app.tailnetById(a)!;
    final b = await f.add('b', name: 'Shared name');
    expect(b, isNot(a));
    final duplicate = await f.add('a', name: 'Renamed');
    expect(duplicate, a);
    expect(f.app.tailnets.length, 2);
    expect(f.app.tailnetById(a), same(original));
    expect(f.app.selectedTailnetId, a);
    expect(f.client.logouts.length, 1, reason: 'Only the extra newly registered device is signed out.');
    expect(f.client.logouts.single.machinePrivateKey, isNot(original.identity.machinePrivateKey));
  });

  test('explicit reauthentication targets the nonselected account and keeps selection', () async {
    final f = await fixture();
    final a = await f.add('a');
    final b = await f.add('b');
    await f.choose(b);
    await f.app.connect();
    final bIdentity = f.app.tailnetById(b)!.identity;
    final aIdentity = f.app.tailnetById(a)!.identity;
    f.client.loginSnapshot = snapshot('a', name: 'New A name');
    await f.app.signInExit(tailnetId: a);
    await f.idle();
    expect(f.client.logins.last.machinePrivateKey, aIdentity.machinePrivateKey);
    expect(f.app.tailnetById(a)!.networkName, 'New A name');
    expect(f.app.tailnetById(b)!.identity, same(bIdentity));
    expect(f.app.selectedTailnetId, b);
    expect(f.app.connected, isTrue);
    expect(f.vpn.stops, 0);
  });

  test('reauthentication into a different network refuses to overwrite the old account', () async {
    final f = await fixture();
    final a = await f.add('a');
    final original = f.app.tailnetById(a)!;
    f.client.loginSnapshot = snapshot('b');
    await f.app.signInExit(tailnetId: a);
    await f.idle();
    expect(f.app.problem, AppProblem.signIn);
    expect(f.app.problemTailnetId, a);
    expect(f.app.tailnets.length, 1);
    expect(f.app.tailnetById(a), same(original));
  });

  test('delayed refresh remains scoped to its account after global selection changes', () async {
    final f = await fixture();
    final a = await f.add('a');
    final b = await f.add('b');
    await f.choose(a);
    final response = Completer<TailnetSnapshot>();
    final entered = Completer<void>();
    f.client.fetch = (_) { entered.complete(); return response.future; };
    final refreshing = f.app.refresh(tailnetId: a);
    await entered.future;
    await f.choose(b);
    response.complete(snapshot('a', name: 'Updated A'));
    expect(await refreshing, isTrue);
    expect(f.app.tailnetById(a)!.networkName, 'Updated A');
    expect(f.app.tailnetById(b)!.networkName, 'b');
    expect(f.app.exit!.networkName, 'b');
    expect(f.app.selectedTailnetId, b);
  });

  test('concurrent refreshes serialize store updates without dropping either snapshot', () async {
    final f = await fixture();
    final a = await f.add('a');
    final b = await f.add('b');
    final responseA = Completer<TailnetSnapshot>();
    final responseB = Completer<TailnetSnapshot>();
    f.client.fetch = (identity) => identity.machinePrivateKey == f.app.tailnetById(a)!.identity.machinePrivateKey
        ? responseA.future : responseB.future;
    final refreshA = f.app.refresh(tailnetId: a);
    final refreshB = f.app.refresh(tailnetId: b);
    responseB.complete(snapshot('b', name: 'Updated B'));
    responseA.complete(snapshot('a', name: 'Updated A'));
    expect(await refreshA, isTrue);
    expect(await refreshB, isTrue);
    final store = TailnetStore.fromJson((await JsonStore.read(f.paths.accountsFile))!);
    expect(store.accounts[a]!.networkName, 'Updated A');
    expect(store.accounts[b]!.networkName, 'Updated B');
  });

  test('removed account cannot be resurrected by a delayed refresh', () async {
    final f = await fixture();
    final a = await f.add('a');
    final b = await f.add('b');
    await f.choose(b);
    final response = Completer<TailnetSnapshot>();
    final entered = Completer<void>();
    f.client.fetch = (_) { entered.complete(); return response.future; };
    final refreshing = f.app.refresh(tailnetId: a);
    await entered.future;
    expect(await f.app.removeTailnet(a), isTrue);
    response.complete(snapshot('a', name: 'Stale A'));
    expect(await refreshing, isFalse);
    expect(f.app.tailnetById(a), isNull);
    expect(f.app.tailnets.single.id, b);
    expect(f.app.selectedTailnetId, b);
  });

  test('reauthentication invalidates an older refresh for the same account', () async {
    final f = await fixture();
    final a = await f.add('a');
    final response = Completer<TailnetSnapshot>();
    final entered = Completer<void>();
    f.client.fetch = (_) { entered.complete(); return response.future; };
    final refreshing = f.app.refresh(tailnetId: a);
    await entered.future;
    f.client.loginSnapshot = snapshot('a', name: 'Reauthenticated A');
    await f.app.signInExit(tailnetId: a);
    await f.idle();
    response.complete(snapshot('a', name: 'Stale A'));
    expect(await refreshing, isFalse);
    expect(f.app.tailnetById(a)!.networkName, 'Reauthenticated A');
  });

  test('cancelled sign-in waiting on native stop cannot replace a later account', () async {
    final f = await fixture();
    final a = await f.add('a');
    await f.choose(a);
    f.vpn.stopGate = Completer<void>();
    f.client.loginSnapshot = snapshot('a', name: 'Cancelled A');
    final oldSignIn = f.app.signInExit(tailnetId: a);
    await pumpEventQueue();
    expect(f.app.signIn, SignInPhase.starting);
    await f.app.cancelSignIn();
    final b = await f.add('b');
    f.vpn.stopGate!.complete();
    await oldSignIn;
    expect(f.app.tailnets.length, 2);
    expect(f.app.tailnetById(a)!.networkName, 'a');
    expect(f.app.tailnetById(b)!.networkName, 'b');
    expect(f.client.logins.length, 2, reason: 'Cancelled reauthentication never registered.');
    expect(f.app.pickerRequestedTailnetId, b);
  });

  test('late cancelled browser authorization cannot commit or change the picker', () async {
    final f = await fixture();
    final events = StreamController<LoginProgress>();
    f.client.loginEvents = events.stream;
    await f.app.signInExit();
    final cancelledIdentity = f.client.logins.single;
    await f.app.cancelSignIn();
    f.client.loginEvents = null;
    final b = await f.add('b');
    events.add(LoginAuthorized(snapshot('a'), identity: cancelledIdentity));
    await events.close();
    await pumpEventQueue();
    expect(f.app.tailnets.single.id, b);
    expect(f.app.pickerRequestedTailnetId, b);
  });

  test('removing an unused account retains selected exit and running connection', () async {
    final f = await fixture();
    final a = await f.add('a');
    final b = await f.add('b');
    await f.choose(a);
    await f.app.connect();
    final selectedState = File('${f.paths.tailscaleStateFor(f.app.tailnetById(a)!).path}/${TsnetState.stateFileName}');
    final before = await selectedState.readAsBytes();
    expect(await f.app.removeTailnet(b), isTrue);
    expect(f.app.tailnets.single.id, a);
    expect(f.app.selectedTailnetId, a);
    expect(f.app.connected, isTrue);
    expect(f.vpn.stops, 0);
    expect(await selectedState.readAsBytes(), before);
    expect(f.client.logouts.single.machinePrivateKey, f.client.logins[1].machinePrivateKey);
  });

  test('removing selected account stops and leaves other identity without a fallback selection', () async {
    final f = await fixture();
    final a = await f.add('a');
    final b = await f.add('b');
    await f.choose(a);
    await f.app.connect();
    expect(await f.app.removeTailnet(a), isTrue);
    expect(f.vpn.stops, 1);
    expect(f.app.tailnets.single.id, b);
    expect(f.app.selectedTailnetId, isNull);
    expect(f.app.exit, isNull);
    expect(f.app.canConnect, isFalse);
    expect(f.app.connected, isFalse);
    final reloaded = await f.reload();
    expect(reloaded.tailnets.single.id, b);
    expect(reloaded.exit, isNull);
  });

  test('removing old running Global account still requires stop after selecting another tailnet', () async {
    final f = await fixture();
    final a = await f.add('a');
    final b = await f.add('b');
    await f.choose(a);
    await f.app.updateSettings(f.app.settings.copyWith(mode: RoutingMode.global));
    await f.app.connect();
    await f.choose(b);
    await pumpEventQueue();
    expect(f.app.selectedTailnetId, b);
    expect(f.app.needsReconnect, isTrue);
    expect(f.vpn.stops, 0);
    expect(f.app.signInRequiresConsent(), isFalse);
    expect(f.app.signInRequiresConsent(tailnetId: a), isTrue);
    expect(f.app.signInRequiresConsent(tailnetId: b), isFalse);
    f.vpn.stopError = StateError('native stop unavailable');
    expect(await f.app.removeTailnet(a), isFalse);
    expect(f.app.tailnets.length, 2);
    expect(f.client.logouts, isEmpty);
    f.vpn.stopError = null;
    expect(await f.app.removeTailnet(a), isTrue);
    expect(f.app.selectedTailnetId, b);
    expect(f.app.exit!.nodeId, 'exit-b');
    expect(f.app.connected, isFalse);
    expect(f.vpn.starts, 1, reason: 'Removal must not implicitly start the newly selected exit.');
  });

  test('restored unknown ownership forces stop before removing even a nonselected account', () async {
    final f = await fixture();
    final a = await f.add('a');
    final b = await f.add('b');
    await f.choose(a);
    final vpn = AccountVpn()..state = VpnConnectionState.connected;
    final app = AppController(paths: f.paths, vpn: vpn, controlClient: () => f.client);
    addTearDown(() async { app.dispose(); await vpn.events.close(); });
    await app.load();
    expect(app.signInRequiresConsent(), isFalse);
    expect(app.signInRequiresConsent(tailnetId: a), isTrue);
    expect(app.signInRequiresConsent(tailnetId: b), isTrue);
    vpn.stopError = StateError('unknown live owner');
    expect(await app.removeTailnet(b), isFalse);
    expect(app.tailnets.length, 2);
    expect(f.client.logouts, isEmpty);
    expect(vpn.stops, 1);
  });

  test('failed persistence retains all accounts and selection without logout', () async {
    final f = await fixture();
    final a = await f.add('a');
    final b = await f.add('b');
    await f.choose(a);
    await f.app.connect();
    final before = await f.paths.accountsFile.readAsString();
    await Directory('${f.paths.accountsFile.path}.tmp').create();
    expect(await f.app.removeTailnet(b), isFalse);
    expect(f.app.tailnets.length, 2);
    expect(f.app.selectedTailnetId, a);
    expect(f.app.connected, isTrue);
    expect(f.vpn.stops, 0);
    expect(f.client.logouts, isEmpty);
    expect(await f.paths.accountsFile.readAsString(), before);
    expect(await f.app.removeTailnet(a), isFalse);
    expect(f.app.tailnets.length, 2);
    expect(f.app.selectedTailnetId, a);
    expect(f.client.logouts, isEmpty);
    expect(f.vpn.stops, 1);
    expect(f.app.problem, AppProblem.storage);
  });

  test('failed add persistence does not replace or lose an existing account', () async {
    final f = await fixture();
    final a = await f.add('a');
    await f.choose(a);
    await Directory('${f.paths.accountsFile.path}.tmp').create();
    f.client.loginSnapshot = snapshot('b');
    await f.app.signInExit();
    await f.idle();
    expect(f.app.tailnets.single.id, a);
    expect(f.app.selectedTailnetId, a);
    expect(f.app.problem, AppProblem.signIn);
  });

  test('empty authoritative store blocks stale legacy resurrection and retrySave persists it', () async {
    final f = await fixture();
    final a = await f.add('a');
    await f.choose(a);
    final legacy = f.app.exit!.toJson();
    expect(await f.app.removeTailnet(a), isTrue);
    await JsonStore.write(f.paths.exitFile, legacy);
    final saved = (await JsonStore.read(f.paths.accountsFile))!;
    expect(saved['accounts'], isEmpty);
    expect(saved['selection'], isNull);
    await f.app.retrySave();
    final reloaded = await f.reload();
    expect(reloaded.tailnets, isEmpty);
    expect(reloaded.exit, isNull);
    expect(reloaded.canConnect, isFalse);
  });

  test('damaged authoritative store never falls back to legacy or gets overwritten by retry', () async {
    final f = await fixture();
    final a = await f.add('a');
    await f.choose(a);
    await JsonStore.write(f.paths.exitFile, f.app.exit!.toJson());
    await f.paths.accountsFile.writeAsString('{broken');
    final reloaded = await f.reload();
    expect(reloaded.tailnets, isEmpty);
    expect(reloaded.problem, AppProblem.storage);
    await reloaded.retrySave();
    expect(await f.paths.accountsFile.readAsString(), '{broken');
  });

  test('stale connection preflight cannot replace a later selected account or start its old exit', () async {
    final f = await fixture();
    final a = await f.add('a');
    final b = await f.add('b');
    await f.choose(a);
    f.client.fetch = (_) async => snapshot('a', at: DateTime(2020));
    expect(await f.app.refresh(tailnetId: a), isTrue);
    final response = Completer<TailnetSnapshot>();
    final entered = Completer<void>();
    f.client.fetch = (_) { entered.complete(); return response.future; };
    final connecting = f.app.connect();
    await entered.future;
    await f.choose(b);
    await f.vpn.startEntered.future.timeout(const Duration(seconds: 5));
    response.complete(snapshot('a', name: 'Stale preflight A'));
    await connecting;
    expect(f.app.selectedTailnetId, b);
    expect(f.app.exit!.nodeId, 'exit-b');
    expect(f.app.tailnetById(a)!.networkName, 'a');
    expect(f.vpn.starts, 1);
    expect((f.vpn.configs.single['endpoints'] as List).single['exit_node'], '100.64.0.2');
  });

  test('changing selection during a submitted Global start waits for explicit reconnect consent', () async {
    final f = await fixture();
    final a = await f.add('a');
    final b = await f.add('b');
    await f.choose(a);
    await f.app.updateSettings(f.app.settings.copyWith(mode: RoutingMode.global));
    f.vpn.startGate = Completer<void>();
    final connecting = f.app.connect();
    await f.vpn.startEntered.future;
    await f.choose(b);
    expect(f.app.reconnectRequiresConsent, isTrue);
    expect(f.vpn.stops, 0);
    f.vpn.startGate!.complete();
    await connecting;
    expect(f.vpn.stops, 0, reason: 'A pending Global start has the same consent policy as an active tunnel.');
    expect(f.app.connected, isTrue);
    expect(f.app.needsReconnect, isTrue);
    expect(f.app.selectedTailnetId, b);
    expect(f.vpn.starts, 1);
    await f.app.reconnect(allowProtectionGap: true);
    expect(f.vpn.stops, 1);
    expect(f.vpn.starts, 2);
    expect((f.vpn.configs.last['endpoints'] as List).single['exit_node'], '100.64.0.2');
  });

  test('native key rotation is read and persisted only for the captured refresh account', () async {
    final f = await fixture();
    final a = await f.add('a');
    final b = await f.add('b');
    await f.choose(b);
    final account = f.app.tailnetById(a)!;
    final bIdentity = f.app.tailnetById(b)!.identity;
    final rotated = account.identity.copyWith(
        nodePrivateKey: (await TailscaleIdentity.generate(hostname: 'phone')).nodePrivateKey);
    await TsnetState.write(stateDirectory: f.paths.tailscaleStateFor(account),
        identity: rotated, snapshot: account.snapshot!);
    String? requestedKey;
    f.client.fetch = (identity) async {
      requestedKey = identity.nodePrivateKey;
      return snapshot('a');
    };
    expect(await f.app.refresh(tailnetId: a), isTrue);
    expect(requestedKey, rotated.nodePrivateKey);
    expect(f.app.tailnetById(a)!.identity.nodePrivateKey, rotated.nodePrivateKey);
    expect(f.app.tailnetById(b)!.identity, same(bIdentity));
    expect(f.app.exit!.identity, same(bIdentity));
    final persisted = TailnetStore.fromJson((await JsonStore.read(f.paths.accountsFile))!);
    expect(persisted.accounts[a]!.identity.nodePrivateKey, rotated.nodePrivateKey);
    expect(persisted.accounts[b]!.identity.nodePrivateKey, bIdentity.nodePrivateKey);
  });

  test('sign-in and refresh log account-scoped count diagnostics without peer or identity data', () async {
    final f = await fixture();
    final a = await f.add('a');
    expect(await f.app.refresh(tailnetId: a), isTrue);
    final discovery = f.app.logs.where((line) => line.message.contains('Netmap discovery:')).toList();
    expect(discovery.length, 2);
    for (final line in discovery) {
      expect(line.message, contains('Tailnet $a: machine-authorized=true;'));
      expect(line.message, contains('peers=1, exit-routes=1'));
      for (final secret in ['phone.a.ts.net', 'exit-a', '100.64.0.', 'privkey:', 'user@a.example']) {
        expect(line.message, isNot(contains(secret)));
      }
    }
  });

  test('reauthenticating a running Global account requires consent and retains other accounts', () async {
    final f = await fixture();
    final a = await f.add('a');
    final b = await f.add('b');
    await f.choose(a);
    await f.app.updateSettings(f.app.settings.copyWith(mode: RoutingMode.global));
    await f.app.connect();
    f.client.loginSnapshot = snapshot('a');
    await f.app.signInExit(tailnetId: a);
    expect(f.client.logins.length, 2);
    expect(f.vpn.stops, 0);
    expect(f.app.needsReconnect, isTrue);
    await f.app.signInExit(tailnetId: a, allowProtectionGap: true);
    await f.idle();
    expect(f.client.logins.length, 3);
    expect(f.vpn.stops, 1);
    expect(f.app.tailnetById(b), isNotNull);
    expect(f.app.selectedTailnetId, a);
    expect(f.app.exit!.nodeId, 'exit-a');
  });

  test('preview data includes separate synthetic identities and tailnets', () async {
    final preview = await PreviewData.seed();
    expect(preview.tailnets.length, 2);
    expect(preview.tailnets.map((account) => account.networkKey).toSet().length, 2);
    expect(preview.tailnets.map((account) => account.identity.machinePrivateKey).toSet().length, 2);
    expect(preview.exit.hasNode, isTrue);
  });
}
