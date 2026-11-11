import 'identity.dart';

/// One device of the tailnet, as reported by Tailscale control.
class TailnetPeer {
  const TailnetPeer({
    required this.stableId,
    required this.name,
    required this.fqdn,
    required this.addresses,
    required this.online,
    this.lastSeen,
    this.os,
    required this.isExitNode,
  });

  /// Stable node ID (`tailcfg.StableNodeID`), usable as sing-box's exit node.
  final String stableId;

  /// First label of [fqdn], e.g. `tokyo-vps`.
  final String name;

  /// MagicDNS name without the trailing dot, e.g. `tokyo-vps.tail1234.ts.net`.
  final String fqdn;

  /// Tailscale addresses as bare IPs (no prefix length).
  final List<String> addresses;
  final bool online;
  final DateTime? lastSeen;
  final String? os;

  /// Whether the peer offers `0.0.0.0/0` and/or `::/0` in its AllowedIPs.
  final bool isExitNode;

  Map<String, Object?> toJson() => {
        'stable_id': stableId,
        'name': name,
        'fqdn': fqdn,
        'addresses': addresses,
        'online': online,
        if (lastSeen != null) 'last_seen': lastSeen!.toUtc().toIso8601String(),
        if (os != null) 'os': os,
        'is_exit_node': isExitNode,
      };

  factory TailnetPeer.fromJson(Map<String, Object?> json) => TailnetPeer(
        stableId: json['stable_id'] as String? ?? '',
        name: json['name'] as String? ?? '',
        fqdn: json['fqdn'] as String? ?? '',
        addresses: _stringList(json['addresses']),
        online: json['online'] as bool? ?? false,
        lastSeen: _parseTime(json['last_seen']),
        os: json['os'] as String?,
        isExitNode: json['is_exit_node'] as bool? ?? false,
      );

  @override
  String toString() => 'TailnetPeer($name, online: $online, exit: $isExitNode)';
}

/// Counts from a received netmap, safe to include in diagnostic logs.
/// No names, addresses, identifiers, keys or URLs are retained here.
class TailnetDiscoveryDiagnostics {
  const TailnetDiscoveryDiagnostics({
    required this.peerCount,
    required this.exitRoutePeerCount,
    required this.advertisedExitRoutePeerCount,
    required this.primaryExitRoutePeerCount,
    required this.missingAllowedIpsPeerCount,
    required this.changedPeerCount,
    required this.peerListPresent,
  });

  final int peerCount;
  final int exitRoutePeerCount;
  final int advertisedExitRoutePeerCount;
  final int primaryExitRoutePeerCount;
  final int missingAllowedIpsPeerCount;
  final int changedPeerCount;
  final bool peerListPresent;

  String get summary => 'Netmap discovery: peers=$peerCount, exit-routes=$exitRoutePeerCount, '
      'advertised-exits=$advertisedExitRoutePeerCount, primary-exits=$primaryExitRoutePeerCount, '
      'allowed-ips-missing=$missingAllowedIpsPeerCount, peer-changes=$changedPeerCount, '
      'peer-list=$peerListPresent';

  Map<String, Object?> toJson() => {
        'peer_count': peerCount,
        'exit_route_peer_count': exitRoutePeerCount,
        'advertised_exit_route_peer_count': advertisedExitRoutePeerCount,
        'primary_exit_route_peer_count': primaryExitRoutePeerCount,
        'missing_allowed_ips_peer_count': missingAllowedIpsPeerCount,
        'changed_peer_count': changedPeerCount,
        'peer_list_present': peerListPresent,
      };

  factory TailnetDiscoveryDiagnostics.fromJson(Map<String, Object?> json) => TailnetDiscoveryDiagnostics(
        peerCount: (json['peer_count'] as num?)?.toInt() ?? 0,
        exitRoutePeerCount: (json['exit_route_peer_count'] as num?)?.toInt() ?? 0,
        advertisedExitRoutePeerCount: (json['advertised_exit_route_peer_count'] as num?)?.toInt() ?? 0,
        primaryExitRoutePeerCount: (json['primary_exit_route_peer_count'] as num?)?.toInt() ?? 0,
        missingAllowedIpsPeerCount: (json['missing_allowed_ips_peer_count'] as num?)?.toInt() ?? 0,
        changedPeerCount: (json['changed_peer_count'] as num?)?.toInt() ?? 0,
        peerListPresent: json['peer_list_present'] as bool? ?? false,
      );

