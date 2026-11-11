// Noise IK as used by Tailscale's control protocol (control/controlbase in
// the pinned tailscale fork): Noise_IK_25519_ChaChaPoly_BLAKE2s with a
// "Tailscale Control Protocol v<N>" prologue.
//
// Unlike the Noise spec, controlbase encodes the transport nonce counter as a
// BIG-endian uint64 in bytes 4..12 of the ChaCha20-Poly1305 nonce.

import 'dart:convert';
import 'dart:typed_data';

import 'package:cryptography/cryptography.dart';
import 'package:cryptography/dart.dart';

import 'keys.dart';

const noiseProtocolName = 'Noise_IK_25519_ChaChaPoly_BLAKE2s';
const noiseProtocolVersionPrefix = 'Tailscale Control Protocol v';

const msgTypeInitiation = 1;
const msgTypeResponse = 2;
const msgTypeError = 3;
const msgTypeRecord = 4;

/// Header size of every message except the initiation.
const noiseHeaderLength = 3;
const initiationHeaderLength = 5;
const initiationMessageLength = 101;
const responseMessageLength = 51;

/// Largest frame on the wire, header included.
const maxNoiseMessageSize = 4096;
const maxCiphertextSize = maxNoiseMessageSize - noiseHeaderLength;
const aeadOverhead = 16;
const maxPlaintextSize = maxCiphertextSize - aeadOverhead;

const _blake2s = DartBlake2s();
const _aead = DartChacha20.poly1305Aead();

/// Raised for any handshake or transport failure. Never contains key bytes.
class NoiseException implements Exception {
  const NoiseException(this.message);
  final String message;

  @override
  String toString() => 'NoiseException: $message';
}

Uint8List blake2s(List<int> data) => Uint8List.fromList(_blake2s.hashSync(data).bytes);

const _blake2sBlockSize = 64;

/// HMAC-BLAKE2s (RFC 2104 with a 64-byte block).
Uint8List hmacBlake2s(List<int> key, List<int> message) {
  final k = key.length > _blake2sBlockSize ? blake2s(key) : key;
  final block = Uint8List(_blake2sBlockSize)..setAll(0, k);
  final inner = Uint8List(_blake2sBlockSize + message.length);
  final outer = Uint8List(_blake2sBlockSize + 32);
  for (var i = 0; i < _blake2sBlockSize; i++) {
    inner[i] = block[i] ^ 0x36;
    outer[i] = block[i] ^ 0x5c;
  }
  inner.setAll(_blake2sBlockSize, message);
  outer.setAll(_blake2sBlockSize, blake2s(inner));
  return blake2s(outer);
}

/// HKDF-BLAKE2s producing two 32-byte outputs, as Go's
/// `hkdf.New(newBLAKE2s, secret, salt, nil)` read twice.
(Uint8List, Uint8List) hkdf2(List<int> salt, List<int> secret) {
  final prk = hmacBlake2s(salt, secret);
  final t1 = hmacBlake2s(prk, const [1]);
  final t2 = hmacBlake2s(prk, [...t1, 2]);
  return (t1, t2);
}

Uint8List aeadSeal(List<int> key, List<int> nonce, List<int> plaintext, List<int> aad) {
  final box = _aead.encryptSync(
    plaintext,
    secretKey: SecretKeyData(key),
    nonce: nonce,
    aad: aad,
  );
  final out = Uint8List(box.cipherText.length + aeadOverhead)
    ..setAll(0, box.cipherText)
    ..setAll(box.cipherText.length, box.mac.bytes);
  return out;
}

Uint8List aeadOpen(List<int> key, List<int> nonce, List<int> ciphertext, List<int> aad) {
  if (ciphertext.length < aeadOverhead) throw const NoiseException('ciphertext too short');
  final split = ciphertext.length - aeadOverhead;
  try {
    return Uint8List.fromList(_aead.decryptSync(
      SecretBox(
        ciphertext.sublist(0, split),
        nonce: nonce,
        mac: Mac(ciphertext.sublist(split)),
      ),
      secretKey: SecretKeyData(key),
      aad: aad,
    ));
  } on SecretBoxAuthenticationError {
    throw const NoiseException('message authentication failed');
  }
}

