// The subset of tailscale.com/tailcfg this client speaks, JSON field names as
// Go marshals them.

import 'models.dart';

/// `tailcfg.CurrentCapabilityVersion` of the tailscale fork pinned by
/// sing-box 1.14.4. Also the Noise protocol version.
const currentCapabilityVersion = 142;

/// Base version of that fork, sent as `Hostinfo.IPNVersion`.
const pinnedTailscaleVersion = '1.102.1';

/// `tailcfg.LBHeader`.
const lbHeader = 'ts-lb';

/// `time.Time{}` as Go marshals it.
const goZeroTime = '0001-01-01T00:00:00Z';

/// The far-past expiry (`time.Unix(123, 0)`) that `TryLogout` sends.
const goLogoutExpiry = '1970-01-01T00:02:03Z';

class HostinfoFields {
  const HostinfoFields({
    required this.hostname,
    required this.os,
    required this.backendLogId,
    this.osVersion,
    this.deviceModel,
  });

  final String hostname;
  final String os;
  final String backendLogId;
  final String? osVersion;
  final String? deviceModel;

  Map<String, Object?> toJson() => {
        'IPNVersion': pinnedTailscaleVersion,
        'BackendLogID': backendLogId,
        'OS': os,
        if (osVersion != null && osVersion!.isNotEmpty) 'OSVersion': osVersion,
        if (deviceModel != null && deviceModel!.isNotEmpty) 'DeviceModel': deviceModel,
        'Hostname': hostname,
      };
}

/// `tailcfg.RegisterRequest`, mirroring what `Direct.doLogin` sends (no
/// auth key, no request signature, no tailnet-lock re-signing).
Map<String, Object?> registerRequestJson({
  required String nodeKey,
  String? oldNodeKey,
  String? nlKey,
  required HostinfoFields hostinfo,
  String followup = '',
  String expiry = goZeroTime,
}) =>
    {
      'Version': currentCapabilityVersion,
      'NodeKey': nodeKey,
      'OldNodeKey': oldNodeKey ?? _zeroNodeKey,
      'NLKey': ?nlKey,
      'Expiry': expiry,
      'Followup': followup,
      'Hostinfo': hostinfo.toJson(),
      'NodeKeySignature': null,
    };

const _zeroNodeKey = 'nodekey:0000000000000000000000000000000000000000000000000000000000000000';
const _zeroDiscoKey = 'discokey:0000000000000000000000000000000000000000000000000000000000000000';

/// A read-only streaming `tailcfg.MapRequest`.
///
/// `Stream: true` with `Version >= 68` is the request the server treats as
/// read-only (tailcfg: "the server should treat this as a read-only request
/// and ignore any Hostinfo or other fields that might be set"), so it does
/// not overwrite the endpoints, DERP home or disco key a running sing-box
/// session reported. A non-streaming request would be an update and would.
/// `Compress` is omitted, so the server sends plain JSON frames.
Map<String, Object?> mapRequestJson({
  required String nodeKey,
  required HostinfoFields hostinfo,
}) =>
    {
      'Version': currentCapabilityVersion,
      'KeepAlive': true,
      'NodeKey': nodeKey,
      'DiscoKey': _zeroDiscoKey,
      'Stream': true,
      'Hostinfo': hostinfo.toJson(),
    };

class RegisterResponse {
  RegisterResponse({
    required this.userId,
    required this.userDisplayName,
    required this.loginName,
    required this.loginDisplayName,
    required this.profilePicUrl,
    required this.nodeKeyExpired,
    required this.machineAuthorized,
    required this.authUrl,
    required this.hasNodeKeySignature,
    required this.error,
  });

  factory RegisterResponse.fromJson(Map<String, Object?> json) {
    final user = _map(json['User']);
    final login = _map(json['Login']);
    final signature = json['NodeKeySignature'];
    return RegisterResponse(
      userId: (user['ID'] as num?)?.toInt() ?? 0,
      userDisplayName: user['DisplayName'] as String? ?? '',
      loginName: login['LoginName'] as String? ?? '',
      loginDisplayName: login['DisplayName'] as String? ?? '',
      profilePicUrl: (user['ProfilePicURL'] as String?) ?? (login['ProfilePicURL'] as String?) ?? '',
      nodeKeyExpired: json['NodeKeyExpired'] as bool? ?? false,
      machineAuthorized: json['MachineAuthorized'] as bool? ?? false,
      authUrl: json['AuthURL'] as String? ?? '',
      hasNodeKeySignature: signature is String && signature.isNotEmpty,
      error: json['Error'] as String? ?? '',
    );
  }

