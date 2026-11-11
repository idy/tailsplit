import 'dart:async';
import 'dart:io';

import 'package:flutter_test/flutter_test.dart';
import 'package:tailsplit_mobile/src/model/settings.dart';
import 'package:tailsplit_mobile/src/services/rules.dart';
import 'package:tailsplit_mobile/src/services/storage.dart';
import 'package:tailsplit_mobile/src/state/app_controller.dart';

class _DelayedCacheFile extends Fake implements File {
  final started = Completer<void>();
  final timestamp = Completer<DateTime>();

  @override
  Future<bool> exists() async => true;

  @override
  Future<DateTime> lastModified() {
    if (!started.isCompleted) started.complete();
    return timestamp.future;
  }
}

final class _DelayedCacheIO extends IOOverrides {
  _DelayedCacheIO(this.path, this.file);
  final String path;
  final _DelayedCacheFile file;

  @override
  File createFile(String path) => path == this.path ? file : super.createFile(path);
}

void main() {
  TestWidgetsFlutterBinding.ensureInitialized();
  late Directory root;
  late AppPaths paths;
  late AppController app;

  setUp(() async {
    root = await Directory.systemTemp.createTemp('tailsplit-settings-order');
    paths = AppPaths.at(root, root);
    app = AppController(paths: paths);
  });
  tearDown(() async {
    app.dispose();
    await root.delete(recursive: true);
  });

  test('a delayed rule lookup cannot overwrite newer routing and DNS preferences', () async {
    const url = 'https://rules.example.test/list.txt';
    final delayed = _DelayedCacheFile();
    final cachePath = RuleLists.cacheFile(paths.rulesDirectory, url).path;
    await IOOverrides.runWithIOOverrides(() async {
      final older = app.updateSettings(app.settings.copyWith(source: RuleSource.custom, customSourceUrl: url));
      await delayed.started.future.timeout(const Duration(seconds: 3));
      final latest = app.settings.copyWith(mode: RoutingMode.global, proxyDns: '1.1.1.1', directDns: '9.9.9.9');
      try {
        await app.updateSettings(latest);
      } finally {
        delayed.timestamp.complete(DateTime.utc(2026, 11, 1));
        await older;
      }
      expect(app.settings.toJson(), latest.toJson());
      final restored = MobileSettings.fromJson((await JsonStore.read(paths.settingsFile))!);
      expect(restored.toJson(), latest.toJson(), reason: 'Restarting must restore the newest visible settings.');
    }, _DelayedCacheIO(cachePath, delayed));
  });

  test('a listener-triggered update persists after the update that notified it', () async {
    Future<void>? newer;
    var notified = false;
    void listener() {
      if (notified) return;
      notified = true;
      newer = app.updateSettings(app.settings.copyWith(directDns: '9.9.9.9'));
    }
    app.addListener(listener);
    try {
      await app.updateSettings(app.settings.copyWith(proxyDns: '1.1.1.1'));
      await newer;
      final restored = MobileSettings.fromJson((await JsonStore.read(paths.settingsFile))!);
      expect(restored.toJson(), app.settings.toJson());
      expect(restored.proxyDns, '1.1.1.1');
      expect(restored.directDns, '9.9.9.9');
    } finally {
      app.removeListener(listener);
    }
  });
}
