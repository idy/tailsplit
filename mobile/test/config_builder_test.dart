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

void main() {
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
    expect(routes.last, {'ip_is_private': true, 'action': 'route', 'outbound': 'direct'});
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
