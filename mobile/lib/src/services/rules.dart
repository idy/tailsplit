import 'dart:async';
import 'dart:convert';
import 'dart:io';
import 'dart:typed_data' show BytesBuilder;

import 'package:crypto/crypto.dart' show sha256;
import 'package:flutter/services.dart' show rootBundle;

import 'errors.dart';

/// Dart port of the desktop's `internal/domainrules` projection: AutoProxy or
/// plain domain lists become whole-domain sing-box rules. Path-specific URL
/// rules are skipped, never promoted to whole hosts. Non-ASCII hosts are
/// skipped rather than guessed, because Dart has no IDNA mapping built in.
class DomainProjection {
  const DomainProjection({
    this.exact = const [],
    this.suffix = const [],
    this.regex = const [],
  });

  final List<String> exact;
  final List<String> suffix;
  final List<String> regex;

  bool get isEmpty => exact.isEmpty && suffix.isEmpty && regex.isEmpty;

  /// sing-box headless rules for an inline rule set.
  List<Map<String, Object?>> toRules() => [
        if (exact.isNotEmpty) {'domain': exact},
        if (suffix.isNotEmpty) {'domain_suffix': suffix},
        if (regex.isNotEmpty) {'domain_regex': regex},
      ];
}

class RuleProjection {
  const RuleProjection({
    required this.proxy,
    required this.direct,
    required this.proxyCount,
    required this.directCount,
    required this.skipped,
    required this.sha256,
  });

  final DomainProjection proxy;
  final DomainProjection direct;
  final int proxyCount;
  final int directCount;
  final int skipped;
  final String sha256;
}

class RuleListException implements Exception {
  const RuleListException(this.message, {this.cause = FailureCause.invalid});
  final String message;
  final FailureCause cause;
  @override
  String toString() => message;
}

/// A loaded rule list and when its copy was downloaded.
class RuleLoad {
  const RuleLoad(this.projection, this.fetchedAt, {this.refreshError});
  final RuleProjection projection;
  final DateTime fetchedAt;

  /// Set when an update was attempted and failed; the cached copy is used.
  final Object? refreshError;
}

class _Rules {
  final exact = <String>{};
  final suffix = <String>{};
  final wildcard = <String>[];

  bool coversSuffix(String domain) {
    var rest = domain;
    while (true) {
      if (suffix.contains(rest)) return true;
      final i = rest.indexOf('.');
      if (i < 0) break;
      rest = rest.substring(i + 1);
    }
    for (final pattern in wildcard) {
      if (pattern.startsWith(r'^(?:[^.]+\.)*') && RegExp(pattern).hasMatch(domain)) {
        return true;
      }
    }
    return false;
  }

  DomainProjection project() => DomainProjection(
        exact: exact.toList()..sort(),
        suffix: suffix.toList()..sort(),
        regex: wildcard.toList()..sort(),
      );
}

abstract final class RuleLists {
  static const defaultUrl =
      'https://raw.githubusercontent.com/gfwlist/gfwlist/master/gfwlist.txt';
  static const _maxSize = 2 << 20;

  /// These domains moved upstream; keep prior coverage when the list lacks them.
  static const _applicationDomains = [
    'openai.com', 'chatgpt.com', 'anthropic.com', 'claude.ai', 'claude.com',
    'claude.app', 'claudeusercontent.com', 'claudemcpcontent.com',
  ];

  /// One cache file per source URL, so switching sources never falls back to
  /// another list's rules.
  static File cacheFile(Directory directory, String url) => File(
        '${directory.path}/${sha256.convert(utf8.encode(url)).toString().substring(0, 24)}.txt',
      );

  /// When the cached copy of [url] was downloaded, or null if there is none.
  static Future<DateTime?> cachedAt(Directory directory, String url) async {
    try {
      final file = cacheFile(directory, url);
      return await file.exists() ? await file.lastModified() : null;
    } on FileSystemException {
      return null;
    }
  }

