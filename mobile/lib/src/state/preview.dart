import '../model/exit_profile.dart';
import '../model/settings.dart';
import '../tailscale/tailscale.dart';
import 'app_controller.dart';

/// Simulated data for design review builds (`TAILSPLIT_PREVIEW=true`).
/// Preview status is documented alongside review captures, outside the app UI.
abstract final class PreviewData {
  static TailnetSnapshot snapshot() {
    final now = DateTime.now();
    TailnetPeer peer(String name, String ip, {bool online = true, Duration? seen, String os = 'linux'}) =>
        TailnetPeer(
          stableId: 'preview-$name',
          name: name,
          fqdn: '$name.tail4f2a1c.ts.net',
          addresses: [ip],
          online: online,
          lastSeen: seen == null ? null : now.subtract(seen),
          os: os,
          isExitNode: true,
        );
    return TailnetSnapshot(
      selfName: 'tailsplit-ios',
      selfFqdn: 'tailsplit-ios.tail4f2a1c.ts.net',
      selfAddresses: const ['100.101.7.42'],
      networkName: 'example.com',
      magicDnsSuffix: 'tail4f2a1c.ts.net',
      userLoginName: 'alice@example.com',
      userDisplayName: 'Alice',
      stableNodeId: 'preview-self',
      exitNodes: [
        peer('tokyo-vps', '100.88.12.4'),
        peer('fra-edge', '100.77.40.18'),
        peer('home-mini', '100.64.3.9', os: 'macOS'),
        peer('sg-relay', '100.91.60.2', online: false, seen: const Duration(hours: 3)),
        peer('nas-office', '100.70.1.30', online: false, seen: const Duration(days: 2)),
      ],
      fetchedAt: now.subtract(const Duration(minutes: 2)),
    );
  }

  static Future<({MobileSettings settings, ExitProfile exit})> seed({bool withNode = true}) async {
    final identity = await TailscaleIdentity.generate(hostname: 'tailsplit-ios');
    final snap = snapshot();
    final node = snap.exitNodes.first;
    return (
      settings: const MobileSettings(),
      exit: ExitProfile(
        identity: identity,
        snapshot: snap,
        nodeId: withNode ? node.stableId : '',
        nodeName: withNode ? node.name : '',
        nodeAddress: withNode ? node.addresses.first : '',
        label: withNode ? ExitProfile.automaticLabel(node.name, snap.networkName) : '',
      ),
    );
  }

  static List<LogLine> logs() => [
        LogLine('info', 'Starting Tailscale sign-in.'),
        LogLine('info', 'Signed in to example.com.'),
        LogLine('info', 'Exit node set to tokyo-vps.'),
        LogLine('info', 'Connecting through tokyo-vps@example.com.'),
        LogLine('info', 'sing-box started (1.14.4)', source: 'core'),
        LogLine('info', 'endpoint/tailscale[tailnet]: exit node 100.88.12.4 selected', source: 'core'),
        LogLine('debug', 'dns: exchanged github.com A via dns-proxy', source: 'core'),
        LogLine('info', 'outbound/direct: connection to 17.253.144.10:443', source: 'core'),
        LogLine('warn', 'dns: lookup failed for telemetry.invalid: NXDOMAIN', source: 'core'),
        LogLine('info', 'endpoint/tailscale[tailnet]: connection to github.com:443', source: 'core'),
        LogLine('error', 'endpoint/tailscale[tailnet]: peer 100.91.60.2 unreachable', source: 'core'),
        LogLine('info', 'Connected (simulated).'),
      ];
}