  final int userId;
  final String userDisplayName;
  final String loginName;
  final String loginDisplayName;
  final String profilePicUrl;
  final bool nodeKeyExpired;
  final bool machineAuthorized;
  final String authUrl;
  final bool hasNodeKeySignature;
  final String error;
}

/// Thrown when a MapResponse cannot be turned into a snapshot.
class MapResponseFormatException implements Exception {
  const MapResponseFormatException(this.message);
  final String message;

  @override
  String toString() => 'MapResponseFormatException: $message';
}

/// Self node facts needed to decide whether the device is still signed in.
class SelfNodeStatus {
  const SelfNodeStatus({required this.expired, required this.keyExpiry, required this.controlTime});
  final bool expired;
  final DateTime? keyExpiry;
  final DateTime? controlTime;

  bool get keyExpired {
    if (expired) return true;
    final expiry = keyExpiry;
    if (expiry == null) return false;
    return expiry.isBefore(controlTime ?? DateTime.now().toUtc());
  }
}

/// The parts of a first (full) `tailcfg.MapResponse` the app uses.
class ParsedMapResponse {
  ParsedMapResponse({required this.snapshot, required this.selfStatus});
  final TailnetSnapshot snapshot;
  final SelfNodeStatus selfStatus;
}

/// Whether [json] is a keep-alive message (no state besides KeepAlive).
bool isKeepAlive(Map<String, Object?> json) => json['KeepAlive'] == true;

/// Builds a snapshot from the first non-keep-alive MapResponse of a session.
ParsedMapResponse parseMapResponse(
  Map<String, Object?> json, {
  DateTime? now,
  String fallbackLoginName = '',
  String fallbackDisplayName = '',
  int fallbackUserId = 0,
}) {
  final node = json['Node'];
  if (node is! Map) throw const MapResponseFormatException('initial MapResponse lacked node');
  final self = node.cast<String, Object?>();

  final selfName = (self['Name'] as String? ?? '');
  final selfFqdn = _trimDot(selfName);
  final selfHostinfo = _map(self['Hostinfo']);
  final userId = (self['User'] as num?)?.toInt() ?? fallbackUserId;

  var loginName = fallbackLoginName;
  var displayName = fallbackDisplayName;
  var picUrl = '';
  for (final profile in (json['UserProfiles'] as List?) ?? const []) {
    if (profile is! Map) continue;
    if ((profile['ID'] as num?)?.toInt() != userId) continue;
    loginName = profile['LoginName'] as String? ?? loginName;
    displayName = profile['DisplayName'] as String? ?? displayName;
    picUrl = profile['ProfilePicURL'] as String? ?? '';
  }
  if (displayName.isEmpty) displayName = loginName;

  final domain = json['Domain'] as String? ?? '';
  final displayNameAttr = tailnetDisplayNameOf(self);

  // Go's mapSession uses a non-empty Peers as the complete list; otherwise
  // PeersChanged adds peers to the initially empty session. Do not silently
  // discard those peers when a server uses that form for the first map.
  final fullPeers = (json['Peers'] as List?) ?? const [];
  final changedPeers = (json['PeersChanged'] as List?) ?? const [];
  final peers = <Map<String, Object?>>[
    for (final peer in fullPeers.isNotEmpty ? fullPeers : changedPeers)
      if (peer is Map) peer.cast<String, Object?>(),
  ];
  final exitNodes = peers.map(_peer).where((p) => p.isExitNode).toList()
    ..sort(comparePeers);
  final diagnostics = TailnetDiscoveryDiagnostics(
    peerCount: peers.length,
    exitRoutePeerCount: exitNodes.length,
    advertisedExitRoutePeerCount: peers.where((p) => offersExitRoute(_map(p['Hostinfo'])['RoutableIPs'])).length,
    primaryExitRoutePeerCount: peers.where((p) => offersExitRoute(p['PrimaryRoutes'])).length,
    missingAllowedIpsPeerCount: peers.where((p) => p['AllowedIPs'] == null).length,
    changedPeerCount: changedPeers.length,
    peerListPresent: json['Peers'] is List,
  );

  final keyExpiry = _goTime(self['KeyExpiry']);
  return ParsedMapResponse(
    snapshot: TailnetSnapshot(
      selfName: _firstLabel(selfFqdn).isNotEmpty
          ? _firstLabel(selfFqdn)
          : (selfHostinfo['Hostname'] as String? ?? ''),
      selfFqdn: selfFqdn,
      selfAddresses: _bareAddresses(self['Addresses']),
      networkName: displayNameAttr.isNotEmpty ? displayNameAttr : domain,
      magicDnsSuffix: magicDnsSuffixOf(selfName),
      userLoginName: loginName,
      userDisplayName: displayName,
      stableNodeId: self['StableID'] as String? ?? '',
      keyExpiry: keyExpiry,
      exitNodes: exitNodes,
      fetchedAt: (now ?? DateTime.now()).toUtc(),
      userId: userId,
      userProfilePicUrl: picUrl,
      domainName: domain,
      tailnetDisplayName: displayNameAttr,
      machineAuthorized: self['MachineAuthorized'] as bool? ?? false,
      discoveryDiagnostics: diagnostics,
    ),
    selfStatus: SelfNodeStatus(
      expired: self['Expired'] as bool? ?? false,
      keyExpiry: keyExpiry,
      controlTime: _goTime(json['ControlTime']),
    ),
  );
}

