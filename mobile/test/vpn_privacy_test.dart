import 'dart:async';
import 'dart:io';

import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:tailsplit_mobile/src/app.dart';
import 'package:tailsplit_mobile/src/model/settings.dart';
import 'package:tailsplit_mobile/src/services/storage.dart';
import 'package:tailsplit_mobile/src/state/app_controller.dart';
import 'package:tailsplit_mobile/src/ui/settings_page.dart';
import 'package:tailsplit_mobile/src/ui/vpn_privacy_page.dart';

import 'multi_tailnet_test.dart' show AccountVpn, fixture;

void main() {
  TestWidgetsFlutterBinding.ensureInitialized();

  test('older or malformed preferences require explicit acceptance', () {
    for (final json in <Map<String, Object?>>[
      {},
      {'vpn_disclosure_accepted': 'true'},
      {'vpn_disclosure_accepted': 1},
    ]) {
      expect(MobileSettings.fromJson(json).vpnDisclosureAccepted, isFalse);
    }
    final accepted = const MobileSettings().copyWith(vpnDisclosureAccepted: true);
    expect(MobileSettings.fromJson(accepted.toJson()).vpnDisclosureAccepted, isTrue);
    expect(accepted.affectsConnection(const MobileSettings()), isFalse);
  });

  test('saved accounts cannot refresh, sign in, download rules or connect before consent', () async {
    final f = await fixture();
    final id = await f.add('a');
    await f.choose(id);
    final logins = f.client.logins.length;
    var fetches = 0;
    f.client.fetch = (_) async { fetches++; throw StateError('Unexpected network access'); };
    await f.app.updateSettings(f.app.settings.copyWith(vpnDisclosureAccepted: false, source: RuleSource.builtin));
    var downloads = 0;
    final vpn = AccountVpn();
    final restored = AppController(paths: f.paths, vpn: vpn, controlClient: () => f.client,
        ruleDownload: (_) async { downloads++; throw StateError('Unexpected rule download'); });
    addTearDown(() async { restored.dispose(); await vpn.events.close(); });
    await restored.load();
    await restored.signInExit();
    expect(await restored.refresh(tailnetId: id), isFalse);
    await restored.updateRules();
    await restored.connect();
    await restored.reconnect();
    expect(restored.canConnect, isFalse);
    expect(restored.preparing, isFalse);
    expect(restored.rulesUpdating, isFalse);
    expect(fetches, 0);
    expect(downloads, 0);
    expect(vpn.starts, 0);
    expect(vpn.stops, 0);
    expect(f.client.logins.length, logins);
    expect(restored.selectedTailnetId, id);
  });

  test('an existing VPN can be stopped without accepting the new disclosure', () async {
    final f = await fixture();
    final id = await f.add('a');
    await f.choose(id);
    await f.app.connect();
    expect(f.vpn.starts, 1);
    await f.app.updateSettings(f.app.settings.copyWith(vpnDisclosureAccepted: false));
    expect(await f.app.disconnect(), isTrue);
    expect(f.vpn.stops, 1);
    expect(f.app.mayUseNetwork, isFalse);
  });

  test('failed persistence keeps network actions blocked and permits retry', () async {
    final f = await fixture(load: false);
    await f.app.load();
    final blocker = Directory('${f.paths.settingsFile.path}.tmp');
    await blocker.create(recursive: true);
    expect(await f.app.acceptVpnDisclosure(), isFalse);
    expect(f.app.mayUseNetwork, isFalse);
    expect(f.app.problem, AppProblem.storage);
    await blocker.delete();
    expect(await f.app.acceptVpnDisclosure(), isTrue);
    expect(f.app.mayUseNetwork, isTrue);
    expect(f.app.problem, isNull);
    expect(f.vpn.starts, 0);
    final restored = await f.reload();
    expect(restored.mayUseNetwork, isTrue);
    expect(f.client.logins, isEmpty);
    expect((await JsonStore.read(f.paths.settingsFile))!['vpn_disclosure_accepted'], isTrue);
  });

  testWidgets('first launch displays disclosure before actions, and acceptance opens the app', (tester) async {
    final f = await tester.runAsync(() => fixture(load: false));
    await tester.runAsync(() => f!.app.load());
    await tester.pumpWidget(TailSplitApp(controller: f!.app));
    await tester.pumpAndSettle();
    expect(find.byType(VpnPrivacyPage), findsOneWidget);
    expect(find.text('Add Tailnet'), findsNothing);
    expect(f.client.logins, isEmpty);
    await tester.scrollUntilVisible(find.text('Agree and continue'), 400);
    expect(find.text('Privacy Policy'), findsOneWidget);
    await tester.runAsync(() async {
      final accepted = Completer<void>();
      void listener() {
        if (f.app.mayUseNetwork && !accepted.isCompleted) accepted.complete();
      }
      f.app.addListener(listener);
      try {
        await tester.tap(find.text('Agree and continue'));
        await accepted.future.timeout(const Duration(seconds: 3));
      } finally {
        f.app.removeListener(listener);
      }
    });
    await tester.pumpAndSettle();
    expect(find.byType(VpnPrivacyPage), findsNothing);
    expect(find.text('Add Tailnet'), findsOneWidget);
    expect(f.app.settings.vpnDisclosureAccepted, isTrue);
    expect(f.vpn.starts, 0);
    await tester.pumpWidget(const SizedBox.shrink());
  }, variant: TargetPlatformVariant({TargetPlatform.iOS, TargetPlatform.android}));

  testWidgets('Chinese disclosure remains scrollable at large text size', (tester) async {
    final f = await tester.runAsync(() => fixture(load: false));
    await tester.runAsync(() => f!.app.load());
    f!.app.settings = const MobileSettings(language: AppLanguage.zh);
    tester.view.physicalSize = const Size(320, 568);
    tester.view.devicePixelRatio = 1;
    addTearDown(tester.view.resetPhysicalSize);
    addTearDown(tester.view.resetDevicePixelRatio);
    tester.platformDispatcher.textScaleFactorTestValue = 2;
    addTearDown(tester.platformDispatcher.clearTextScaleFactorTestValue);
    await tester.pumpWidget(TailSplitApp(controller: f.app));
    await tester.pumpAndSettle();
    await tester.scrollUntilVisible(find.text('同意并继续'), 400);
    expect(find.text('隐私政策'), findsOneWidget);
    expect(tester.takeException(), isNull);
    expect(f.app.mayUseNetwork, isFalse);
    await tester.pumpWidget(const SizedBox.shrink());
  }, variant: TargetPlatformVariant.only(TargetPlatform.iOS));

  testWidgets('settings retains disclosure and privacy-policy entry after acceptance', (tester) async {
    final f = await tester.runAsync(() => fixture());
    await tester.pumpWidget(TailSplitApp(controller: f!.app));
    await tester.pumpAndSettle();
    await tester.tap(find.text('Settings'));
    await tester.pumpAndSettle();
    expect(find.byType(SettingsPage), findsOneWidget);
    final settingsScroll = find.descendant(of: find.byType(SettingsPage), matching: find.byType(Scrollable));
    await tester.scrollUntilVisible(find.text('VPN & Privacy').hitTestable(), 400, scrollable: settingsScroll);
    expect(find.text('Privacy Policy'), findsOneWidget);
    await tester.tap(find.text('VPN & Privacy'));
    await tester.pumpAndSettle();
    expect(find.byType(VpnPrivacyPage), findsOneWidget);
    expect(find.text('Agree and continue'), findsNothing);
    await tester.pumpWidget(const SizedBox.shrink());
  });
}