  /// Like the desktop, a list downloads on first use and is then reused from
  /// the cache; updating it is explicit ([refresh]). A failed refresh keeps the
  /// cached copy and reports why in [RuleLoad.refreshError].
  static Future<RuleLoad> load(
    String url,
    Directory directory, {
    bool refresh = false,
    Future<String> Function(Uri uri)? download,
  }) async {
    final cache = cacheFile(directory, url);
    final supplement = await _supplement();
    String? cached;
    try {
      if (await cache.exists()) cached = await cache.readAsString();
    } on FileSystemException {
      cached = null;
    }
    if (cached != null && !refresh) {
      try {
        return RuleLoad(projectActive(cached, supplement), await cache.lastModified());
      } on RuleListException {
        cached = null; // A damaged cache is replaced by a fresh download.
      }
    }
    try {
      final text = await (download ?? _download)(Uri.parse(url));
      final projection = projectActive(text, supplement); // Validate first.
      await cache.parent.create(recursive: true);
      final temp = File('${cache.path}.tmp');
      await temp.writeAsString(text, flush: true);
      await temp.rename(cache.path);
      return RuleLoad(projection, DateTime.now());
    } catch (error) {
      if (cached == null) rethrow;
      return RuleLoad(
        projectActive(cached, supplement),
        await cache.lastModified(),
        refreshError: error,
      );
    }
  }

  static String? _bundledSupplement;
  static Future<String> _supplement() async =>
      _bundledSupplement ??= await rootBundle.loadString('assets/rules/supplemental.txt');

  /// Test seam: supplies the bundled supplement without an asset bundle.
  static set supplementForTesting(String value) => _bundledSupplement = value;

  static Future<String> _download(Uri uri) async {
    if (uri.scheme != 'https' || uri.host.isEmpty) {
      throw const RuleListException('rule lists must use an HTTPS URL');
    }
    final client = HttpClient()..connectionTimeout = const Duration(seconds: 10);
    try {
      final request = await client.getUrl(uri).timeout(const Duration(seconds: 10));
      final response = await request.close().timeout(const Duration(seconds: 20));
      if (response.statusCode != 200) {
        throw RuleListException(
          'HTTP ${response.statusCode}',
          cause: response.statusCode >= 500 ? FailureCause.server : FailureCause.rejected,
        );
      }
      final bytes = BytesBuilder(copy: false);
      await for (final chunk in response.timeout(const Duration(seconds: 20))) {
        bytes.add(chunk);
        if (bytes.length > _maxSize) {
          throw const RuleListException('rule list exceeds 2 MiB');
        }
      }
      return utf8.decode(bytes.takeBytes(), allowMalformed: true);
    } on SocketException catch (error) {
      throw RuleListException(error.osError?.message ?? error.message, cause: FailureCause.offline);
    } on HttpException catch (error) {
      throw RuleListException(error.message, cause: FailureCause.offline);
    } on HandshakeException {
      throw const RuleListException('TLS handshake failed', cause: FailureCause.tls);
    } on TimeoutException {
      throw const RuleListException('timed out', cause: FailureCause.timeout);
    } finally {
      client.close(force: true);
    }
  }

  /// Projection including the bundled supplement, keeping exceptions first.
  static RuleProjection projectActive(String list, String supplementText) {
    final parsed = parse(list);
    final extra = StringBuffer(supplementText);
    for (final domain in _applicationDomains) {
      if (!parsed.proxy.coversSuffix(domain) && !parsed.direct.coversSuffix(domain)) {
        extra.write('\n||$domain');
      }
    }
    final supplement = parse(extra.toString());
    final proxy = parsed.proxy
      ..exact.addAll(supplement.proxy.exact)
      ..suffix.addAll(supplement.proxy.suffix)
      ..wildcard.addAll(supplement.proxy.wildcard.where(
          (pattern) => !parsed.proxy.wildcard.contains(pattern)));
    return RuleProjection(
      proxy: proxy.project(),
      direct: parsed.direct.project(),
      proxyCount: parsed.proxyCount,
      directCount: parsed.directCount,
      skipped: parsed.skipped,
      sha256: parsed.sha256,
    );
  }