  @override
  String toString() => summary;
}

/// What the app needs from one netmap: the device itself, the tailnet and
/// the peers that can act as exit nodes.
class TailnetSnapshot {
  const TailnetSnapshot({
    required this.selfName,
    required this.selfFqdn,
    required this.selfAddresses,
    required this.networkName,
    required this.magicDnsSuffix,
    required this.userLoginName,
    required this.userDisplayName,
    required this.stableNodeId,
    this.keyExpiry,
    required this.exitNodes,
    required this.fetchedAt,
    this.userId = 0,
    this.userProfilePicUrl = '',
    this.domainName = '',
    this.tailnetDisplayName = '',
    this.machineAuthorized = true,
    this.discoveryDiagnostics,
  });

  final String selfName;
  final String selfFqdn;
  final List<String> selfAddresses;

  /// The tailnet name as Tailscale shows it: the admin-set tailnet display
  /// name when present, otherwise `MapResponse.Domain`.
  final String networkName;

  /// For example `tail1234.ts.net`; empty when the node has no MagicDNS name.
  final String magicDnsSuffix;
  final String userLoginName;
  final String userDisplayName;
  final String stableNodeId;

  /// When this device's node key expires; null if it never expires.
  final DateTime? keyExpiry;

  /// Peers that offer exit routes, online first, then by name.
  final List<TailnetPeer> exitNodes;
  final DateTime fetchedAt;

  /// `tailcfg.UserID` of the owning user (written to tsnet's profile).
  final int userId;
  final String userProfilePicUrl;

  /// Raw `MapResponse.Domain` (tsnet's `NetworkProfile.DomainName`).
  final String domainName;

  /// The `tailnet-display-name` node attribute, if the admin set one.
  final String tailnetDisplayName;

  /// False while the tailnet requires admin approval of this device.
  final bool machineAuthorized;

  /// Counts from the original map response; absent in older saved snapshots.
  final TailnetDiscoveryDiagnostics? discoveryDiagnostics;

  TailnetSnapshot copyWith({List<TailnetPeer>? exitNodes, DateTime? fetchedAt}) =>
      TailnetSnapshot(
        selfName: selfName,
        selfFqdn: selfFqdn,
        selfAddresses: selfAddresses,
        networkName: networkName,
        magicDnsSuffix: magicDnsSuffix,
        userLoginName: userLoginName,
        userDisplayName: userDisplayName,
        stableNodeId: stableNodeId,
        keyExpiry: keyExpiry,
        exitNodes: exitNodes ?? this.exitNodes,
        fetchedAt: fetchedAt ?? this.fetchedAt,
        userId: userId,
        userProfilePicUrl: userProfilePicUrl,
        domainName: domainName,
        tailnetDisplayName: tailnetDisplayName,
        machineAuthorized: machineAuthorized,
        discoveryDiagnostics: discoveryDiagnostics,
      );

  Map<String, Object?> toJson() => {
        'self_name': selfName,
        'self_fqdn': selfFqdn,
        'self_addresses': selfAddresses,
        'network_name': networkName,
        'magic_dns_suffix': magicDnsSuffix,
        'user_login_name': userLoginName,
        'user_display_name': userDisplayName,
        'stable_node_id': stableNodeId,
        if (keyExpiry != null) 'key_expiry': keyExpiry!.toUtc().toIso8601String(),
        'exit_nodes': [for (final peer in exitNodes) peer.toJson()],
        'fetched_at': fetchedAt.toUtc().toIso8601String(),
        'user_id': userId,
        if (userProfilePicUrl.isNotEmpty) 'user_profile_pic_url': userProfilePicUrl,
        'domain_name': domainName,
        if (tailnetDisplayName.isNotEmpty) 'tailnet_display_name': tailnetDisplayName,
        'machine_authorized': machineAuthorized,
        if (discoveryDiagnostics != null) 'discovery_diagnostics': discoveryDiagnostics!.toJson(),
      };

