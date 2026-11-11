import 'keys.dart';

/// The embedded Tailscale device: its machine key, current node key and
/// tailnet-lock key. Contains private keys; persist it like a password and
/// never log it.
class TailscaleIdentity {
  TailscaleIdentity({
    required this.machinePrivateKey,
    required this.nodePrivateKey,
    required this.hostname,
    this.networkLockPrivateKey,
  }) {
    parsePrivateKey(machinePrivateKey);
    parsePrivateKey(nodePrivateKey);
    final nl = networkLockPrivateKey;
    if (nl != null) decodeKey(nl, KeyPrefix.nlPrivate, 64);
  }

  /// `privkey:<hex>`; tsnet's `_machinekey`.
  final String machinePrivateKey;

  /// `privkey:<hex>`; tsnet's `Persist.PrivateNodeKey`.
  final String nodePrivateKey;

  /// DNS label sent as `Hostinfo.Hostname` (and sing-box's `hostname`).
  final String hostname;

  /// `nlpriv:<hex>`; tsnet's `Persist.NetworkLockKey`. Null only for
  /// identities persisted before this field existed.
  final String? networkLockPrivateKey;

  static Future<TailscaleIdentity> generate({required String hostname}) async {
    return TailscaleIdentity(
      machinePrivateKey: encodeKey(KeyPrefix.privateKey, newCurve25519PrivateKey()),
      nodePrivateKey: encodeKey(KeyPrefix.privateKey, newCurve25519PrivateKey()),
      hostname: normalizeHostname(hostname),
      networkLockPrivateKey: encodeKey(KeyPrefix.nlPrivate, await newNetworkLockPrivateKey()),
    );
  }

  /// `nodekey:<hex>`.
  late final String nodePublicKey = publicKeyText(nodePrivateKey, KeyPrefix.nodePublic);

  /// `mkey:<hex>`.
  late final String machinePublicKey = publicKeyText(machinePrivateKey, KeyPrefix.machinePublic);

  /// `nlpub:<hex>`, or null without a tailnet-lock key.
  String? get networkLockPublicKey {
    final nl = networkLockPrivateKey;
    return nl == null ? null : networkLockPublicKeyText(nl);
  }

  TailscaleIdentity copyWith({String? nodePrivateKey, String? hostname}) => TailscaleIdentity(
        machinePrivateKey: machinePrivateKey,
        nodePrivateKey: nodePrivateKey ?? this.nodePrivateKey,
        hostname: hostname == null ? this.hostname : normalizeHostname(hostname),
        networkLockPrivateKey: networkLockPrivateKey,
      );

  Map<String, Object?> toJson() => {
        'machine_private_key': machinePrivateKey,
        'node_private_key': nodePrivateKey,
        'hostname': hostname,
        if (networkLockPrivateKey != null) 'network_lock_private_key': networkLockPrivateKey,
      };

  factory TailscaleIdentity.fromJson(Map<String, Object?> json) {
    final machine = json['machine_private_key'];
    final node = json['node_private_key'];
    if (machine is! String || node is! String) {
      throw const FormatException('Tailscale identity is missing its keys');
    }
    return TailscaleIdentity(
      machinePrivateKey: machine,
      nodePrivateKey: node,
      hostname: json['hostname'] as String? ?? 'tailsplit',
      networkLockPrivateKey: json['network_lock_private_key'] as String?,
    );
  }

  /// Lowercase DNS label: letters, digits and inner hyphens, at most 63.
  static String normalizeHostname(String value) {
    final label = value
        .trim()
        .toLowerCase()
        .split('.')
        .first
        .replaceAll(RegExp(r'[^a-z0-9-]+'), '-')
        .replaceAll(RegExp(r'-+'), '-')
        .replaceAll(RegExp(r'^-+|-+$'), '');
    final short = label.length > 63 ? label.substring(0, 63).replaceAll(RegExp(r'-+$'), '') : label;
    return short.isEmpty ? 'tailsplit' : short;
  }

  @override
  String toString() => 'TailscaleIdentity($hostname)';
}