  static ({
    _Rules proxy,
    _Rules direct,
    int proxyCount,
    int directCount,
    int skipped,
    String sha256,
  }) parse(String input) {
    String trim(String text) => text.replaceFirst('﻿', '').trim();
    var data = trim(input);
    if (data.isEmpty) throw const RuleListException('empty rule list');
    // Domain and AutoProxy text contains punctuation outside Base64; decode
    // only input that is entirely valid Base64.
    final compact = data.replaceAll(RegExp(r'\s+'), '');
    if (RegExp(r'^[A-Za-z0-9+/]+={0,2}$').hasMatch(compact)) {
      try {
        data = trim(utf8.decode(base64.decode(base64.normalize(compact))));
      } on FormatException {
        // Not Base64 after all; parse as text.
      }
    }
    if (RegExp(r'<\s*(!doctype|html|head|body)\b', caseSensitive: false).hasMatch(data)) {
      throw const RuleListException('HTML is not a rule list');
    }
    final proxy = _Rules();
    final direct = _Rules();
    var proxyCount = 0, directCount = 0, skipped = 0;
    for (var line in const LineSplitter().convert(data)) {
      line = line.trim();
      if (line.isEmpty || line.startsWith('!') || line.startsWith('#')) continue;
      if (line.startsWith('[AutoProxy ')) {
        if (!line.endsWith(']')) {
          throw const RuleListException('invalid rule list header');
        }
        continue;
      }
      final exception = line.startsWith('@@');
      if (exception) line = line.substring(2);
      final projected = _projectRule(line);
      if (projected == null) {
        skipped++;
        continue;
      }
      final destination = exception ? direct : proxy;
      exception ? directCount++ : proxyCount++;
      final (domain, suffix) = projected;
      if (domain.contains('*')) {
        var pattern = RegExp.escape(domain).replaceAll(r'\*', '[^.]*');
        if (suffix) pattern = r'(?:[^.]+\.)*' + pattern;
        destination.wildcard.add('^$pattern\$');
      } else if (suffix) {
        destination.suffix.add(domain);
      } else {
        destination.exact.add(domain);
      }
    }
    if (proxyCount == 0) {
      throw const RuleListException('no usable proxy domains in rule list');
    }
    return (
      proxy: proxy,
      direct: direct,
      proxyCount: proxyCount,
      directCount: directCount,
      skipped: skipped,
      sha256: sha256.convert(utf8.encode(data)).toString(),
    );
  }

  static (String, bool)? _projectRule(String line) {
    var suffix = true;
    if (line.startsWith('||')) {
      line = line.substring(2);
    } else if (line.startsWith('|')) {
      line = line.substring(1);
      suffix = false;
    } else if (line.startsWith('.')) {
      line = line.substring(1);
    }
    if (line.startsWith('http://') || line.startsWith('https://')) {
      line = line.substring(line.indexOf('://') + 3);
      suffix = false;
    }
    for (final end in ['|', '^', '/']) {
      if (line.endsWith(end)) line = line.substring(0, line.length - 1);
    }
    if (RegExp(r'[/:?$#|^\\\s]').hasMatch(line)) return null;
    final domain = canonicalDomain(line.replaceAll('*', 'wildcard'));
    if (domain == null || !domain.contains('.')) return null;
    if (InternetAddress.tryParse(domain) != null) return null;
    if (line.contains('*')) {
      if (line.codeUnits.any((c) => c > 127)) return null;
      var host = line.toLowerCase();
      if (host.endsWith('.')) host = host.substring(0, host.length - 1);
      return (host, suffix);
    }
    return (domain, suffix);
  }

  /// Lower-case ASCII hostname validation; returns null when invalid.
  static String? canonicalDomain(String input) {
    var domain = input.trim().toLowerCase();
    if (domain.endsWith('.')) domain = domain.substring(0, domain.length - 1);
    if (domain.isEmpty || domain.length > 253) return null;
    for (final label in domain.split('.')) {
      if (label.isEmpty || label.length > 63) return null;
      if (label.startsWith('-') || label.endsWith('-')) return null;
      if (!RegExp(r'^[a-z0-9-]+$').hasMatch(label)) return null;
    }
    return domain;
  }
}