final Uint8List _zeroNonce = Uint8List(12);

/// `protocolVersionPrologue` in controlbase.
Uint8List protocolVersionPrologue(int version) =>
    Uint8List.fromList(utf8.encode('$noiseProtocolVersionPrefix$version'));

/// controlbase's `symmetricState`.
class SymmetricState {
  SymmetricState() {
    h = blake2s(utf8.encode(noiseProtocolName));
    ck = Uint8List.fromList(h);
  }

  late Uint8List h;
  late Uint8List ck;

  void mixHash(List<int> data) {
    h = blake2s([...h, ...data]);
  }

  /// MixKey(DH(priv, pub)); returns the single-use handshake cipher key.
  Uint8List mixDh(List<int> privateKey, List<int> publicKey) {
    final shared = x25519(privateKey, publicKey);
    final (newCk, k) = hkdf2(ck, shared);
    ck = newCk;
    return k;
  }

  Uint8List encryptAndHash(List<int> key, List<int> plaintext) {
    final ciphertext = aeadSeal(key, _zeroNonce, plaintext, h);
    mixHash(ciphertext);
    return ciphertext;
  }

  Uint8List decryptAndHash(List<int> key, List<int> ciphertext) {
    final plaintext = aeadOpen(key, _zeroNonce, ciphertext, h);
    mixHash(ciphertext);
    return plaintext;
  }

  (Uint8List, Uint8List) split() => hkdf2(ck, const []);
}

/// One direction of an established Noise session.
class NoiseCipher {
  NoiseCipher(this._key);

  final Uint8List _key;
  int _counter = 0;

  /// Messages processed so far (the next nonce value).
  int get counter => _counter;

  /// 4 zero bytes, then the counter as big-endian uint64.
  static Uint8List nonceFor(int counter) {
    final nonce = Uint8List(12);
    ByteData.sublistView(nonce).setUint64(4, counter, Endian.big);
    return nonce;
  }

  // Dart ints are signed 64-bit; 2^63 messages can never be reached, so
  // controlbase's 2^64-1 nonce exhaustion cannot happen here either.
  Uint8List seal(List<int> plaintext) => aeadSeal(_key, nonceFor(_counter++), plaintext, const []);

  Uint8List open(List<int> ciphertext) => aeadOpen(_key, nonceFor(_counter++), ciphertext, const []);

  /// Encrypts [plaintext] into one or more record frames (type 4), split at
  /// [maxPlaintextSize] like `controlbase.Conn.Write`.
  Uint8List encryptFrames(List<int> plaintext) {
    final out = BytesBuilder(copy: false);
    var offset = 0;
    do {
      final end = offset + maxPlaintextSize < plaintext.length ? offset + maxPlaintextSize : plaintext.length;
      final ciphertext = seal(plaintext.sublist(offset, end));
      out
        ..add(Uint8List.fromList([msgTypeRecord, ciphertext.length >> 8, ciphertext.length & 0xff]))
        ..add(ciphertext);
      offset = end;
    } while (offset < plaintext.length);
    return out.takeBytes();
  }
}

/// Client side of the handshake, split like `controlbase.ClientDeferred`.
class NoiseClientHandshake {
  NoiseClientHandshake._(
      this._state, this.initiation, this._machinePrivate, this._ephemeralPrivate, this.protocolVersion);

