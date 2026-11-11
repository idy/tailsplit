import 'dart:convert';
import 'dart:io' show InternetAddress;

import '../model/settings.dart';
import 'rules.dart';

/// Inputs that come from the signed-in device rather than from settings.
class ExitRuntime {
  const ExitRuntime({
    required this.hostname,
    required this.nodeAddress,
    required this.stateDirectory,
    required this.cacheFile,
    this.magicDnsSuffix = '',
  });

  final String hostname;

  /// Tailscale address of the chosen exit node (sing-box `exit_node`).
  final String nodeAddress;
  final String stateDirectory;
  final String cacheFile;
  final String magicDnsSuffix;
}

/// Builds the sing-box JSON for one exit, following the desktop compiler's
/// Split/Global semantics (compile.go) with a single `tailnet` endpoint.
abstract final class ConfigBuilder {
  static const exitTag = 'tailnet';
  static const tunIpv4 = '172.31.255.1/30';
  static const tunIpv6 = 'fd74:6169:6c73::1/126';
  static const tailnetRanges = ['100.64.0.0/10', 'fd7a:115c:a1e0::/48'];

  static String build({
    required MobileSettings settings,
    required ExitRuntime exit,
    RuleProjection? rules,
  }) =>
      const JsonEncoder.withIndent('  ').convert(
        buildObject(settings: settings, exit: exit, rules: rules),
      );

  static Map<String, Object?> buildObject({
    required MobileSettings settings,
    required ExitRuntime exit,
    RuleProjection? rules,
  }) {
    final global = settings.mode == RoutingMode.global;
    final noDirectIpv6 = !settings.directIpv6;
    final throughExit = global || settings.unmatched == UnmatchedRoute.exit;
    final suffix = exit.magicDnsSuffix.toLowerCase();

    final servers = <Map<String, Object?>>[
      _resolver('dns-direct', settings.directDns),
      {..._resolver('dns-proxy', settings.proxyDns, proxy: true), 'detour': exitTag},
      if (suffix.isNotEmpty)
        {
          'type': 'tailscale',
          'tag': 'dns-tailnet',
          'endpoint': exitTag,
          'accept_default_resolvers': false,
          'accept_search_domain': false,
        },
      if (settings.fakeIp)
        {
          'type': 'fakeip',
          'tag': 'dns-fake',
          'inet4_range': '198.18.0.0/15',
          'inet6_range': '2001:2::/48',
        },
    ];

    final directMatchers = <Map<String, Object?>>[];
    final proxyMatchers = <Map<String, Object?>>[];
    final ruleSets = <Map<String, Object?>>[];
    if (!global) {
      if (settings.directDomains.isNotEmpty) {
        directMatchers.add({'domain_suffix': settings.directDomains});
      }
      if (settings.proxyDomains.isNotEmpty) {
        proxyMatchers.add({'domain_suffix': settings.proxyDomains});
      }
      if (rules != null) {
        if (!rules.direct.isEmpty) {
          ruleSets.add({'type': 'inline', 'tag': 'gfw-direct', 'rules': rules.direct.toRules()});
          directMatchers.add({'rule_set': ['gfw-direct']});
        }
        ruleSets.add({'type': 'inline', 'tag': 'gfw-proxy', 'rules': rules.proxy.toRules()});
        proxyMatchers.add({'rule_set': ['gfw-proxy']});
      }
    }

    final dnsRules = <Map<String, Object?>>[];
    if (suffix.isNotEmpty) {
      dnsRules.add({'domain_suffix': [suffix], 'action': 'route', 'server': 'dns-tailnet'});
    }
    for (final match in directMatchers) {
      if (noDirectIpv6) {
        dnsRules.add({...match, 'query_type': ['AAAA'], 'action': 'predefined', 'rcode': 'NOERROR'});
      }
      dnsRules.add({...match, 'action': 'route', 'server': 'dns-direct'});
    }
    for (final match in proxyMatchers) {
      if (settings.fakeIp) {
        dnsRules.add({...match, 'query_type': ['A', 'AAAA'], 'action': 'route', 'server': 'dns-fake'});
      }
      dnsRules.add({...match, 'action': 'route', 'server': 'dns-proxy'});
    }
    var dnsFinal = 'dns-direct';
    if (throughExit) {
      dnsFinal = 'dns-proxy';
    } else if (noDirectIpv6) {
      dnsRules.add({'query_type': ['AAAA'], 'action': 'predefined', 'rcode': 'NOERROR'});
    }
    if (global && settings.fakeIp) {
      // Only client DNS gets synthetic answers; the endpoint's own lookups
      // still resolve for real.
      dnsRules.add({
        'inbound': ['tun-in'],
        'query_type': ['A', 'AAAA'],
        'action': 'route',
        'server': 'dns-fake',
      });
    }

    // DNS policy cannot catch IPv6 literals or cached answers. Guard every
    // direct-routing branch before it accepts a destination, while keeping
    // Tailnet/proxy IPv6 routes available.
    List<Map<String, Object?>> directRoutes(Map<String, Object?> match) => [
          if (noDirectIpv6) {...match, 'ip_version': 6, 'action': 'reject'},
          {...match, 'action': 'route', 'outbound': 'direct'},
        ];

    final routes = <Map<String, Object?>>[
      {'inbound': ['tun-in'], 'port': 53, 'action': 'hijack-dns'},
      {
        'inbound': ['tun-in'],
        'action': 'sniff',
        'sniffer': ['http', 'tls', 'quic'],
        'timeout': '300ms',
      },
      if (suffix.isNotEmpty) ...[
        {'domain_suffix': [suffix], 'action': 'resolve', 'server': 'dns-tailnet'},
        {'domain_suffix': [suffix], 'action': 'route', 'outbound': exitTag},
      ],
      for (final match in directMatchers) ...directRoutes(match),
      if (!global && settings.directIps.isNotEmpty)
        ...directRoutes({'ip_cidr': settings.directIps}),
      {'ip_cidr': tailnetRanges, 'action': 'route', 'outbound': exitTag},
      if (!global && settings.proxyIps.isNotEmpty)
        {'ip_cidr': settings.proxyIps, 'action': 'route', 'outbound': exitTag},
      for (final match in proxyMatchers) {...match, 'action': 'route', 'outbound': exitTag},
      if (!global) ...directRoutes({'ip_is_private': true}),
      if (!global && !throughExit && noDirectIpv6)
        {'ip_version': 6, 'action': 'reject'},
    ];

    final direct = <String, Object?>{
      'type': 'direct',
      'tag': 'direct',
      'domain_resolver': noDirectIpv6
          ? {'server': 'dns-direct', 'strategy': 'ipv4_only'}
          : 'dns-direct',
    };

    return {
      'log': {'level': settings.logLevel.name, 'timestamp': true},
      'dns': {
        'servers': servers,
        'rules': dnsRules,
        'final': dnsFinal,
        'reverse_mapping': true,
      },
      'inbounds': [
        {
          'type': 'tun',
          'tag': 'tun-in',
          'address': [tunIpv4, tunIpv6],
          'mtu': settings.mtu,
          'auto_route': true,
          'strict_route': true,
          'stack': 'gvisor',
        },
      ],
      // Global has no client-selectable direct route; dns-direct exists only
      // for the endpoint's own bootstrap.
      'outbounds': global ? <Object?>[] : [direct],
      'endpoints': [
        {
          'type': 'tailscale',
          'tag': exitTag,
          'hostname': exit.hostname,
          'state_directory': exit.stateDirectory,
          'exit_node': exit.nodeAddress,
          'system_interface': false,
          'accept_routes': false,
          'advertise_exit_node': false,
          'domain_resolver': 'dns-direct',
        },
      ],
      'route': {
        'rules': routes,
        if (ruleSets.isNotEmpty) 'rule_set': ruleSets,
        'final': throughExit ? exitTag : 'direct',
        'auto_detect_interface': true,
        'default_domain_resolver': global ? 'dns-proxy' : 'dns-direct',
      },
      'experimental': {
        'cache_file': {
          'enabled': true,
          'path': exit.cacheFile,
          'store_fakeip': true,
        },
      },
    };
  }

