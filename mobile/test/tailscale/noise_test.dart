// Vectors in fixtures/noise_vectors.json come from a throwaway Go test that
// drives the pinned control/controlbase package (deterministic randomness
// via testing/cryptotest) over net.Pipe and records the wire bytes.

import 'dart:convert';
import 'dart:io';
import 'dart:typed_data';

import 'package:flutter_test/flutter_test.dart';
import 'package:tailsplit_mobile/src/tailscale/keys.dart';
import 'package:tailsplit_mobile/src/tailscale/noise.dart';

Map<String, Object?> _vectors() => (jsonDecode(
      File('test/tailscale/fixtures/noise_vectors.json').readAsStringSync(),
    ) as Map)
        .cast<String, Object?>();

Uint8List _hex(Object? v) => hexDecode(v! as String);

void main() {
  final v = _vectors();

  group('key encodings match types/key', () {
    for (final (i, k) in (v['keys']! as List).cast<Map>().indexed) {
      test('key set $i', () async {
        final raw = _hex(k['raw']);
        expect(encodeKey(KeyPrefix.privateKey, raw), k['node_private']);
        expect(publicKeyText(k['node_private'] as String, KeyPrefix.nodePublic), k['node_public']);
        expect(publicKeyText(k['node_private'] as String, KeyPrefix.machinePublic), k['machine_public']);
        expect(networkLockPublicKeyText(k['nl_private'] as String), k['nl_public']);
        expect(
          encodeKey(KeyPrefix.nlPrivate, await networkLockPrivateKeyFromSeed(_hex(k['nl_seed']))),
          k['nl_private'],
        );
        expect(parsePrivateKey(k['node_private'] as String), raw);
      });
    }

    test('zero keys', () {
      expect(zeroNodePublicKey, v['zero_node_public']);
      expect(zeroDiscoPublicKey, v['zero_disco_public']);
    });

    test('generated tailnet-lock key layout is seed || ed25519 public', () async {
      final nl = await newNetworkLockPrivateKey();
      expect(nl, hasLength(64));
    });

    test('decodeKey rejects wrong prefix and size without echoing the key', () {
      const secret = 'privkey:00112233';
      expect(
        () => decodeKey(secret, KeyPrefix.privateKey, 32),
        throwsA(isA<FormatException>().having((e) => e.message, 'message', isNot(contains('00112233')))),
      );
      expect(() => decodeKey('nodekey:${'00' * 32}', KeyPrefix.privateKey, 32), throwsFormatException);
    });

    test('generated private keys are clamped', () {
      for (var i = 0; i < 20; i++) {
        final k = newCurve25519PrivateKey();
        expect(k[0] & 7, 0);
        expect(k[31] & 0x80, 0);
        expect(k[31] & 0x40, 0x40);
      }
    });
  });

  group('noise', () {
    test('nonce counter is big-endian in bytes 4..12', () {
      expect(NoiseCipher.nonceFor(0), Uint8List(12));
      expect(NoiseCipher.nonceFor(1), [0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 1]);
      expect(NoiseCipher.nonceFor(0x0102030405060708), [0, 0, 0, 0, 1, 2, 3, 4, 5, 6, 7, 8]);
    });

    test('client handshake and transport match controlbase byte for byte', () {
      final machine = parsePrivateKey(v['machine_private']! as String);
      final controlPublic = decodeKey(v['control_public']! as String, KeyPrefix.machinePublic, 32);
      final hs = NoiseClientHandshake.start(
        machinePrivate: machine,
        controlPublic: controlPublic,
        protocolVersion: v['protocol_version']! as int,
        ephemeralPrivate: clamp25519(_hex(v['client_ephemeral_seed'])),
      );
      expect(hexEncode(hs.initiation), v['initiation']);

      final session = hs.finish(_hex(v['response']));
      expect(hexEncode(session.handshakeHash), v['handshake_hash']);

      expect(hexEncode(session.tx.encryptFrames(_hex(v['c2s_plain_1']))), v['c2s_wire_1']);
      // 9000 bytes: split into 4077-byte plaintext frames like Conn.Write.
      expect(hexEncode(session.tx.encryptFrames(_hex(v['c2s_plain_2']))), v['c2s_wire_2']);
      expect(session.tx.counter, 4);

      // Feed the server's bytes in awkward chunk sizes.
      final decoder = NoiseFrameDecoder(session.rx);
      final wire = _hex(v['s2c_wire']);
      final plain = BytesBuilder();
      for (var i = 0; i < wire.length; i += 777) {
        for (final chunk in decoder.add(wire.sublist(i, i + 777 > wire.length ? wire.length : i + 777))) {
          plain.add(chunk);
        }
      }
      expect(hexEncode(plain.takeBytes()), v['s2c_plain']);
    });

    test('tampered response fails authentication', () {
      final hs = NoiseClientHandshake.start(
        machinePrivate: parsePrivateKey(v['machine_private']! as String),
        controlPublic: decodeKey(v['control_public']! as String, KeyPrefix.machinePublic, 32),
        protocolVersion: 142,
        ephemeralPrivate: clamp25519(_hex(v['client_ephemeral_seed'])),
      );
      final response = _hex(v['response']);
      response[50] ^= 1;
      expect(() => hs.finish(response), throwsA(isA<NoiseException>()));
    });

    test('decoder rejects non-record frames and refuses further input', () {
      final hs = NoiseClientHandshake.start(
        machinePrivate: parsePrivateKey(v['machine_private']! as String),
        controlPublic: decodeKey(v['control_public']! as String, KeyPrefix.machinePublic, 32),
        protocolVersion: 142,
        ephemeralPrivate: clamp25519(_hex(v['client_ephemeral_seed'])),
      );
      final session = hs.finish(_hex(v['response']));
      final decoder = NoiseFrameDecoder(session.rx);
      expect(() => decoder.add([3, 0, 1, 0x41]), throwsA(isA<NoiseException>()));
      expect(() => decoder.add(_hex(v['s2c_wire'])), throwsA(isA<NoiseException>()));
    });
  });
}
