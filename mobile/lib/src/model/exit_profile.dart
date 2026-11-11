import '../tailscale/tailscale.dart';

/// The phone's single exit: one embedded Tailscale device and its exit node.
class ExitProfile {
  const ExitProfile({
    required this.identity,
    this.snapshot,
    this.nodeId = '',
    this.nodeName = '',
    this.nodeAddress = '',
    this.label = '',
    this.autoName = true,
  });

  final TailscaleIdentity identity;

  /// The last netmap fetched from Tailscale control, if any.
  final TailnetSnapshot? snapshot;

  /// Stable ID of the chosen exit node; empty until the user confirms one.
  final String nodeId;
  final String nodeName;

  /// Tailscale IPv4 of the chosen node, passed to sing-box's `exit_node`.
  final String nodeAddress;

  /// `<exit-device-name>@<network name>` unless the user named it.
  final String label;
  final bool autoName;

  bool get hasNode => nodeId.isNotEmpty;

  /// The chosen node as last reported, or null when it is missing.
  TailnetPeer? get node {
    if (!hasNode) return null;
    for (final peer in snapshot?.exitNodes ?? const <TailnetPeer>[]) {
      if (peer.stableId == nodeId) return peer;
    }
    return null;
  }

  String get networkName => snapshot?.networkName ?? '';

  String displayName(String fallback) {
    if (label.isNotEmpty) return label;
    if (networkName.isNotEmpty) return networkName;
    return fallback;
  }

  static String automaticLabel(String nodeName, String network) =>
      network.isEmpty ? nodeName : '$nodeName@$network';

  ExitProfile copyWith({
    TailscaleIdentity? identity,
    TailnetSnapshot? snapshot,
    String? nodeId,
    String? nodeName,
    String? nodeAddress,
    String? label,
    bool? autoName,
  }) =>
      ExitProfile(
        identity: identity ?? this.identity,
        snapshot: snapshot ?? this.snapshot,
        nodeId: nodeId ?? this.nodeId,
        nodeName: nodeName ?? this.nodeName,
        nodeAddress: nodeAddress ?? this.nodeAddress,
        label: label ?? this.label,
        autoName: autoName ?? this.autoName,
      );

  Map<String, Object?> toJson() => {
        'identity': identity.toJson(),
        if (snapshot != null) 'snapshot': snapshot!.toJson(),
        'node_id': nodeId,
        'node_name': nodeName,
        'node_address': nodeAddress,
        'label': label,
        'auto_name': autoName,
      };

  factory ExitProfile.fromJson(Map<String, Object?> json) => ExitProfile(
        identity: TailscaleIdentity.fromJson(
          (json['identity'] as Map).cast<String, Object?>(),
        ),
        snapshot: json['snapshot'] is Map
            ? TailnetSnapshot.fromJson(
                (json['snapshot'] as Map).cast<String, Object?>(),
              )
            : null,
        nodeId: json['node_id'] as String? ?? '',
        nodeName: json['node_name'] as String? ?? '',
        nodeAddress: json['node_address'] as String? ?? '',
        label: json['label'] as String? ?? '',
        autoName: json['auto_name'] as bool? ?? true,
      );
}
