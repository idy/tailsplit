// Cross-implementation tests against a fake control server built from the
// pinned Go tailscale fork (controlhttpserver + controlbase + x/net/http2).
// Skipped unless one of these is set:
//   TS_FAKE_CONTROL_BIN  path to the fakecontrol binary (spawned per group)
//   TS_FAKE_CONTROL_URL  URL of an already running fakecontrol
// With TS_HANDOFF_DIR set, a signed-in identity's tsnet state is written to
// $TS_HANDOFF_DIR/state plus $TS_HANDOFF_DIR/expect.json for the Go handoff
// check (which reuses the same running control server).

import 'dart:async';
import 'dart:convert';
import 'dart:io';

import 'package:flutter_test/flutter_test.dart';
import 'package:tailsplit_mobile/src/tailscale/keys.dart';
import 'package:tailsplit_mobile/src/tailscale/tailscale.dart';

final _bin = Platform.environment['TS_FAKE_CONTROL_BIN'];
final _externalUrl = Platform.environment['TS_FAKE_CONTROL_URL'];
final _handoffDir = Platform.environment['TS_HANDOFF_DIR'];

class _Fake {
  _Fake(this.url, this._process);
  final Uri url;
  final Process? _process;

  static Future<_Fake> start({List<String> args = const []}) async {
    if (_externalUrl != null && args.isEmpty) return _Fake(Uri.parse(_externalUrl!), null);
    final process = await Process.start(_bin!, ['-listen', '127.0.0.1:0', ...args]);
    process.stderr.drain<void>();
    final line = await process.stdout.transform(utf8.decoder).transform(const LineSplitter()).first;
    final info = jsonDecode(line) as Map;
    return _Fake(Uri.parse(info['url'] as String), process);
  }

  Future<List<Map<String, Object?>>> events() async {
    final client = HttpClient();
    try {
      final req = await client.getUrl(url.replace(path: '/debug/events'));
      final res = await req.close();
      final body = await res.transform(utf8.decoder).join();
      return [for (final e in jsonDecode(body) as List) (e as Map).cast<String, Object?>()];
    } finally {
      client.close(force: true);
    }
  }

  Future<void> expireNodeKey(String nodePublic) async {
    final client = HttpClient();
    try {
      final req = await client.getUrl(url.replace(path: '/debug/expire', queryParameters: {'nodekey': nodePublic}));
      await (await req.close()).drain<void>();
    } finally {
      client.close(force: true);
    }
  }

  void stop() => _process?.kill();
}

Future<(List<LoginProgress>, Object?)> _collect(Stream<LoginProgress> stream) async {
  final events = <LoginProgress>[];
  Object? error;
  try {
    await for (final e in stream.timeout(const Duration(seconds: 30))) {
      events.add(e);
    }
  } catch (e) {
    error = e;
  }
  return (events, error);
}

