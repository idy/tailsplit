import 'dart:convert';
import 'dart:io';

import 'package:flutter_test/flutter_test.dart';
import 'package:tailsplit_mobile/src/tailscale/keys.dart';
import 'package:tailsplit_mobile/src/tailscale/tailscale.dart';

TailnetSnapshot _snapshot() => TailnetSnapshot(
      selfName: 'tailsplit-ios',
      selfFqdn: 'tailsplit-ios.tail4f2a1c.ts.net',
      selfAddresses: const ['100.101.7.42', 'fd7a:115c:a1e0::1'],
      networkName: "Alice's Tailnet",
      magicDnsSuffix: 'tail4f2a1c.ts.net',
      userLoginName: 'alice@example.com',
      userDisplayName: 'Alice',
      stableNodeId: 'nSELF1001CNTRL',
      keyExpiry: DateTime.utc(2027, 5, 9, 7),
      exitNodes: [
        TailnetPeer(
          stableId: 'n1',
          name: 'tokyo',
          fqdn: 'tokyo.tail4f2a1c.ts.net',
          addresses: const ['100.88.12.4'],
          online: false,
          lastSeen: DateTime.utc(2026, 11, 10, 4),
          os: 'linux',
          isExitNode: true,
        ),
      ],
      fetchedAt: DateTime.utc(2026, 11, 10, 8),
      userId: 4242,
      domainName: 'alice@example.com',
      tailnetDisplayName: "Alice's Tailnet",
      machineAuthorized: false,
    );

String _name(FileSystemEntity e) => e.uri.pathSegments.where((s) => s.isNotEmpty).last;

