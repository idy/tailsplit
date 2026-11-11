import 'dart:io';

import 'package:flutter_test/flutter_test.dart';
import 'package:tailsplit_mobile/src/model/settings.dart';
import 'package:tailsplit_mobile/src/services/config_builder.dart';
import 'package:tailsplit_mobile/src/services/rules.dart';

const _exit = ExitRuntime(
  hostname: 'tailsplit-ios',
  nodeAddress: '100.88.12.4',
  stateDirectory: '/shared/tailscale',
  cacheFile: '/shared/cache.db',
  magicDnsSuffix: 'tail4f2a1c.ts.net',
);

const _list = '''
[AutoProxy 0.2.9]
! comment
||blocked.example
|http://path.example/only/this
@@||allowed.blocked.example
.wild*.example
''';

List<Map<String, Object?>> _routes(Map<String, Object?> config) =>
    ((config['route'] as Map)['rules'] as List).cast<Map<String, Object?>>();

// Evaluate terminal routing decisions for these no-rule-list fixtures using
// sing-box's ordered, conjunctive rule semantics. Native parsing is also checked
// against the pinned core; this helper does not model DNS or sniffing.
String terminalRoute(Map<String, Object?> config, String address, {String domain = '', bool private = false}) {
  final ip = InternetAddress(address);
  final version = ip.type == InternetAddressType.IPv6 ? 6 : 4;
  bool inCidr(String cidr) {
    final parts = cidr.split('/');
    final network = InternetAddress(parts.first).rawAddress;
    final bytes = ip.rawAddress;
    if (network.length != bytes.length) return false;
    for (var bit = 0; bit < int.parse(parts.last); bit++) {
      final mask = 0x80 >> (bit % 8);
      if ((network[bit ~/ 8] & mask) != (bytes[bit ~/ 8] & mask)) return false;
    }
    return true;
  }
  for (final rule in _routes(config)) {
    if (rule['action'] != 'route' && rule['action'] != 'reject') continue;
    if (rule['ip_version'] != null && rule['ip_version'] != version) continue;
    if (rule['ip_is_private'] == true && !private) continue;
    final cidrs = rule['ip_cidr'] as List<String>?;
    if (cidrs != null && !cidrs.any(inCidr)) continue;
    final domains = rule['domain_suffix'] as List<String>?;
    if (domains != null && !domains.any((suffix) => domain == suffix || domain.endsWith('.$suffix'))) continue;
    expect(rule.containsKey('rule_set'), isFalse, reason: 'Only source:none fixtures are supported.');
    return rule['action'] == 'reject' ? 'reject' : rule['outbound'] as String;
  }
  return (config['route'] as Map)['final'] as String;
}

