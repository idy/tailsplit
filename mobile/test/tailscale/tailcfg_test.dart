import 'dart:async';
import 'dart:convert';
import 'dart:io';
import 'dart:typed_data';

import 'package:flutter_test/flutter_test.dart';
import 'package:tailsplit_mobile/src/tailscale/control_conn.dart';
import 'package:tailsplit_mobile/src/tailscale/keys.dart';
import 'package:tailsplit_mobile/src/tailscale/tailcfg.dart';
import 'package:tailsplit_mobile/src/tailscale/tailscale.dart';

Map<String, Object?> _fixture() =>
    (jsonDecode(File('test/tailscale/fixtures/map_response.json').readAsStringSync()) as Map)
        .cast<String, Object?>();

Uint8List _frame(Object json) {
  final body = utf8.encode(jsonEncode(json));
  return Uint8List.fromList([
    ...(ByteData(4)..setUint32(0, body.length, Endian.little)).buffer.asUint8List(),
    ...body,
  ]);
}

void main() {
  group('MapResponse parsing', () {
    final parsed = parseMapResponse(_fixture(), now: DateTime.utc(2026, 11, 10, 8));
    final s = parsed.snapshot;

    test('self node and tailnet', () {
      expect(s.selfName, 'tailsplit-ios');
      expect(s.selfFqdn, 'tailsplit-ios.tail4f2a1c.ts.net');
      expect(s.selfAddresses, ['100.101.7.42', 'fd7a:115c:a1e0::1']);
      expect(s.networkName, "Alice's Tailnet");
      expect(s.tailnetDisplayName, "Alice's Tailnet");
      expect(s.domainName, 'alice@example.com');
      expect(s.magicDnsSuffix, 'tail4f2a1c.ts.net');
      expect(s.userLoginName, 'alice@example.com');
      expect(s.userDisplayName, 'Alice Example');
      expect(s.userId, 4242);
      expect(s.userProfilePicUrl, 'https://example.com/alice.png');
      expect(s.stableNodeId, 'nSELF1001CNTRL');
      expect(s.keyExpiry, DateTime.utc(2027, 5, 9, 7));
      expect(s.machineAuthorized, isTrue);
      expect(s.fetchedAt, DateTime.utc(2026, 11, 10, 8));
      expect(parsed.selfStatus.keyExpired, isFalse);
    });

    test('exit nodes: 0.0.0.0/0 and/or ::/0, online first then by name', () {
      expect([for (final p in s.exitNodes) p.stableId], [
        'nBOB2005CNTRL', // ::/0 only, shared-in
        'nTOKYO2001CNTRL',
        'nMULLVAD2006CNTRL', // Online absent: unknown, sorted as offline
        'nSG2002CNTRL', // 0.0.0.0/0 only
        'nNONAME2007CNTRL',
      ]);
      final tokyo = s.exitNodes[1];
      expect(tokyo.name, 'Tokyo-VPS');
      expect(tokyo.fqdn, 'Tokyo-VPS.tail4f2a1c.ts.net');
      expect(tokyo.addresses, ['100.88.12.4', 'fd7a:115c:a1e0::88']);
      expect(tokyo.online, isTrue);
      expect(tokyo.os, 'linux');
      expect(tokyo.isExitNode, isTrue);
      final sg = s.exitNodes[3];
      expect(sg.online, isFalse);
      expect(sg.lastSeen, DateTime.utc(2026, 11, 10, 4));
      final noName = s.exitNodes[4];
      expect(noName.name, 'unnamed-exit');
      expect(noName.fqdn, '');
      expect(noName.lastSeen, isNull, reason: 'Go zero time is not a timestamp');
      expect(noName.os, isNull);
      expect(s.exitNodes.any((p) => p.name == 'laptop' || p.name == 'office-router'), isFalse);
    });

    test('initial map accepts PeersChanged when Peers is absent or empty', () {
      for (final emptyPeers in [null, <Object?>[]]) {
        final json = _fixture();
        json['PeersChanged'] = json['Peers'];
        json['Peers'] = emptyPeers;
        final snapshot = parseMapResponse(json).snapshot;
        expect(snapshot.exitNodes.map((p) => p.stableId), s.exitNodes.map((p) => p.stableId));
        expect(snapshot.discoveryDiagnostics!.changedPeerCount, 7);
        expect(snapshot.discoveryDiagnostics!.peerListPresent, emptyPeers != null);
      }
    });

    test('a non-empty full peer list takes precedence over PeersChanged', () {
      final json = _fixture();
      json['PeersChanged'] = json['Peers'];
      json['Peers'] = [(json['Peers'] as List).first]; // Laptop, no exit route.
      expect(parseMapResponse(json).snapshot.exitNodes, isEmpty);
    });

    test('route advertisements and capabilities do not grant an exit route', () {
      final json = _fixture();
      json['Peers'] = [
        {
          'ID': 99,
          'Addresses': ['100.64.0.99/32'],
          'PrimaryRoutes': ['0.0.0.0/0', '::/0'],
          'Hostinfo': {'RoutableIPs': ['0.0.0.0/0', '::/0']},
          'Capabilities': ['suggest-exit-node'],
          'CapMap': {'suggest-exit-node': []},
        },
      ];
      final snapshot = parseMapResponse(json).snapshot;
      expect(snapshot.exitNodes, isEmpty);
      final counts = snapshot.discoveryDiagnostics!;
      expect(counts.peerCount, 1);
      expect(counts.exitRoutePeerCount, 0);
      expect(counts.advertisedExitRoutePeerCount, 1);
      expect(counts.primaryExitRoutePeerCount, 1);
      expect(counts.missingAllowedIpsPeerCount, 1);
    });

    test('discovery diagnostics retain only counts and peer-list presence', () {
      final counts = s.discoveryDiagnostics!;
      expect(counts.toJson(), {
        'peer_count': 7,
        'exit_route_peer_count': 5,
        'advertised_exit_route_peer_count': 0,
        'primary_exit_route_peer_count': 0,
        'missing_allowed_ips_peer_count': 1,
        'changed_peer_count': 0,
        'peer_list_present': true,
      });
      expect(counts.summary, 'Netmap discovery: peers=7, exit-routes=5, advertised-exits=0, '
          'primary-exits=0, allowed-ips-missing=1, peer-changes=0, peer-list=true');
      expect(counts.toJson().values.every((v) => v is int || v is bool), isTrue);
      for (final privateValue in [s.selfName, s.networkName, s.userLoginName, s.stableNodeId, ...s.selfAddresses]) {
        expect(counts.summary, isNot(contains(privateValue)));
      }
    });

    test('an empty map reports zero counts without inventing exits', () {
      final json = _fixture()..remove('Peers');
      final snapshot = parseMapResponse(json).snapshot;
      expect(snapshot.exitNodes, isEmpty);
      expect(snapshot.discoveryDiagnostics!.peerCount, 0);
      expect(snapshot.discoveryDiagnostics!.exitRoutePeerCount, 0);
      expect(snapshot.discoveryDiagnostics!.peerListPresent, isFalse);
    });

    test('offersExitRoute', () {
      expect(offersExitRoute(null), isFalse, reason: 'nil AllowedIPs means Addresses (capver >= 112)');
      expect(offersExitRoute(['100.1.1.1/32', '10.0.0.0/8']), isFalse);
      expect(offersExitRoute(['0.0.0.0/0']), isTrue);
      expect(offersExitRoute(['::/0']), isTrue);
    });

    test('expired self key and missing node', () {
      final json = _fixture();
      (json['Node'] as Map)['KeyExpiry'] = '2026-11-01T00:00:00Z';
      expect(parseMapResponse(json).selfStatus.keyExpired, isTrue);
      final expired = _fixture();
      (expired['Node'] as Map)['Expired'] = true;
      expect(parseMapResponse(expired).selfStatus.keyExpired, isTrue);
      final noExpiry = _fixture();
      (noExpiry['Node'] as Map).remove('KeyExpiry');
      expect(parseMapResponse(noExpiry).snapshot.keyExpiry, isNull);
      expect(parseMapResponse(noExpiry).selfStatus.keyExpired, isFalse);
      expect(() => parseMapResponse({'KeepAlive': true}), throwsA(isA<MapResponseFormatException>()));
    });

    test('falls back to Domain and register login without display name / profiles', () {
      final json = _fixture();
      (json['Node'] as Map).remove('CapMap');
      json.remove('UserProfiles');
      final snap = parseMapResponse(json, fallbackLoginName: 'a@b.c', fallbackDisplayName: 'A').snapshot;
      expect(snap.networkName, 'alice@example.com');
      expect(snap.userLoginName, 'a@b.c');
      expect(snap.userDisplayName, 'A');
    });

    test('magicDnsSuffixOf', () {
      expect(magicDnsSuffixOf('a.tail1.ts.net.'), 'tail1.ts.net');
      expect(magicDnsSuffixOf('host'), '');
      expect(magicDnsSuffixOf(''), '');
    });
  });

  group('requests', () {
    const host = HostinfoFields(hostname: 'h', os: 'iOS', backendLogId: 'abc');

    test('register request mirrors Direct.doLogin', () {
      final json = registerRequestJson(nodeKey: 'nodekey:11', nlKey: 'nlpub:22', hostinfo: host);
      expect(json['Version'], 142);
      expect(json['OldNodeKey'], zeroNodePublicKey);
      expect(json['Expiry'], '0001-01-01T00:00:00Z');
      expect(json['Followup'], '');
      expect((json['Hostinfo'] as Map)['IPNVersion'], '1.102.1');
      expect((json['Hostinfo'] as Map)['BackendLogID'], 'abc');
      expect(json.containsKey('Auth'), isFalse);
      final logout = registerRequestJson(nodeKey: 'nodekey:11', hostinfo: host, expiry: goLogoutExpiry);
      expect(DateTime.parse(logout['Expiry']! as String), DateTime.fromMillisecondsSinceEpoch(123000, isUtc: true));
      expect(logout.containsKey('NLKey'), isFalse);
    });

    test('map request is a read-only stream without compression', () {
      final json = mapRequestJson(nodeKey: 'nodekey:11', hostinfo: host);
      expect(json['Stream'], isTrue);
      expect(json['Version'], greaterThanOrEqualTo(68));
      expect(json.containsKey('Compress'), isFalse);
      expect(json.containsKey('OmitPeers'), isFalse);
      expect(json.containsKey('Endpoints'), isFalse);
      expect(json['DiscoKey'], zeroDiscoPublicKey);
    });

    test('register response', () {
      final r = RegisterResponse.fromJson({
        'User': {'ID': 9, 'DisplayName': 'U'},
        'Login': {'LoginName': 'u@x', 'DisplayName': 'L'},
        'NodeKeyExpired': false,
        'MachineAuthorized': true,
        'AuthURL': '',
        'NodeKeySignature': null,
        'Error': '',
      });
      expect(r.userId, 9);
      expect(r.loginName, 'u@x');
      expect(r.machineAuthorized, isTrue);
      expect(r.hasNodeKeySignature, isFalse);
    });
  });

  group('framing', () {
    test('map frames: little-endian length prefix across chunk boundaries', () async {
      final bytes = [..._frame({'KeepAlive': true}), ..._frame(_fixture())];
      final chunks = <List<int>>[for (var i = 0; i < bytes.length; i += 333) bytes.sublist(i, i + 333 > bytes.length ? bytes.length : i + 333)];
      final messages = await decodeMapFrames(Stream.fromIterable(chunks)).toList();
      expect(messages, hasLength(2));
      expect(isKeepAlive(messages[0]), isTrue);
      expect((messages[1]['Node'] as Map)['StableID'], 'nSELF1001CNTRL');
    });

    test('map frames larger than the initial buffer', () async {
      final big = {'Padding': 'x' * 300000, 'N': 1};
      final bytes = [..._frame({'KeepAlive': true}), ..._frame(big), ..._frame({'N': 2})];
      final chunks = <List<int>>[
        for (var i = 0; i < bytes.length; i += 7001) bytes.sublist(i, i + 7001 > bytes.length ? bytes.length : i + 7001)
      ];
      final messages = await decodeMapFrames(Stream.fromIterable(chunks)).toList();
      expect([for (final m in messages) m['N']], [null, 1, 2]);
      expect((messages[1]['Padding']! as String).length, 300000);
    });

    test('compressed map frames are rejected with a clear error', () {
      final zstdLike = [4, 0, 0, 0, 0x28, 0xb5, 0x2f, 0xfd];
      expect(decodeMapFrames(Stream.value(zstdLike)).toList(), throwsA(isA<TailscaleControlException>()));
    });

    test('early payload is stripped; HTTP/2 bytes pass through', () async {
      final payload = utf8.encode('{"nodeKeyChallenge":"chalpub:00"}');
      final header = [...earlyPayloadMagic, 0, 0, 0, payload.length];
      final h2 = utf8.encode('PRI-ish settings frame bytes');
      final input = [...header, ...payload, ...h2];
      final chunks = [for (var i = 0; i < input.length; i += 4) input.sublist(i, i + 4 > input.length ? input.length : i + 4)];
      final out = await stripEarlyPayload(Stream.fromIterable(chunks)).expand((c) => c).toList();
      expect(out, h2);

      final plain = utf8.encode('\x00\x00\x12\x04\x00\x00\x00\x00\x00rest');
      expect(await stripEarlyPayload(Stream.value(plain)).expand((c) => c).toList(), plain);
    });

    test('redact removes URLs and keys', () {
      final text = redact('visit https://login.tailscale.com/a/abc123 with nodekey:${'ab' * 32} now');
      expect(text, 'visit <url> with <key> now');
      expect(redact('x' * 500).length, lessThanOrEqualTo(201));
    });
  });

  group('control endpoint ports (ts2021.NewClient rules)', () {
    test('default control: HTTP 80 then HTTPS 443', () {
      final e = ControlEndpoint(Uri.parse('https://controlplane.tailscale.com'));
      expect((e.httpPort, e.httpsPort), (80, 443));
      expect(e.authority, 'controlplane.tailscale.com');
    });
    test('explicit https port: HTTPS only', () {
      final e = ControlEndpoint(Uri.parse('https://hs.example.com:8443'));
      expect((e.httpPort, e.httpsPort), (null, 8443));
    });
    test('http with private host: no HTTPS', () {
      expect((ControlEndpoint(Uri.parse('http://127.0.0.1:8080')).httpPort, ControlEndpoint(Uri.parse('http://127.0.0.1:8080')).httpsPort), (8080, null));
      expect(ControlEndpoint(Uri.parse('http://192.168.1.5')).httpsPort, isNull);
      expect(ControlEndpoint(Uri.parse('http://hs.example.com:8080')).httpsPort, 443);
    });
  });

  test('client normalizes the control URL', () {
    expect(TailscaleControlClient().controlUrl.toString(), 'https://controlplane.tailscale.com');
    expect(TailscaleControlClient(controlUrl: Uri.parse('https://hs.example.com:8443/')).controlUrl.toString(),
        'https://hs.example.com:8443');
  });

  test('closed client refuses work', () async {
    final client = TailscaleControlClient(controlUrl: Uri.parse('http://127.0.0.1:9'));
    await client.close();
    final identity = await TailscaleIdentity.generate(hostname: 'x');
    await expectLater(client.fetchSnapshot(identity), throwsA(isA<TailscaleControlException>()));
    await expectLater(client.login(identity).toList(), throwsA(isA<TailscaleControlException>()));
  });

  test('unreachable control surfaces a TailscaleControlException without secrets', () async {
    final client = TailscaleControlClient(controlUrl: Uri.parse('http://127.0.0.1:9'));
    final identity = await TailscaleIdentity.generate(hostname: 'x');
    final error = await client.fetchSnapshot(identity).then<Object?>((_) => null, onError: (Object e) => e)
        .timeout(const Duration(seconds: 60));
    await client.close();
    expect(error, isA<TailscaleControlException>());
    expect(error.toString(), isNot(contains('privkey')));
    expect(error.toString(), isNot(contains(identity.nodePublicKey.substring(8))));
  }, timeout: const Timeout(Duration(seconds: 90)));
}