  factory TailnetSnapshot.fromJson(Map<String, Object?> json) {
    final networkName = json['network_name'] as String? ?? '';
    return TailnetSnapshot(
      selfName: json['self_name'] as String? ?? '',
      selfFqdn: json['self_fqdn'] as String? ?? '',
      selfAddresses: _stringList(json['self_addresses']),
      networkName: networkName,
      magicDnsSuffix: json['magic_dns_suffix'] as String? ?? '',
      userLoginName: json['user_login_name'] as String? ?? '',
      userDisplayName: json['user_display_name'] as String? ?? '',
      stableNodeId: json['stable_node_id'] as String? ?? '',
      keyExpiry: _parseTime(json['key_expiry']),
      exitNodes: [
        for (final peer in (json['exit_nodes'] as List?) ?? const [])
          if (peer is Map) TailnetPeer.fromJson(peer.cast<String, Object?>()),
      ],
      fetchedAt: _parseTime(json['fetched_at']) ?? DateTime.fromMillisecondsSinceEpoch(0, isUtc: true),
      userId: (json['user_id'] as num?)?.toInt() ?? 0,
      userProfilePicUrl: json['user_profile_pic_url'] as String? ?? '',
      domainName: json['domain_name'] as String? ?? networkName,
      tailnetDisplayName: json['tailnet_display_name'] as String? ?? '',
      machineAuthorized: json['machine_authorized'] as bool? ?? true,
      discoveryDiagnostics: json['discovery_diagnostics'] is Map
          ? TailnetDiscoveryDiagnostics.fromJson((json['discovery_diagnostics'] as Map).cast<String, Object?>())
          : null,
    );
  }

  @override
  String toString() => 'TailnetSnapshot($selfName on $networkName, ${exitNodes.length} exit nodes)';
}

/// Progress of [TailscaleControlClient.login].
sealed class LoginProgress {
  const LoginProgress();
}

/// Control wants the user to sign in at [authUrl]. May be emitted again with
/// a new URL if control replaces it. Treat [authUrl] as a secret: do not log it.
final class LoginNeedsBrowser extends LoginProgress {
  const LoginNeedsBrowser(this.authUrl);
  final Uri authUrl;

  @override
  String toString() => 'LoginNeedsBrowser(${authUrl.host})';
}

/// The device is signed in. [identity] is the identity that control now
/// knows; it differs from the one passed to `login` only when control made
/// the client replace an expired node key, so always persist this one.
final class LoginAuthorized extends LoginProgress {
  const LoginAuthorized(this.snapshot, {required this.identity});
  final TailnetSnapshot snapshot;
  final TailscaleIdentity identity;

  @override
  String toString() => 'LoginAuthorized($snapshot)';
}

/// Stable failure categories for callers; diagnostic wording is independent.
enum ControlFailureKind { offline, timeout, tls, rateLimited, server, rejected, cancelled }

/// A control-plane failure. [message] never contains keys or auth URLs.
class TailscaleControlException implements Exception {
  const TailscaleControlException(this.message, {this.kind = ControlFailureKind.server});
  final String message;
  final ControlFailureKind kind;

  @override
  String toString() => 'TailscaleControlException: $message';
}

/// The node key is unknown to control, expired or no longer authorized; the
/// device has to sign in again.
class TailscaleNeedsLogin extends TailscaleControlException {
  const TailscaleNeedsLogin([super.message = 'This device needs to sign in to Tailscale again.'])
      : super(kind: ControlFailureKind.rejected);

  @override
  String toString() => 'TailscaleNeedsLogin: $message';
}

List<String> _stringList(Object? value) =>
    [for (final item in (value as List?) ?? const []) if (item is String) item];

DateTime? _parseTime(Object? value) {
  if (value is! String || value.isEmpty) return null;
  return DateTime.tryParse(value)?.toUtc();
}
