/// Routing and app preferences, mirroring the desktop's `config.default.yaml`.
library;

enum RoutingMode { split, global }

enum RuleSource { builtin, custom, none }

/// Where unmatched traffic goes in Split mode (`rules.final`).
enum UnmatchedRoute { direct, exit }

enum AppLanguage { system, en, zh }

enum LogLevel { trace, debug, info, warn, error }

class MobileSettings {
  const MobileSettings({
    this.mode = RoutingMode.split,
    this.source = RuleSource.builtin,
    this.customSourceUrl = '',
    this.unmatched = UnmatchedRoute.direct,
    this.proxyDomains = defaultProxyDomains,
    this.directDomains = const [],
    this.proxyIps = defaultProxyIps,
    this.directIps = const [],
    this.proxyDns = '8.8.8.8',
    this.directDns = '',
    this.fakeIp = true,
    this.directIpv6 = false,
    this.mtu = 1400,
    this.logLevel = LogLevel.info,
    this.language = AppLanguage.system,
    this.vpnDisclosureAccepted = false,
  });

  static const defaultProxyDomains = [
    'github.com',
    'youtube.com',
    'myexternalip.com',
    'idy.dev',
  ];
  static const defaultProxyIps = [
    '8.8.8.8/32',
    '8.8.4.4/32',
    '1.1.1.1/32',
    '1.0.0.1/32',
  ];

  final RoutingMode mode;
  final RuleSource source;
  final String customSourceUrl;
  final UnmatchedRoute unmatched;
  final List<String> proxyDomains;
  final List<String> directDomains;
  final List<String> proxyIps;
  final List<String> directIps;

  /// Resolver used through the exit for proxied names.
  final String proxyDns;

  /// Empty means the system resolver.
  final String directDns;
  final bool fakeIp;
  final bool directIpv6;
  final int mtu;
  final LogLevel logLevel;
  final AppLanguage language;

  /// Explicit acceptance of the VPN data disclosure; older installs must opt in.
  final bool vpnDisclosureAccepted;

  MobileSettings copyWith({
    RoutingMode? mode,
    RuleSource? source,
    String? customSourceUrl,
    UnmatchedRoute? unmatched,
    List<String>? proxyDomains,
    List<String>? directDomains,
    List<String>? proxyIps,
    List<String>? directIps,
    String? proxyDns,
    String? directDns,
    bool? fakeIp,
    bool? directIpv6,
    int? mtu,
    LogLevel? logLevel,
    AppLanguage? language,
    bool? vpnDisclosureAccepted,
  }) =>
      MobileSettings(
        mode: mode ?? this.mode,
        source: source ?? this.source,
        customSourceUrl: customSourceUrl ?? this.customSourceUrl,
        unmatched: unmatched ?? this.unmatched,
        proxyDomains: proxyDomains ?? this.proxyDomains,
        directDomains: directDomains ?? this.directDomains,
        proxyIps: proxyIps ?? this.proxyIps,
        directIps: directIps ?? this.directIps,
        proxyDns: proxyDns ?? this.proxyDns,
        directDns: directDns ?? this.directDns,
        fakeIp: fakeIp ?? this.fakeIp,
        directIpv6: directIpv6 ?? this.directIpv6,
        mtu: mtu ?? this.mtu,
        logLevel: logLevel ?? this.logLevel,
        language: language ?? this.language,
        vpnDisclosureAccepted: vpnDisclosureAccepted ?? this.vpnDisclosureAccepted,
      );

  /// True when [other] would produce a different VPN configuration.
  bool affectsConnection(MobileSettings other) =>
      mode != other.mode ||
      source != other.source ||
      customSourceUrl != other.customSourceUrl ||
      unmatched != other.unmatched ||
      !_same(proxyDomains, other.proxyDomains) ||
      !_same(directDomains, other.directDomains) ||
      !_same(proxyIps, other.proxyIps) ||
      !_same(directIps, other.directIps) ||
      proxyDns != other.proxyDns ||
      directDns != other.directDns ||
      fakeIp != other.fakeIp ||
      directIpv6 != other.directIpv6 ||
      mtu != other.mtu ||
      logLevel != other.logLevel;

  static bool _same(List<String> a, List<String> b) {
    if (a.length != b.length) return false;
    for (var i = 0; i < a.length; i++) {
      if (a[i] != b[i]) return false;
    }
    return true;
  }

  Map<String, Object?> toJson() => {
        'mode': mode.name,
        'source': source.name,
        'custom_source_url': customSourceUrl,
        'final': unmatched.name,
        'proxy_domains': proxyDomains,
        'direct_domains': directDomains,
        'proxy_ips': proxyIps,
        'direct_ips': directIps,
        'proxy_dns': proxyDns,
        'direct_dns': directDns,
        'fake_ip': fakeIp,
        'direct_ipv6': directIpv6,
        'mtu': mtu,
        'log_level': logLevel.name,
        'language': language.name,
        'vpn_disclosure_accepted': vpnDisclosureAccepted,
      };

  static const minMtu = 1280;
  static const maxMtu = 9000;

  /// Decodes saved settings field by field: a damaged or older file keeps
  /// every readable value and falls back to defaults for the rest.
  factory MobileSettings.fromJson(Map<String, Object?> json) {
    const d = MobileSettings();
    T pick<T extends Enum>(List<T> values, Object? name, T fallback) =>
        values.where((v) => v.name == name).firstOrNull ?? fallback;
    List<String> list(Object? value, List<String> fallback) => value is List
        ? value.whereType<String>().map((v) => v.trim()).where((v) => v.isNotEmpty).toSet().toList()
        : fallback;
    String text(Object? value, String fallback) => value is String ? value.trim() : fallback;
    bool flag(Object? value, bool fallback) => value is bool ? value : fallback;
    final mtu = json['mtu'];
    final proxyDns = text(json['proxy_dns'], d.proxyDns);
    return MobileSettings(
      mode: pick(RoutingMode.values, json['mode'], d.mode),
      source: pick(RuleSource.values, json['source'], d.source),
      customSourceUrl: text(json['custom_source_url'], ''),
      unmatched: pick(UnmatchedRoute.values, json['final'], d.unmatched),
      proxyDomains: list(json['proxy_domains'], d.proxyDomains),
      directDomains: list(json['direct_domains'], d.directDomains),
      proxyIps: list(json['proxy_ips'], d.proxyIps),
      directIps: list(json['direct_ips'], d.directIps),
      proxyDns: proxyDns.isEmpty ? d.proxyDns : proxyDns,
      directDns: text(json['direct_dns'], d.directDns),
      fakeIp: flag(json['fake_ip'], d.fakeIp),
      directIpv6: flag(json['direct_ipv6'], d.directIpv6),
      mtu: mtu is num ? mtu.toInt().clamp(minMtu, maxMtu) : d.mtu,
      logLevel: pick(LogLevel.values, json['log_level'], d.logLevel),
      language: pick(AppLanguage.values, json['language'], d.language),
      vpnDisclosureAccepted: flag(json['vpn_disclosure_accepted'], false),
    );
  }
}