  /// The proxy resolver runs through the exit, so it must be a real server;
  /// an unusable saved value falls back to the desktop default.
  static Map<String, Object?> _resolver(String tag, String value, {bool proxy = false}) {
    var address = ResolverAddress.parse(value);
    if (proxy && (address == null || address.isSystem)) address = ResolverAddress.proxyDefault;
    if (address == null || address.isSystem) return {'type': 'local', 'tag': tag};
    return {'type': 'udp', 'tag': tag, 'server': address.host, 'server_port': address.port};
  }
}

/// A DNS server: `system`, an IPv4 or IPv6 address, `IPv4:port` or
/// `[IPv6]:port`. Hostnames are rejected because a resolver cannot depend on
/// another lookup.
class ResolverAddress {
  const ResolverAddress(this.host, [this.port = 53]);
  const ResolverAddress._system()
      : host = null,
        port = 53;

  static const system = ResolverAddress._system();
  static const proxyDefault = ResolverAddress('8.8.8.8');

  final String? host;
  final int port;

  bool get isSystem => host == null;

  static ResolverAddress? parse(String input) {
    final value = input.trim();
    if (value.isEmpty || value == 'system' || value == 'local') return system;
    var host = value;
    var port = 53;
    if (value.startsWith('[')) {
      final end = value.indexOf(']');
      if (end < 0) return null;
      host = value.substring(1, end);
      final rest = value.substring(end + 1);
      if (rest.isNotEmpty) {
        if (!rest.startsWith(':')) return null;
        final parsed = int.tryParse(rest.substring(1));
        if (parsed == null) return null;
        port = parsed;
      }
    } else if (':'.allMatches(value).length == 1) {
      final colon = value.indexOf(':');
      host = value.substring(0, colon);
      final parsed = int.tryParse(value.substring(colon + 1));
      if (parsed == null) return null;
      port = parsed;
    }
    if (port < 1 || port > 65535) return null;
    if (!host.contains(':') && !RegExp(r'^\d{1,3}(\.\d{1,3}){3}$').hasMatch(host)) return null;
    final address = InternetAddress.tryParse(host);
    if (address == null) return null;
    return ResolverAddress(address.address, port);
  }

  @override
  String toString() {
    final host = this.host;
    if (host == null) return 'system';
    if (port == 53) return host;
    return host.contains(':') ? '[$host]:$port' : '$host:$port';
  }
}
