import 'dart:async';
import 'dart:convert';
import 'dart:io';

import 'package:flutter_test/flutter_test.dart';
import 'package:tailsplit_mobile/src/model/exit_profile.dart';
import 'package:tailsplit_mobile/src/model/settings.dart';
import 'package:tailsplit_mobile/src/services/config_builder.dart';
import 'package:tailsplit_mobile/src/services/errors.dart';
import 'package:tailsplit_mobile/src/services/rules.dart';
import 'package:tailsplit_mobile/src/services/storage.dart';
import 'package:tailsplit_mobile/src/services/vpn.dart';
import 'package:tailsplit_mobile/src/state/app_controller.dart';
import 'package:tailsplit_mobile/src/tailscale/control_conn.dart' show ControlTransientException;
import 'package:tailsplit_mobile/src/tailscale/tailscale.dart';
import 'package:tailsplit_mobile/src/ui/editors.dart';

const _list = '[AutoProxy 0.2.9]\n||blocked.example';

class FakeVpn extends VpnService {
  final statuses = StreamController<VpnStatus>.broadcast();
  int starts = 0;
  int stops = 0;
  VpnConnectionState state = VpnConnectionState.disconnected;

  @override
  Stream<VpnStatus> get status => statuses.stream;
  @override
  Stream<SingBoxLog> get logs => const Stream.empty();
  @override
  Future<void> initialize() async {}
  @override
  Future<VpnStatus> currentStatus() async => VpnStatus(state: state);
  @override
  Future<String> coreVersion() async => 'test';
  @override
  Future<void> check(String config) async {}
  @override
  Future<bool> requestPermission() async => true;
  @override
  Future<void> start(String config) async => starts++;
  @override
  Future<void> stop() async => stops++;
  @override
  Future<void> waitFor(VpnConnectionState state, {Duration timeout = const Duration(seconds: 15)}) async {}
}

class FakeClient implements TailscaleControlClient {
  FakeClient({this.snapshot, this.loginEvents});

  final TailnetSnapshot? snapshot;
  final Stream<LoginProgress>? loginEvents;

  @override
  Future<TailnetSnapshot> fetchSnapshot(TailscaleIdentity identity) async =>
      snapshot ?? (throw const ControlTransientException('Cannot reach Tailscale control: offline.', kind: ControlFailureKind.offline));
  @override
  Stream<LoginProgress> login(TailscaleIdentity identity) => loginEvents ?? const Stream.empty();
  @override
  Future<void> logout(TailscaleIdentity identity) async {}
  @override
  Future<void> close() async {}
  @override
  dynamic noSuchMethod(Invocation invocation) => super.noSuchMethod(invocation);
}

TailnetPeer _peer(String name, {bool online = true, List<String> addresses = const ['100.64.0.9']}) =>
    TailnetPeer(
      stableId: 'id-$name',
      name: name,
      fqdn: '$name.example.ts.net',
      addresses: addresses,
      online: online,
      isExitNode: true,
    );

TailnetSnapshot _snapshot(List<TailnetPeer> peers, {DateTime? at, DateTime? keyExpiry}) => TailnetSnapshot(
      selfName: 'phone',
      selfFqdn: 'phone.example.ts.net',
      selfAddresses: const ['100.64.0.2'],
      networkName: 'example.com',
      magicDnsSuffix: 'example.ts.net',
      userLoginName: 'user@example.com',
      userDisplayName: 'User',
      stableNodeId: 'self',
      exitNodes: peers,
      keyExpiry: keyExpiry,
      fetchedAt: at ?? DateTime.now(),
    );

Future<(AppController, FakeVpn, Directory)> _controller({
  TailscaleControlClient Function()? client,
  Future<String> Function(Uri uri)? download,
}) async {
  final root = await Directory.systemTemp.createTemp('tailsplit-test');
  final vpn = FakeVpn();
  final app = AppController(
    paths: AppPaths.at(Directory('${root.path}/app')..createSync(), Directory('${root.path}/shared')..createSync()),
    vpn: vpn,
    controlClient: client ?? FakeClient.new,
    ruleDownload: download,
  );
  app.settings = app.settings.copyWith(vpnDisclosureAccepted: true);
  return (app, vpn, root);
}