void main() {
  group('TailscaleIdentity', () {
    test('generate: fresh clamped keys, cached public keys, JSON round-trip', () async {
      final a = await TailscaleIdentity.generate(hostname: 'My iPhone 15 Pro');
      final b = await TailscaleIdentity.generate(hostname: 'x');
      expect(a.hostname, 'my-iphone-15-pro');
      expect(a.machinePrivateKey, startsWith('privkey:'));
      expect(a.nodePrivateKey, isNot(a.machinePrivateKey));
      expect(a.nodePrivateKey, isNot(b.nodePrivateKey));
      expect(a.nodePublicKey, startsWith('nodekey:'));
      expect(a.machinePublicKey, startsWith('mkey:'));
      expect(a.networkLockPrivateKey, startsWith('nlpriv:'));
      expect(a.networkLockPublicKey, startsWith('nlpub:'));
      expect(parsePrivateKey(a.nodePrivateKey)[0] & 7, 0);

      final c = TailscaleIdentity.fromJson(jsonDecode(jsonEncode(a.toJson())) as Map<String, Object?>);
      expect(c.machinePrivateKey, a.machinePrivateKey);
      expect(c.nodePrivateKey, a.nodePrivateKey);
      expect(c.hostname, a.hostname);
      expect(c.networkLockPrivateKey, a.networkLockPrivateKey);
      expect(c.nodePublicKey, a.nodePublicKey);
    });

    test('copyWith swaps the node key and recomputes the public key', () async {
      final a = await TailscaleIdentity.generate(hostname: 'phone');
      final next = encodeKey(KeyPrefix.privateKey, newCurve25519PrivateKey());
      final b = a.copyWith(nodePrivateKey: next, hostname: 'Phone-2');
      expect(b.nodePrivateKey, next);
      expect(b.nodePublicKey, publicKeyText(next, KeyPrefix.nodePublic));
      expect(b.machinePrivateKey, a.machinePrivateKey);
      expect(b.networkLockPrivateKey, a.networkLockPrivateKey);
      expect(b.hostname, 'phone-2');
    });

    test('older JSON without a tailnet-lock key still loads', () {
      final key = encodeKey(KeyPrefix.privateKey, newCurve25519PrivateKey());
      final id = TailscaleIdentity.fromJson({'machine_private_key': key, 'node_private_key': key, 'hostname': 'h'});
      expect(id.networkLockPrivateKey, isNull);
      expect(id.networkLockPublicKey, isNull);
    });

    test('rejects malformed keys; toString never shows keys', () async {
      expect(
        () => TailscaleIdentity.fromJson({'machine_private_key': 'privkey:00', 'node_private_key': 'privkey:00'}),
        throwsFormatException,
      );
      final a = await TailscaleIdentity.generate(hostname: 'phone');
      expect(a.toString(), isNot(contains('privkey')));
    });

    test('normalizeHostname makes a DNS label', () {
      expect(TailscaleIdentity.normalizeHostname('  Alice’s iPhone!! '), 'alice-s-iphone');
      expect(TailscaleIdentity.normalizeHostname('host.example.com'), 'host');
      expect(TailscaleIdentity.normalizeHostname('---'), 'tailsplit');
      expect(TailscaleIdentity.normalizeHostname('a' * 80), hasLength(63));
    });
  });

  group('snapshot JSON', () {
    test('round-trip keeps every field', () {
      final s = _snapshot();
      final back = TailnetSnapshot.fromJson(jsonDecode(jsonEncode(s.toJson())) as Map<String, Object?>);
      expect(back.toJson(), s.toJson());
      expect(back.keyExpiry, DateTime.utc(2027, 5, 9, 7));
      expect(back.exitNodes.single.lastSeen, DateTime.utc(2026, 11, 10, 4));
      expect(back.machineAuthorized, isFalse);
    });

    test('discovery diagnostics survive JSON and snapshot copies', () {
      final counts = {
        'peer_count': 4,
        'exit_route_peer_count': 2,
        'advertised_exit_route_peer_count': 3,
        'primary_exit_route_peer_count': 1,
        'missing_allowed_ips_peer_count': 1,
        'changed_peer_count': 4,
        'peer_list_present': false,
      };
      final snapshot = TailnetSnapshot.fromJson({
        ..._snapshot().toJson(),
        'discovery_diagnostics': counts,
      });
      final copy = snapshot.copyWith(fetchedAt: DateTime.utc(2026, 11, 11));
      expect(copy.discoveryDiagnostics!.toJson(), counts);
      final back = TailnetSnapshot.fromJson(jsonDecode(jsonEncode(copy.toJson())) as Map<String, Object?>);
      expect(back.discoveryDiagnostics!.toJson(), counts);
      expect(back.discoveryDiagnostics!.summary, copy.discoveryDiagnostics!.summary);
    });

    test('minimal JSON has sane defaults', () {
      final s = TailnetSnapshot.fromJson({'network_name': 'example.com', 'exit_nodes': []});
      expect(s.domainName, 'example.com');
      expect(s.keyExpiry, isNull);
      expect(s.machineAuthorized, isTrue);
      expect(s.discoveryDiagnostics, isNull);
      expect(s.userId, 0);
      final p = TailnetPeer.fromJson({'stable_id': 'x'});
      expect(p.addresses, isEmpty);
      expect(p.online, isFalse);
      expect(p.os, isNull);
    });
  });

  group('TsnetState', () {
    late Directory dir;
    setUp(() async => dir = await Directory.systemTemp.createTemp('tsnet_state_test'));
    tearDown(() async => dir.delete(recursive: true));

    test('writes the FileStore layout tsnet reads', () async {
      final identity = await TailscaleIdentity.generate(hostname: 'phone');
      await TsnetState.write(stateDirectory: dir, identity: identity, snapshot: _snapshot());
      final store = jsonDecode(await File('${dir.path}/tailscaled.state').readAsString()) as Map<String, Object?>;
      String value(String k) => utf8.decode(base64.decode(store[k]! as String));
      expect(store, hasLength(4));
      expect(value('_machinekey'), identity.machinePrivateKey);
      final profileKey = value('_current-profile');
      expect(profileKey, matches(RegExp(r'^profile-[0-9a-f]{4}$')));
      final profiles = jsonDecode(value('_profiles')) as Map<String, Object?>;
      final profile = profiles[profileKey.substring(8)]! as Map<String, Object?>;
      expect(profile['Key'], profileKey);
      expect(profile['NodeID'], 'nSELF1001CNTRL');
      expect(profile['ControlURL'], '');
      expect(profile['Name'], 'alice@example.com');
      expect((profile['UserProfile']! as Map)['ID'], 4242);
      expect(profile['NetworkProfile'], {
        'MagicDNSName': 'tail4f2a1c.ts.net',
        'DomainName': 'alice@example.com',
        'DisplayName': "Alice's Tailnet",
      });
      final prefs = jsonDecode(value(profileKey)) as Map<String, Object?>;
      expect(prefs['LoggedOut'], isFalse);
      expect(prefs['WantRunning'], isTrue);
      expect(prefs['Hostname'], 'phone');
      final persist = prefs['Config']! as Map<String, Object?>;
      expect(persist['PrivateNodeKey'], identity.nodePrivateKey);
      expect(persist['NetworkLockKey'], identity.networkLockPrivateKey);
      expect(persist['NodeID'], 'nSELF1001CNTRL');
      expect(dir.listSync().map(_name), ['tailscaled.state']);
    });

    test('readNodePrivateKey round-trips and tolerates missing or corrupt state', () async {
      expect(await TsnetState.readNodePrivateKey(dir), isNull);
      final identity = await TailscaleIdentity.generate(hostname: 'phone');
      await TsnetState.write(
        stateDirectory: dir,
        identity: identity,
        snapshot: _snapshot(),
        controlUrl: Uri.parse('https://hs.example.com/'),
      );
      expect(await TsnetState.readNodePrivateKey(dir), identity.nodePrivateKey);
      expect(await TsnetState.readLoggedOut(dir), isFalse);
      await File('${dir.path}/tailscaled.state').writeAsString('{not json');
      expect(await TsnetState.readNodePrivateKey(dir), isNull);
    });

    test('clear removes tsnet files only', () async {
      final identity = await TailscaleIdentity.generate(hostname: 'phone');
      await TsnetState.write(stateDirectory: dir, identity: identity, snapshot: _snapshot());
      await File('${dir.path}/tailscaled.log.conf').writeAsString('{}');
      await File('${dir.path}/tailscaled.state.tmp123').writeAsString('');
      await Directory('${dir.path}/tka-profiles/ab12').create(recursive: true);
      await File('${dir.path}/keep.txt').writeAsString('mine');
      await TsnetState.clear(dir);
      expect(dir.listSync().map(_name), ['keep.txt']);
      expect(await TsnetState.readNodePrivateKey(dir), isNull);
      await TsnetState.clear(Directory('${dir.path}/missing'));
    });
  });
}