/// `netmap.MagicDNSSuffixOfNodeName`, but empty when the name has no dot.
String magicDnsSuffixOf(String nodeName) {
  final name = nodeName.replaceAll(RegExp(r'^\.+|\.+$'), '');
  final dot = name.indexOf('.');
  return dot < 0 ? '' : name.substring(dot + 1);
}

/// First value of the `tailnet-display-name` node attribute.
String tailnetDisplayNameOf(Map<String, Object?> node) {
  final capMap = node['CapMap'];
  if (capMap is! Map) return '';
  final values = capMap['tailnet-display-name'];
  if (values is! List || values.isEmpty) return '';
  final first = values.first;
  return first is String ? first : '';
}

/// Whether AllowedIPs offers an IPv4 and/or IPv6 default route. A missing
/// AllowedIPs (capver >= 112) means "same as Addresses", so not an exit node.
bool offersExitRoute(Object? allowedIps) {
  if (allowedIps is! List) return false;
  return allowedIps.any((p) => p == '0.0.0.0/0' || p == '::/0');
}

int comparePeers(TailnetPeer a, TailnetPeer b) {
  if (a.online != b.online) return a.online ? -1 : 1;
  final byName = a.name.toLowerCase().compareTo(b.name.toLowerCase());
  if (byName != 0) return byName;
  return a.stableId.compareTo(b.stableId);
}

TailnetPeer _peer(Map<String, Object?> node) {
  final fqdn = _trimDot(node['Name'] as String? ?? '');
  final hostinfo = _map(node['Hostinfo']);
  final label = _firstLabel(fqdn);
  final os = hostinfo['OS'] as String?;
  return TailnetPeer(
    stableId: node['StableID'] as String? ?? '',
    name: label.isNotEmpty ? label : (hostinfo['Hostname'] as String? ?? ''),
    fqdn: fqdn,
    addresses: _bareAddresses(node['Addresses']),
    online: node['Online'] == true,
    lastSeen: _goTime(node['LastSeen']),
    os: os == null || os.isEmpty ? null : os,
    isExitNode: offersExitRoute(node['AllowedIPs']),
  );
}

List<String> _bareAddresses(Object? prefixes) => [
      for (final p in (prefixes as List?) ?? const [])
        if (p is String && p.isNotEmpty) p.contains('/') ? p.substring(0, p.indexOf('/')) : p,
    ];

String _trimDot(String name) => name.endsWith('.') ? name.substring(0, name.length - 1) : name;

String _firstLabel(String fqdn) {
  final dot = fqdn.indexOf('.');
  return dot < 0 ? fqdn : fqdn.substring(0, dot);
}

DateTime? _goTime(Object? value) {
  if (value is! String || value.isEmpty || value.startsWith('0001-01-01')) return null;
  return DateTime.tryParse(value)?.toUtc();
}

Map<String, Object?> _map(Object? value) =>
    value is Map ? value.cast<String, Object?>() : const <String, Object?>{};