void main() {
  final skip = (_bin == null && _externalUrl == null) ? 'set TS_FAKE_CONTROL_BIN or TS_FAKE_CONTROL_URL' : null;

  group('against Go fake control', () {
    late _Fake fake;
    setUpAll(() async => fake = await _Fake.start());
    tearDownAll(() => fake.stop());

    test('login: auth URL, follow-up long poll, read-only map', () async {
      final identity = await TailscaleIdentity.generate(hostname: 'tailsplit-test');
      final client = TailscaleControlClient(controlUrl: fake.url);
      final (events, error) = await _collect(client.login(identity));
      await client.close();
      expect(error, isNull);
      expect(events, hasLength(2));
      expect((events[0] as LoginNeedsBrowser).authUrl.host, 'login.fake.test');
      final done = events[1] as LoginAuthorized;
      expect(done.identity.nodePrivateKey, identity.nodePrivateKey);
      final s = done.snapshot;
      expect(s.selfName, 'tailsplit-test');
      expect(s.selfFqdn, 'tailsplit-test.tail1234.ts.net');
      expect(s.selfAddresses, ['100.101.7.42', 'fd7a:115c:a1e0::1']);
      expect(s.networkName, 'Example Tailnet');
      expect(s.domainName, 'alice@example.com');
      expect(s.magicDnsSuffix, 'tail1234.ts.net');
      expect(s.userLoginName, 'alice@example.com');
      expect(s.userDisplayName, 'Alice Example');
      expect(s.userId, 4242);
      expect(s.stableNodeId, startsWith('nFAKE'));
      expect(s.machineAuthorized, isTrue);
      expect(s.keyExpiry, isNotNull);
      expect([for (final p in s.exitNodes) p.name], ['tokyo-vps', 'sg-relay']);
      expect(s.exitNodes.first.online, isTrue);
      expect(s.exitNodes.last.lastSeen, isNotNull);

      final log = await fake.events();
      final noise = log.where((e) => e['kind'] == 'noise').toList();
      expect(noise, isNotEmpty);
      expect(noise.every((e) => e['machine_key'] == identity.machinePublicKey), isTrue);
      expect(noise.first['version'], 142);
      expect(noise.first['upgrade'], 'tailscale-control-protocol');
      final regs = log.where((e) => e['kind'] == 'register' && e['node_key'] == identity.nodePublicKey).toList();
      expect(regs.map((e) => e['followup']), [false, true]);
      for (final r in regs) {
        expect(r['proto'], 'HTTP/2.0');
        expect(r['version'], 142);
        expect(r['hostname'], 'tailsplit-test');
        expect(r['os'], 'iOS');
        expect(r['nl_key'], identity.networkLockPublicKey);
        expect(r['lb'], identity.nodePublicKey);
        expect(r['old_node_key'], zeroNodePublicKey);
      }
      final maps = log.where((e) => e['kind'] == 'map' && e['node_key'] == identity.nodePublicKey).toList();
      expect(maps, hasLength(1));
      expect(maps.single['stream'], isTrue);
      expect(maps.single['omit_peers'], isFalse);
      expect(maps.single['compress'], '');
      expect(maps.single['endpoints'], 0);
      expect(maps.single['disco_key'], zeroDiscoPublicKey);
    });

    test('fetchSnapshot, key rotation, logout, needs-login', () async {
      final identity = await TailscaleIdentity.generate(hostname: 'rotator');
      var client = TailscaleControlClient(controlUrl: fake.url, os: 'android');
      var (events, error) = await _collect(client.login(identity));
      expect(error, isNull);
      final signedIn = (events.last as LoginAuthorized).identity;

      final snap = await client.fetchSnapshot(signedIn);
      expect(snap.exitNodes, hasLength(2));
      await client.close();

      // Control expires the key: login must rotate it and keep the device.
      await fake.expireNodeKey(signedIn.nodePublicKey);
      client = TailscaleControlClient(controlUrl: fake.url, os: 'android');
      (events, error) = await _collect(client.login(signedIn));
      expect(error, isNull);
      expect(events.first, isA<LoginNeedsBrowser>());
      final rotated = (events.last as LoginAuthorized).identity;
      expect(rotated.nodePrivateKey, isNot(signedIn.nodePrivateKey));
      expect(rotated.machinePrivateKey, signedIn.machinePrivateKey);
      expect((events.last as LoginAuthorized).snapshot.stableNodeId, snap.stableNodeId);
      final log = await fake.events();
      final rotation = log.firstWhere((e) => e['kind'] == 'register' && e['node_key'] == rotated.nodePublicKey);
      expect(rotation['old_node_key'], signedIn.nodePublicKey);
      expect((rotation['lb'] as String).split(','), [signedIn.nodePublicKey, rotated.nodePublicKey]);

      await expectLater(client.fetchSnapshot(signedIn), throwsA(isA<TailscaleNeedsLogin>()));

      await client.logout(rotated);
      expect((await fake.events()).any((e) => e['kind'] == 'logout' && e['node_key'] == rotated.nodePublicKey),
          isTrue);
      await expectLater(client.fetchSnapshot(rotated), throwsA(isA<TailscaleNeedsLogin>()));
      await client.close();
    });

    test('cancelling login closes the connection', () async {
      final identity = await TailscaleIdentity.generate(hostname: 'canceller');
      final client = TailscaleControlClient(controlUrl: fake.url);
      final first = Completer<LoginProgress>();
      final sub = client.login(identity).listen(first.complete);
      expect(await first.future.timeout(const Duration(seconds: 15)), isA<LoginNeedsBrowser>());
      await sub.cancel();
      await client.close();
      expect(() => client.fetchSnapshot(identity), throwsA(isA<TailscaleControlException>()));
    });

    test('writes tsnet state for the Go handoff check', () async {
      final identity = await TailscaleIdentity.generate(hostname: 'handoff-node');
      final client = TailscaleControlClient(controlUrl: fake.url);
      final (events, error) = await _collect(client.login(identity));
      await client.close();
      expect(error, isNull);
      final done = events.last as LoginAuthorized;
      final dir = Directory('${_handoffDir!}/state');
      if (await dir.exists()) await dir.delete(recursive: true);
      await TsnetState.write(
        stateDirectory: dir,
        identity: done.identity,
        snapshot: done.snapshot,
        controlUrl: fake.url,
      );
      expect(await TsnetState.readNodePrivateKey(dir), done.identity.nodePrivateKey);
      await File('$_handoffDir/expect.json').writeAsString(jsonEncode({
        'control_url': fake.url.toString(),
        'machine_public': done.identity.machinePublicKey,
        'node_public': done.identity.nodePublicKey,
        'node_id': done.snapshot.stableNodeId,
        'hostname': done.identity.hostname,
      }));
    }, skip: _handoffDir == null ? 'set TS_HANDOFF_DIR' : null);
  }, skip: skip);

  group('without early payload', () {
    late _Fake fake;
    setUpAll(() async => fake = await _Fake.start(args: ['-no-early-payload']));
    tearDownAll(() => fake.stop());

    test('login works when the server starts HTTP/2 directly', () async {
      final identity = await TailscaleIdentity.generate(hostname: 'plain');
      final client = TailscaleControlClient(controlUrl: fake.url);
      final (events, error) = await _collect(client.login(identity));
      await client.close();
      expect(error, isNull);
      expect(events.last, isA<LoginAuthorized>());
    });
  }, skip: _bin == null ? 'set TS_FAKE_CONTROL_BIN' : null);

  test('handoff state still names the same node after tsnet ran', () async {
    final dir = Directory('${_handoffDir!}/state');
    final expect_ = jsonDecode(await File('$_handoffDir/expect.json').readAsString()) as Map;
    final live = await TsnetState.readNodePrivateKey(dir);
    expect(live, isNotNull);
    expect(publicKeyText(live!, KeyPrefix.nodePublic), expect_['node_public']);
    expect(await TsnetState.readLoggedOut(dir), isFalse);
  }, skip: Platform.environment['TS_HANDOFF_VERIFY'] == null ? 'set TS_HANDOFF_VERIFY and TS_HANDOFF_DIR' : null);
}
