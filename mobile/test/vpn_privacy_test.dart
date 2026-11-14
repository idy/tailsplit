import 'dart:async';
import 'dart:io';

import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:tailsplit_mobile/src/app.dart';
import 'package:tailsplit_mobile/src/model/settings.dart';
import 'package:tailsplit_mobile/src/services/storage.dart';
import 'package:tailsplit_mobile/src/services/vpn.dart' show VpnConnectionState;
import 'package:tailsplit_mobile/src/state/app_controller.dart';
import 'package:tailsplit_mobile/src/tailscale/models.dart' show TailnetSnapshot;
import 'package:tailsplit_mobile/src/ui/root_shell.dart';
import 'package:tailsplit_mobile/src/ui/settings_page.dart';
import 'package:tailsplit_mobile/src/ui/vpn_privacy_page.dart';

import 'multi_tailnet_test.dart' show AccountVpn, fixture, snapshot;

class DeferredInitVpn extends AccountVpn {
  final initializing = Completer<void>();
  final ready = Completer<void>();

  @override
  Future<void> initialize() {
    if (!initializing.isCompleted) initializing.complete();
    return ready.future;
  }
}

Future<void> waitForApp(AppController app, bool Function() ready) async {
  if (ready()) return;
  final done = Completer<void>();
  void listener() {
    if (ready() && !done.isCompleted) done.complete();
  }
  app.addListener(listener);
  try {
    await done.future.timeout(const Duration(seconds: 3));
  } finally {
    app.removeListener(listener);
  }
}

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

  test('saved Tailnets refresh once after consent without waiting for the network', () async {
    final f = await fixture();
    final id = await f.add('a');
    await f.app.updateSettings(f.app.settings.copyWith(vpnDisclosureAccepted: false));
    var fetches = 0;
    final entered = Completer<void>();
    final response = Completer<TailnetSnapshot>();
    addTearDown(() { if (!response.isCompleted) response.complete(snapshot('a')); });
    f.client.fetch = (_) {
      fetches++;
      if (!entered.isCompleted) entered.complete();
      return response.future;
    };
    final restored = await f.reload();
    expect(fetches, 0);
    final blocker = Directory('${f.paths.settingsFile.path}.tmp');
    await blocker.create();
    expect(await restored.acceptVpnDisclosure(), isFalse);
    expect(fetches, 0, reason: 'Failed consent persistence cannot start network access.');
    await blocker.delete();

    expect(await Future.wait([
      restored.acceptVpnDisclosure(), restored.acceptVpnDisclosure(),
    ]).timeout(const Duration(seconds: 3)), [true, true]);
    await entered.future.timeout(const Duration(seconds: 3));
    expect(restored.mayUseNetwork, isTrue);
    expect(fetches, 1);
    expect(response.isCompleted, isFalse, reason: 'Acceptance must finish before the network response.');
    response.complete(snapshot('a', name: 'Refreshed network'));
    await waitForApp(restored, () => !restored.refreshing);
    expect(restored.tailnetById(id)!.networkName, 'Refreshed network');
    expect(await restored.acceptVpnDisclosure(), isTrue);
    await pumpEventQueue();
    expect(fetches, 1, reason: 'Repeated acceptance must not repeat the startup refresh.');
  });

  for (final nativeActive in [false, true]) {
    test('consent during startup respects the final native state (active=$nativeActive)', () async {
      final f = await fixture();
      final id = await f.add('a');
      await f.app.updateSettings(f.app.settings.copyWith(vpnDisclosureAccepted: false));
      var fetches = 0;
      f.client.fetch = (_) async { fetches++; return snapshot('a', name: 'Updated'); };
      final vpn = DeferredInitVpn();
      if (nativeActive) vpn.state = VpnConnectionState.connected;
      final app = AppController(paths: f.paths, vpn: vpn, controlClient: () => f.client);
      final loading = app.load();
      addTearDown(() async {
        if (!vpn.ready.isCompleted) vpn.ready.complete();
        await loading;
        app.dispose();
        await vpn.events.close();
      });
      await vpn.initializing.future.timeout(const Duration(seconds: 3));
      expect(app.settingsLoaded, isTrue);
      expect(await app.acceptVpnDisclosure().timeout(const Duration(seconds: 3)), isTrue);
      expect(fetches, 0, reason: 'Do not refresh until saved accounts and OS state are known.');
      vpn.ready.complete();
      await loading;
      if (!nativeActive) await waitForApp(app, () => app.tailnetById(id)!.networkName == 'Updated');
      await pumpEventQueue();
      expect(fetches, nativeActive ? 0 : 1);
      expect(vpn.starts, 0);
      expect(vpn.stops, 0);
    });
  }

  testWidgets('returning user never sees consent in production runApp-then-load order', (tester) async {
    final f = await tester.runAsync(() => fixture(load: false));
    await tester.runAsync(() => JsonStore.write(f!.paths.settingsFile,
        const MobileSettings(vpnDisclosureAccepted: true).toJson()));
    await tester.pumpWidget(TailSplitApp(controller: f!.app));
    expect(find.byKey(const ValueKey('app-startup')), findsOneWidget);
    expect(find.byType(VpnPrivacyPage), findsNothing);
    expect(find.text('Agree and continue'), findsNothing);
    await tester.runAsync(f.app.load);
    await tester.pumpAndSettle();
    expect(find.byType(RootShell), findsOneWidget);
    expect(find.byType(VpnPrivacyPage), findsNothing);
    await tester.pumpWidget(const SizedBox.shrink());
  }, variant: TargetPlatformVariant({TargetPlatform.iOS, TargetPlatform.android}));

  testWidgets('first launch displays disclosure before actions, and acceptance opens the app', (tester) async {
    final f = await tester.runAsync(() => fixture(load: false));
    await tester.pumpWidget(TailSplitApp(controller: f!.app));
    expect(find.byKey(const ValueKey('app-startup')), findsOneWidget);
    expect(find.byType(VpnPrivacyPage), findsNothing);
    await tester.runAsync(f.app.load);
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
    expect(find.text('Tailscale 隐私政策'), findsOneWidget);
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