void main() {
  test('disabled Direct IPv6 rejects literal/cached direct targets but preserves proxy IPv6', () {
    final config = ConfigBuilder.buildObject(
      settings: const MobileSettings(source: RuleSource.none,
        directDomains: ['direct.example'], proxyDomains: ['proxy.example'],
        directIps: ['2001:db8:2::/48'], proxyIps: ['2001:db8:1::/48']),
      exit: _exit,
    );
    expect(terminalRoute(config, '2001:db8:9::1'), 'reject');
    expect(terminalRoute(config, '2001:db8:2::1'), 'reject');
    expect(terminalRoute(config, 'fd00::1', private: true), 'reject');
    expect(terminalRoute(config, '2001:db8:9::1', domain: 'direct.example'), 'reject');
    expect(terminalRoute(config, '2001:db8:9::1', domain: 'proxy.example'), 'tailnet');
    expect(terminalRoute(config, '2001:db8:1::1'), 'tailnet');
    expect(terminalRoute(config, 'fd7a:115c:a1e0::1', private: true), 'tailnet');
    expect(terminalRoute(config, '198.51.100.7'), 'direct');
    expect(terminalRoute(config, '192.168.1.7', private: true), 'direct');
  });

  test('Direct IPv6 enabled permits direct IPv6 targets', () {
    final config = ConfigBuilder.buildObject(
      settings: const MobileSettings(source: RuleSource.none, directIpv6: true), exit: _exit);
    expect(terminalRoute(config, '2001:db8:9::1'), 'direct');
    expect(terminalRoute(config, 'fd00::1', private: true), 'direct');
    expect(terminalRoute(config, 'fd7a:115c:a1e0::1', private: true), 'tailnet');
  });

  test('IPv6 final-exit routing does not bypass explicit direct restrictions', () {
    final config = ConfigBuilder.buildObject(
      settings: const MobileSettings(source: RuleSource.none, unmatched: UnmatchedRoute.exit,
          directDomains: ['direct.example']), exit: _exit);
    expect(terminalRoute(config, '2001:db8:9::1'), 'tailnet');
    expect(terminalRoute(config, '2001:db8:9::1', domain: 'direct.example'), 'reject');
    expect(terminalRoute(config, 'fd00::1', private: true), 'reject');
  });

  test('Global keeps IPv6 on the exit regardless of the Split direct preference', () {
    final config = ConfigBuilder.buildObject(
      settings: const MobileSettings(mode: RoutingMode.global, source: RuleSource.none,
          directDomains: ['direct.example'], directIps: ['2001:db8::/32']), exit: _exit);
    expect(terminalRoute(config, '2001:db8:9::1', domain: 'direct.example'), 'tailnet');
    expect(terminalRoute(config, 'fd00::1', private: true), 'tailnet');
  });

  test('rule lists project domains and keep exceptions direct', () {
    final projection = RuleLists.projectActive(_list, '[AutoProxy 0.2.9]\n||supplement.example');
    expect(projection.proxy.suffix, contains('blocked.example'));
    expect(projection.proxy.suffix, contains('supplement.example'));
    expect(projection.proxy.suffix, contains('openai.com'));
    expect(projection.direct.suffix, ['allowed.blocked.example']);
    expect(projection.proxy.regex.single, r'^(?:[^.]+\.)*wild[^.]*\.example$');
    expect(projection.skipped, 1, reason: 'path-specific rules are never promoted');
  });

  test('rule lists decode Base64 input and reject HTML', () {
    const encoded = 'W0F1dG9Qcm94eSAwLjIuOV0KfHxibG9ja2VkLmV4YW1wbGU=';
    expect(RuleLists.parse(encoded).proxyCount, 1);
    expect(
      () => RuleLists.parse('<!doctype html><html><body>nope</body></html>'),
      throwsA(isA<RuleListException>()),
    );
  });

  test('split routes rules through the exit and leaves the rest direct', () {
    final config = ConfigBuilder.buildObject(
      settings: const MobileSettings(),
      exit: _exit,
      rules: RuleLists.projectActive(_list, '[AutoProxy 0.2.9]\n||supplement.example'),
    );
    final route = config['route'] as Map;
    expect(route['final'], 'direct');
    expect((config['outbounds'] as List).single, containsPair('tag', 'direct'));
    final endpoint = (config['endpoints'] as List).single as Map;
    expect(endpoint['exit_node'], '100.88.12.4');
    expect(endpoint['state_directory'], '/shared/tailscale');
    final routes = _routes(config);
    expect(routes.first, containsPair('action', 'hijack-dns'));
    expect(
      routes.where((r) => r['outbound'] == ConfigBuilder.exitTag && r['rule_set'] != null),
      isNotEmpty,
    );
    expect(routes.where((r) => r['ip_is_private'] == true && r['action'] == 'route').single,
        {'ip_is_private': true, 'action': 'route', 'outbound': 'direct'});
    expect(routes.last, {'ip_version': 6, 'action': 'reject'});
    final dns = config['dns'] as Map;
    expect(dns['final'], 'dns-direct');
    expect(
      (dns['servers'] as List).map((s) => (s as Map)['tag']),
      containsAll(['dns-direct', 'dns-proxy', 'dns-tailnet', 'dns-fake']),
    );
  });

  test('global sends everything through the exit with no direct outbound', () {
    final config = ConfigBuilder.buildObject(
      settings: const MobileSettings(mode: RoutingMode.global),
      exit: _exit,
    );
    expect((config['route'] as Map)['final'], ConfigBuilder.exitTag);
    expect(config['outbounds'], isEmpty);
    expect((config['route'] as Map).containsKey('rule_set'), isFalse);
    expect((config['dns'] as Map)['final'], 'dns-proxy');
    expect(_routes(config).any((r) => r['outbound'] == 'direct'), isFalse);
  });

  test('unmatched traffic can use the exit in split mode', () {
    final config = ConfigBuilder.buildObject(
      settings: const MobileSettings(unmatched: UnmatchedRoute.exit, source: RuleSource.none),
      exit: _exit,
    );
    expect((config['route'] as Map)['final'], ConfigBuilder.exitTag);
    expect((config['dns'] as Map)['final'], 'dns-proxy');
  });

  test('custom resolvers keep their port and system means local', () {
    final config = ConfigBuilder.buildObject(
      settings: const MobileSettings(proxyDns: '1.1.1.1:5353', directDns: ''),
      exit: _exit,
    );
    final servers = ((config['dns'] as Map)['servers'] as List).cast<Map>();
    expect(servers.firstWhere((s) => s['tag'] == 'dns-direct')['type'], 'local');
    final proxy = servers.firstWhere((s) => s['tag'] == 'dns-proxy');
    expect(proxy['server'], '1.1.1.1');
    expect(proxy['server_port'], 5353);
    expect(proxy['detour'], ConfigBuilder.exitTag);
  });

  test('settings survive a JSON round trip', () {
    const settings = MobileSettings(
      mode: RoutingMode.global,
      unmatched: UnmatchedRoute.exit,
      proxyDomains: ['a.example'],
      language: AppLanguage.zh,
      mtu: 1280,
    );
    final restored = MobileSettings.fromJson(settings.toJson());
    expect(restored.affectsConnection(settings), isFalse);
    expect(restored.language, AppLanguage.zh);
  });
}