Future<File> _savedState(Directory root, ExitProfile profile, String state) async {
  final directory = Directory('${root.path}/shared/tailscale');
  await TsnetState.write(
    stateDirectory: directory,
    identity: profile.identity,
    snapshot: profile.snapshot!,
  );
  final file = File('${directory.path}/${TsnetState.stateFileName}');
  if (state == 'unreadable') {
    await file.writeAsString('{}');
    return file;
  }
  final store = jsonDecode(await file.readAsString()) as Map<String, dynamic>;
  final profileKey = utf8.decode(base64.decode(store['_current-profile'] as String));
  final prefs = jsonDecode(utf8.decode(base64.decode(store[profileKey] as String))) as Map<String, dynamic>;
  if (state == 'logged-out') prefs['LoggedOut'] = true;
  if (state == 'missing-key') (prefs['Config'] as Map).remove('PrivateNodeKey');
  store[profileKey] = base64.encode(utf8.encode(jsonEncode(prefs)));
  await file.writeAsString(jsonEncode(store));
  return file;
}

void main() {
  TestWidgetsFlutterBinding.ensureInitialized();
  RuleLists.supplementForTesting = '[AutoProxy 0.2.9]\n||supplement.example';

  group('resolver addresses', () {
    test('accept IPv4, IPv6, ports and system', () {
      expect(ResolverAddress.parse('8.8.8.8').toString(), '8.8.8.8');
      expect(ResolverAddress.parse('1.1.1.1:5353')!.port, 5353);
      expect(ResolverAddress.parse('2001:4860:4860::8888').toString(), '2001:4860:4860::8888');
      final bracketed = ResolverAddress.parse('[2001:db8::1]:853')!;
      expect((bracketed.host, bracketed.port), ('2001:db8::1', 853));
      expect(bracketed.toString(), '[2001:db8::1]:853');
      expect(ResolverAddress.parse(' ')!.isSystem, isTrue);
    });

    test('reject hostnames, bad ports and short IPv4', () {
      for (final value in ['dns.google', '8.8.8.8:0', '8.8.8.8:70000', '1.1', '[::1', '300.1.1.1']) {
        expect(ResolverAddress.parse(value), isNull, reason: value);
      }
    });

    test('an unusable proxy resolver falls back to a real server', () {
      final config = ConfigBuilder.buildObject(
        settings: const MobileSettings(proxyDns: 'system', source: RuleSource.none),
        exit: const ExitRuntime(hostname: 'h', nodeAddress: '100.64.0.9', stateDirectory: '/s', cacheFile: '/c'),
      );
      final servers = ((config['dns'] as Map)['servers'] as List).cast<Map>();
      final proxy = servers.firstWhere((s) => s['tag'] == 'dns-proxy');
      expect((proxy['type'], proxy['server']), ('udp', '8.8.8.8'));
    });
  });

  test('failures map to causes the UI can explain', () {
    expect(classifyFailure(const SocketException('x')), FailureCause.offline);
    expect(classifyFailure(TimeoutException('x')), FailureCause.timeout);
    expect(classifyFailure(const ControlTransientException('Cannot reach Tailscale control: x.', kind: ControlFailureKind.offline)), FailureCause.offline);
    expect(classifyFailure(const ControlTransientException('Timed out reaching Tailscale control.', kind: ControlFailureKind.timeout)), FailureCause.timeout);
    expect(classifyFailure(const ControlTransientException('Cannot verify Tailscale control’s TLS certificate.', kind: ControlFailureKind.tls)), FailureCause.tls);
    expect(classifyFailure(const ControlTransientException('Tailscale control is rate limiting this device (HTTP 429).', kind: ControlFailureKind.rateLimited)), FailureCause.rateLimited);
    expect(classifyFailure(const TailscaleControlException('Tailscale control refused the sign-in: x', kind: ControlFailureKind.rejected)), FailureCause.rejected);
    expect(classifyFailure(const RuleListException('HTML is not a rule list')), FailureCause.invalid);
    expect(classifyFailure(const FileSystemException('disk full')), FailureCause.storage);
  });

  group('rule list cache', () {
    late Directory dir;
    setUp(() async => dir = await Directory.systemTemp.createTemp('rules'));

    test('downloads once, then reuses the saved copy without the network', () async {
      var downloads = 0;
      Future<String> download(Uri uri) async {
        downloads++;
        return _list;
      }

      await RuleLists.load('https://a.example/list', dir, download: download);
      final again = await RuleLists.load('https://a.example/list', dir, download: download);
      expect(downloads, 1);
      expect(again.projection.proxy.suffix, contains('blocked.example'));
      expect(await RuleLists.cachedAt(dir, 'https://a.example/list'), isNotNull);
    });

    test('a failed update keeps the saved copy and reports why', () async {
      await RuleLists.load('https://a.example/list', dir, download: (_) async => _list);
      final updated = await RuleLists.load('https://a.example/list', dir,
          refresh: true, download: (_) async => throw const RuleListException('reset', cause: FailureCause.offline));
      expect(updated.projection.proxy.suffix, contains('blocked.example'));
      expect(classifyFailure(updated.refreshError!), FailureCause.offline);
    });

    test('each source keeps its own cache, and a first failure is reported', () async {
      await RuleLists.load('https://a.example/list', dir, download: (_) async => _list);
      expect(
        () => RuleLists.load('https://b.example/list', dir,
            download: (_) async => throw const RuleListException('reset', cause: FailureCause.offline)),
        throwsA(isA<RuleListException>().having((e) => e.cause, 'cause', FailureCause.offline)),
      );
    });

    test('an invalid download never replaces a good copy', () async {
      await RuleLists.load('https://a.example/list', dir, download: (_) async => _list);
      final updated = await RuleLists.load('https://a.example/list', dir,
          refresh: true, download: (_) async => '<!doctype html><html><body>captive portal</body></html>');
      expect(classifyFailure(updated.refreshError!), FailureCause.invalid);
      final cached = await RuleLists.load('https://a.example/list', dir, download: (_) async => fail('network'));
      expect(cached.projection.proxy.suffix, contains('blocked.example'));
    });
  });

  test('CIDR entries are validated and stored as networks', () {
    expect(SettingsList.proxyIps.normalize('192.0.2.77/24'), '192.0.2.0/24');
    expect(SettingsList.proxyIps.normalize('10.1.2.3'), '10.1.2.3/32');
    expect(SettingsList.proxyIps.normalize('2001:db8::7/32'), '2001:db8::/32');
    for (final bad in ['10.0.0.0/33', '1.2.3', 'example.com', '10.0.0.0/x']) {
      expect(SettingsList.proxyIps.normalize(bad), isNull, reason: bad);
    }
  });

  test('damaged settings keep readable values and default the rest', () {
    final settings = MobileSettings.fromJson({
      'mode': 'global',
      'custom_source_url': 42,
      'proxy_dns': '',
      'fake_ip': 'yes',
      'mtu': 100,
      'proxy_domains': ['a.example', ' a.example ', 7, ''],
    });
    expect(settings.mode, RoutingMode.global);
    expect(settings.customSourceUrl, '');
    expect(settings.proxyDns, '8.8.8.8');
    expect(settings.fakeIp, isTrue);
    expect(settings.mtu, MobileSettings.minMtu);
    expect(settings.proxyDomains, ['a.example']);
  });

  test('log lines drop terminal escapes and control characters', () {
    expect(LogLine.sanitize('\x1B[31mred\x1B[0m\tok\x07'), 'red\tok');
    expect(LogLine.sanitize('x' * 5000).length, LogLine.maxLength + 1);
  });

  group('connection preflight', () {
    for (final state in ['logged-out', 'missing-key', 'unreadable']) {
      test('$state native state requires sign-in without discarding the exit', () async {
        final (app, vpn, root) = await _controller();
        addTearDown(() async {
          app.dispose();
          await vpn.statuses.close();
          await root.delete(recursive: true);
        });
        app.settings = app.settings.copyWith(source: RuleSource.none);
        final profile = ExitProfile(
          identity: await TailscaleIdentity.generate(hostname: 'phone'),
          snapshot: _snapshot([_peer('tokyo')]),
          nodeId: 'id-tokyo',
          nodeName: 'tokyo',
          nodeAddress: '100.64.0.9',
        );
        app.exit = profile;
        final file = await _savedState(root, profile, state);
        final before = await file.readAsString();
        await app.connect();
        expect(vpn.starts, 0);
        expect(app.problem, AppProblem.needsSignIn);
        expect(app.preparing, isFalse);
        expect(app.exit!.identity, same(profile.identity));
        expect(app.exit!.nodeId, profile.nodeId);
        expect(await file.readAsString(), before,
            reason: 'A connect must not silently restore a logged-out identity.');
      });
    }

    test('signing in again repairs native state while keeping the chosen exit', () async {
      final identity = await TailscaleIdentity.generate(hostname: 'phone');
      final snapshot = _snapshot([_peer('tokyo')]);
      final (app, vpn, root) = await _controller(client: () => FakeClient(
        loginEvents: Stream.value(LoginAuthorized(snapshot, identity: identity)),
      ));
      addTearDown(() async {
        app.dispose();
        await vpn.statuses.close();
        await root.delete(recursive: true);
      });
      app.settings = app.settings.copyWith(source: RuleSource.none);
      app.exit = ExitProfile(identity: identity, snapshot: snapshot,
          nodeId: 'id-tokyo', nodeName: 'tokyo', nodeAddress: '100.64.0.9');
      await _savedState(root, app.exit!, 'logged-out');
      await app.connect();
      expect(app.problem, AppProblem.needsSignIn);

      final finished = Completer<void>();
      app.addListener(() {
        if (app.signIn == SignInPhase.idle && !finished.isCompleted) finished.complete();
      });
      await app.signInExit(tailnetId: app.selectedTailnetId);
      await finished.future.timeout(const Duration(seconds: 5));
      expect(app.problem, isNull);
      expect(app.exit!.nodeId, 'id-tokyo');
      expect(await TsnetState.readLoggedOut(Directory('${root.path}/shared/tailscale')), isFalse);
      await app.connect();
      expect(vpn.starts, 1);
      expect(app.problem, isNull);
    });

    test('expired cached keys cannot start when revalidation is unavailable', () async {
      final (app, vpn, root) = await _controller();
      addTearDown(() async {
        app.dispose();
        await vpn.statuses.close();
        await root.delete(recursive: true);
      });
      app.settings = app.settings.copyWith(source: RuleSource.none);
      app.exit = ExitProfile(
        identity: await TailscaleIdentity.generate(hostname: 'phone'),
        snapshot: _snapshot([_peer('tokyo')], keyExpiry: DateTime.utc(2020)),
        nodeId: 'id-tokyo', nodeName: 'tokyo', nodeAddress: '100.64.0.9',
      );
      await _savedState(root, app.exit!, 'active');
      await app.connect();
      expect(vpn.starts, 0);
      expect(app.problem, AppProblem.needsSignIn);
    });

    test('a fresh control snapshot can renew an expired cached key', () async {
      final snapshot = _snapshot([_peer('tokyo')],
          keyExpiry: DateTime.now().add(const Duration(days: 30)));
      final (app, vpn, root) = await _controller(client: () => FakeClient(snapshot: snapshot));
      addTearDown(() async {
        app.dispose();
        await vpn.statuses.close();
        await root.delete(recursive: true);
      });
      app.settings = app.settings.copyWith(source: RuleSource.none);
      app.exit = ExitProfile(
        identity: await TailscaleIdentity.generate(hostname: 'phone'),
        snapshot: _snapshot([_peer('tokyo')], keyExpiry: DateTime.utc(2020)),
        nodeId: 'id-tokyo', nodeName: 'tokyo', nodeAddress: '100.64.0.9',
      );
      await _savedState(root, app.exit!, 'active');
      await app.connect();
      expect(vpn.starts, 1);
      expect(app.problem, isNull);
      expect(app.exit!.snapshot!.keyExpiry, snapshot.keyExpiry);
    });

    test('refuses an exit node that Tailscale reports offline', () async {
      final stale = _snapshot([_peer('tokyo')], at: DateTime(2020));
      final (app, vpn, _) = await _controller(
        client: () => FakeClient(snapshot: _snapshot([_peer('tokyo', online: false)])),
      );
      app.settings = app.settings.copyWith(source: RuleSource.none);
      app.exit = ExitProfile(
        identity: await TailscaleIdentity.generate(hostname: 'phone'),
        snapshot: stale,
        nodeId: 'id-tokyo',
        nodeName: 'tokyo',
        nodeAddress: '100.64.0.9',
      );
      await app.connect();
      expect(vpn.starts, 0);
      expect(app.problem, AppProblem.nodeOffline);
      expect(app.problemSubject, 'tokyo');
      expect(app.preparing, isFalse);
    });

    test('refuses a node no longer offered as an exit', () async {
      final (app, vpn, _) = await _controller(client: () => FakeClient(snapshot: _snapshot([_peer('fra')])));
      app.settings = app.settings.copyWith(source: RuleSource.none);
      app.exit = ExitProfile(
        identity: await TailscaleIdentity.generate(hostname: 'phone'),
        nodeId: 'id-tokyo',
        nodeName: 'tokyo',
        nodeAddress: '100.64.0.9',
      );
      await app.connect();
      expect((vpn.starts, app.problem), (0, AppProblem.nodeMissing));
    });

    test('turning the switch off while preparing cancels the attempt', () async {
      final download = Completer<String>();
      final (app, vpn, _) = await _controller(download: (_) => download.future);
      app.exit = ExitProfile(
        identity: await TailscaleIdentity.generate(hostname: 'phone'),
        snapshot: _snapshot([_peer('tokyo')]),
        nodeId: 'id-tokyo',
        nodeName: 'tokyo',
        nodeAddress: '100.64.0.9',
      );
      final attempt = app.connect();
      // Real file IO (tsnet state) precedes the rule step; wait for it.
      for (var i = 0; i < 200 && app.connectStep != ConnectStep.rules; i++) {
        await Future<void>.delayed(const Duration(milliseconds: 10));
      }
      expect(app.connectStep, ConnectStep.rules);
      expect(app.canToggleConnection, isTrue);
      await app.disconnect();
      expect(app.preparing, isFalse);
      download.complete(_list);
      await attempt;
      expect(vpn.starts, 0);
      expect(app.problem, isNull);
    });

    test('a node without an address cannot be chosen', () async {
      final (app, _, _) = await _controller();
      app.exit = ExitProfile(identity: await TailscaleIdentity.generate(hostname: 'phone'));
      expect(AppController.selectable(_peer('bare', addresses: const [])), isFalse);
      expect(await app.confirmNode(_peer('bare', addresses: const [])), isFalse);
      expect(app.exit!.hasNode, isFalse);
    });
  });

  test('a sign-in that cannot be saved ends with a retryable problem', () async {
    final identity = await TailscaleIdentity.generate(hostname: 'phone');
    final (app, _, root) = await _controller(
      client: () => FakeClient(
        loginEvents: Stream.value(LoginAuthorized(_snapshot([_peer('tokyo')]), identity: identity)),
      ),
    );
    // A file where account state directories belong makes the save fail.
    File('${root.path}/shared/accounts').writeAsStringSync('');
    await app.signInExit();
    await pumpEventQueue();
    expect(app.signIn, SignInPhase.idle);
    expect(app.problem, AppProblem.signIn);
  });

  test('a settings save failure is reported and can be retried', () async {
    final (app, _, root) = await _controller();
    Directory('${root.path}/app/settings.json').createSync();
    await app.updateSettings(const MobileSettings(fakeIp: false));
    expect(app.settings.fakeIp, isFalse, reason: 'the change still applies in memory');
    expect(app.problem, AppProblem.storage);
    Directory('${root.path}/app/settings.json').deleteSync();
    await app.retrySave();
    expect(app.problem, isNull);
  });
}
