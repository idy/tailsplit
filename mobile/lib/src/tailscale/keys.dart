import 'dart:math';
import 'dart:typed_data';

import 'package:cryptography/cryptography.dart';
import 'package:cryptography/dart.dart';

/// Text encodings from tailscale.com/types/key (lowercase hex after a type
/// prefix). Machine and node private keys share the `privkey:` prefix.
abstract final class KeyPrefix {
  static const privateKey = 'privkey:';
  static const machinePublic = 'mkey:';
  static const nodePublic = 'nodekey:';
  static const discoPublic = 'discokey:';
  static const nlPrivate = 'nlpriv:';
  static const nlPublic = 'nlpub:';
}

final Random _random = Random.secure();

/// Cryptographically secure random bytes.
Uint8List randomBytes(int length) {
  final out = Uint8List(length);
  for (var i = 0; i < length; i++) {
    out[i] = _random.nextInt(256);
  }
  return out;
}

const _hexDigits = '0123456789abcdef';

String hexEncode(List<int> bytes) {
  final out = StringBuffer();
  for (final b in bytes) {
    out
      ..write(_hexDigits[(b >> 4) & 0xf])
      ..write(_hexDigits[b & 0xf]);
  }
  return out.toString();
}

Uint8List hexDecode(String hex) {
  if (hex.length.isOdd) throw const FormatException('hex string has odd length');
  final out = Uint8List(hex.length ~/ 2);
  for (var i = 0; i < out.length; i++) {
    out[i] = (_nibble(hex.codeUnitAt(2 * i)) << 4) | _nibble(hex.codeUnitAt(2 * i + 1));
  }
  return out;
}

int _nibble(int c) {
  if (c >= 0x30 && c <= 0x39) return c - 0x30;
  if (c >= 0x61 && c <= 0x66) return c - 0x61 + 10;
  if (c >= 0x41 && c <= 0x46) return c - 0x41 + 10;
  throw const FormatException('invalid hex digit');
}

/// `prefix` followed by lowercase hex, like Go's `appendHexKey`.
String encodeKey(String prefix, List<int> raw) => '$prefix${hexEncode(raw)}';

/// Parses a prefixed hex key, like Go's `parseHex`. The error message never
/// contains the key text.
Uint8List decodeKey(String text, String prefix, int length) {
  if (!text.startsWith(prefix)) {
    throw FormatException('key does not have the expected "$prefix" prefix');
  }
  final hex = text.substring(prefix.length);
  if (hex.length != length * 2) {
    throw FormatException('key has the wrong size: got ${hex.length ~/ 2} bytes, want $length');
  }
  return hexDecode(hex);
}

bool isZeroKey(List<int> raw) => raw.every((b) => b == 0);

/// Curve25519 private key clamping (`clamp25519Private` in types/key).
Uint8List clamp25519(Uint8List key) {
  key[0] &= 248;
  key[31] = (key[31] & 127) | 64;
  return key;
}

/// A new clamped Curve25519 private key, as `key.NewMachine`/`key.NewNode`.
Uint8List newCurve25519PrivateKey() => clamp25519(randomBytes(32));

final Uint8List _basePoint = Uint8List(32)..[0] = 9;
const _x25519 = DartX25519();

/// X25519(private, public). Throws if the inputs are not 32 bytes.
Uint8List x25519(List<int> privateKey, List<int> publicKey) {
  if (privateKey.length != 32 || publicKey.length != 32) {
    throw ArgumentError('X25519 keys must be 32 bytes');
  }
  final keyPair = SimpleKeyPairData(
    Uint8List.fromList(privateKey),
    // The pure-Dart implementation does not use this value for the shared
    // secret; it only needs a well-formed placeholder.
    publicKey: SimplePublicKey(Uint8List(32), type: KeyPairType.x25519),
    type: KeyPairType.x25519,
  );
  final secret = _x25519.sharedSecretSync(
    keyPairData: keyPair,
    remotePublicKey: SimplePublicKey(Uint8List.fromList(publicKey), type: KeyPairType.x25519),
  );
  if (secret is! SecretKeyData) throw StateError('unexpected X25519 secret type');
  return Uint8List.fromList(secret.bytes);
}

/// The Curve25519 public key for [privateKey] (`curve25519.ScalarBaseMult`).
Uint8List curve25519PublicKey(List<int> privateKey) => x25519(privateKey, _basePoint);

/// A new tailnet-lock key in Go's ed25519 private key layout: seed || public.
Future<Uint8List> newNetworkLockPrivateKey() => networkLockPrivateKeyFromSeed(randomBytes(32));

/// Go's `ed25519.NewKeyFromSeed` layout for [seed].
Future<Uint8List> networkLockPrivateKeyFromSeed(List<int> seed) async {
  final keyPair = await DartEd25519().newKeyPairFromSeed(seed);
  final public = await keyPair.extractPublicKey();
  return Uint8List.fromList([...seed, ...public.bytes]);
}

/// Parses `privkey:<64 hex>` into 32 raw bytes.
Uint8List parsePrivateKey(String text) => decodeKey(text, KeyPrefix.privateKey, 32);

/// `nodekey:`/`mkey:` text for the public half of a `privkey:` text.
String publicKeyText(String privateKeyText, String publicPrefix) =>
    encodeKey(publicPrefix, curve25519PublicKey(parsePrivateKey(privateKeyText)));

/// `nlpub:` text for an `nlpriv:` text (the public key is the last 32 bytes).
String networkLockPublicKeyText(String nlPrivateText) {
  final raw = decodeKey(nlPrivateText, KeyPrefix.nlPrivate, 64);
  return encodeKey(KeyPrefix.nlPublic, raw.sublist(32));
}

/// Zero-valued public key texts as Go marshals them.
final String zeroNodePublicKey = encodeKey(KeyPrefix.nodePublic, Uint8List(32));
final String zeroDiscoPublicKey = encodeKey(KeyPrefix.discoPublic, Uint8List(32));