  /// Builds the 101-byte initiation message (`-> e, es, s, ss`).
  /// [ephemeralPrivate] exists for deterministic tests only.
  factory NoiseClientHandshake.start({
    required Uint8List machinePrivate,
    required Uint8List controlPublic,
    required int protocolVersion,
    Uint8List? ephemeralPrivate,
  }) {
    final state = SymmetricState()
      ..mixHash(protocolVersionPrologue(protocolVersion))
      ..mixHash(controlPublic);
    final ephemeral = ephemeralPrivate ?? newCurve25519PrivateKey();
    final ephemeralPublic = curve25519PublicKey(ephemeral);
    final init = Uint8List(initiationMessageLength);
    ByteData.sublistView(init)
      ..setUint16(0, protocolVersion, Endian.big)
      ..setUint8(2, msgTypeInitiation)
      ..setUint16(3, initiationMessageLength - initiationHeaderLength, Endian.big);
    init.setAll(5, ephemeralPublic);
    state.mixHash(ephemeralPublic);
    var k = state.mixDh(ephemeral, controlPublic);
    init.setAll(37, state.encryptAndHash(k, curve25519PublicKey(machinePrivate)));
    k = state.mixDh(machinePrivate, controlPublic);
    init.setAll(85, state.encryptAndHash(k, const []));
    return NoiseClientHandshake._(state, init, machinePrivate, ephemeral, protocolVersion);
  }

  final SymmetricState _state;
  final Uint8List initiation;
  final Uint8List _machinePrivate;
  final Uint8List _ephemeralPrivate;
  final int protocolVersion;
  bool _finished = false;

  /// Processes the server's 51-byte response (`<- e, ee, se`).
  NoiseSession finish(Uint8List response) {
    if (_finished) throw StateError('handshake already finished');
    _finished = true;
    if (response.length != responseMessageLength || response[0] != msgTypeResponse) {
      throw const NoiseException('malformed handshake response');
    }
    final length = (response[1] << 8) | response[2];
    if (length != responseMessageLength - noiseHeaderLength) {
      throw NoiseException('wrong length $length received for handshake response');
    }
    final controlEphemeral = response.sublist(3, 35);
    _state.mixHash(controlEphemeral);
    _state.mixDh(_ephemeralPrivate, controlEphemeral);
    final k = _state.mixDh(_machinePrivate, controlEphemeral);
    _state.decryptAndHash(k, response.sublist(35));
    final (c1, c2) = _state.split();
    return NoiseSession(
      tx: NoiseCipher(c1),
      rx: NoiseCipher(c2),
      handshakeHash: Uint8List.fromList(_state.h),
      protocolVersion: protocolVersion,
    );
  }
}

class NoiseSession {
  NoiseSession({
    required this.tx,
    required this.rx,
    required this.handshakeHash,
    required this.protocolVersion,
  });

  final NoiseCipher tx;
  final NoiseCipher rx;
  final Uint8List handshakeHash;
  final int protocolVersion;
}

/// Splits a raw byte stream into record frames and decrypts them. Once a
/// frame fails, the decoder refuses further input (like controlbase).
class NoiseFrameDecoder {
  NoiseFrameDecoder(this._rx);

  final NoiseCipher _rx;
  Uint8List _pending = Uint8List(0);
  bool _failed = false;

  /// Returns the plaintext of every complete frame buffered so far.
  List<Uint8List> add(List<int> data) {
    if (_failed) throw const NoiseException('noise session is no longer usable');
    final buffer = _pending.isEmpty
        ? Uint8List.fromList(data)
        : (Uint8List(_pending.length + data.length)
          ..setAll(0, _pending)
          ..setAll(_pending.length, data));
    final out = <Uint8List>[];
    var offset = 0;
    try {
      while (buffer.length - offset >= noiseHeaderLength) {
        final type = buffer[offset];
        final length = (buffer[offset + 1] << 8) | buffer[offset + 2];
        if (type != msgTypeRecord) {
          throw NoiseException('received message with unexpected type $type, want $msgTypeRecord');
        }
        if (noiseHeaderLength + length > maxNoiseMessageSize) {
          throw const NoiseException('peer sent a Noise frame larger than allowed');
        }
        if (buffer.length - offset < noiseHeaderLength + length) break;
        final start = offset + noiseHeaderLength;
        offset = start + length;
        final plaintext = _rx.open(Uint8List.sublistView(buffer, start, offset));
        if (plaintext.isNotEmpty) out.add(plaintext);
      }
    } on NoiseException {
      _failed = true;
      rethrow;
    }
    _pending = offset < buffer.length ? Uint8List.fromList(buffer.sublist(offset)) : Uint8List(0);
    return out;
  }
}
