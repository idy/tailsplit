import 'dart:io';

import 'package:adaptive_platform_ui/adaptive_platform_ui.dart';
import 'package:flutter/cupertino.dart';
import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:tailsplit_mobile/src/app.dart';
import 'package:tailsplit_mobile/src/services/storage.dart';
import 'package:tailsplit_mobile/src/state/app_controller.dart';
import 'package:tailsplit_mobile/src/ui/exit_page.dart';
import 'package:tailsplit_mobile/src/ui/native.dart';
import 'package:tailsplit_mobile/src/ui/settings_page.dart';

void main() {
  testWidgets('disabled older-iOS choice menu cannot open its action sheet', (tester) async {
    // The host test runner uses the package's pre-iOS-26 Flutter fallback.
    expect(PlatformInfo.isIOS26OrHigher(), isFalse);
    final selections = <int>[];
    final enabled = ValueNotifier(false);
    addTearDown(enabled.dispose);
    await tester.pumpWidget(
      MaterialApp(
        home: Scaffold(
          body: Center(
            child: ValueListenableBuilder<bool>(
              valueListenable: enabled,
              builder: (context, value, _) => ChoiceMenu<int>(
                label: 'Rule-based',
                options: const [(0, 'Rule-based'), (1, 'Global (Kill-Switch)')],
                selected: 0,
                enabled: value,
                onSelected: selections.add,
              ),
            ),
          ),
        ),
      ),
    );

    await tester.tap(find.text('Rule-based'), warnIfMissed: false);
    await tester.pumpAndSettle();
    expect(find.byType(CupertinoActionSheet), findsNothing);
    expect(selections, isEmpty);

    // Re-enabling retains both options and the ordinary selection behavior.
    enabled.value = true;
    await tester.pump();
    await tester.tap(find.text('Rule-based'));
    await tester.pumpAndSettle();
    expect(find.byType(CupertinoActionSheet), findsOneWidget);
    expect(find.widgetWithText(CupertinoActionSheetAction, 'Rule-based'), findsOneWidget);
    await tester.tap(find.widgetWithText(CupertinoActionSheetAction, 'Global (Kill-Switch)'));
    await tester.pumpAndSettle();
    expect(selections, [1]);

    enabled.value = false;
    await tester.pump();
    await tester.tap(find.text('Rule-based'), warnIfMissed: false);
    await tester.pumpAndSettle();
    expect(find.byType(CupertinoActionSheet), findsNothing);
    expect(selections, [1]);
    await tester.pumpWidget(const SizedBox.shrink());
  }, variant: TargetPlatformVariant.only(TargetPlatform.iOS));

  testWidgets('Android Back at Exit root leaves the activity', (tester) async {
    final platformPops = _recordPlatformPops(tester);
    await _pumpShell(tester);

    await _systemBack(tester);
    expect(platformPops, hasLength(1));
    await tester.pumpWidget(const SizedBox.shrink());
  }, variant: TargetPlatformVariant.only(TargetPlatform.android));

  for (final (index, label) in [(1, 'Logs'), (2, 'Settings')]) {
    testWidgets('Android Back at $label root returns to Exit before leaving', (tester) async {
      final platformPops = _recordPlatformPops(tester);
      await _pumpShell(tester);
      await _selectTab(tester, label);
      expect(_selectedTab(tester), index);

      await _systemBack(tester);
      expect(_selectedTab(tester), 0);
      expect(platformPops, isEmpty);
      await _systemBack(tester);
      expect(platformPops, hasLength(1));
      await tester.pumpWidget(const SizedBox.shrink());
    }, variant: TargetPlatformVariant.only(TargetPlatform.android));
  }

  testWidgets('Android Back pops one detail, preserves tabs, then leaves Exit', (tester) async {
    final platformPops = _recordPlatformPops(tester);
    await _pumpShell(tester);
    final exitNavigator = Navigator.of(tester.element(find.byType(ExitPage)));
    _pushDetail(exitNavigator.context, 'Exit detail');
    await tester.pumpAndSettle();

    await _selectTab(tester, 'Settings');
    final settingsNavigator = Navigator.of(tester.element(find.byType(SettingsPage)));
    _pushDetail(settingsNavigator.context, 'Settings detail');
    await tester.pumpAndSettle();
    _pushDetail(settingsNavigator.context, 'Deeper settings detail');
    await tester.pumpAndSettle();

    await _systemBack(tester);
    expect(find.text('Deeper settings detail'), findsNothing);
    expect(find.text('Settings detail'), findsOneWidget);
    expect(_selectedTab(tester), 2, reason: 'Popping a detail must not also switch tabs.');
    expect(exitNavigator.canPop(), isTrue, reason: 'The hidden Exit stack must not pop.');

    await _systemBack(tester);
    expect(settingsNavigator.canPop(), isFalse);
    expect(_selectedTab(tester), 2);
    expect(platformPops, isEmpty);

    await _systemBack(tester);
    expect(_selectedTab(tester), 0);
    expect(find.text('Exit detail'), findsOneWidget);
    await _systemBack(tester);
    expect(exitNavigator.canPop(), isFalse);
    expect(platformPops, isEmpty);
    await _systemBack(tester);
    expect(platformPops, hasLength(1));
    await tester.pumpWidget(const SizedBox.shrink());
  }, variant: TargetPlatformVariant.only(TargetPlatform.android));

  testWidgets('inactive detail stacks do not block Android Back at Exit root', (tester) async {
    final platformPops = _recordPlatformPops(tester);
    await _pumpShell(tester);
    await _selectTab(tester, 'Settings');
    final navigator = Navigator.of(tester.element(find.byType(SettingsPage)));
    _pushDetail(navigator.context, 'Saved settings detail');
    await tester.pumpAndSettle();

    await _selectTab(tester, 'Exit Node');
    await _systemBack(tester);
    expect(platformPops, hasLength(1));
    expect(navigator.canPop(), isTrue);
    // The platform mock keeps the widget alive, allowing stack verification.
    await _selectTab(tester, 'Settings');
    expect(find.text('Saved settings detail'), findsOneWidget);
    await tester.pumpWidget(const SizedBox.shrink());
  }, variant: TargetPlatformVariant.only(TargetPlatform.android));

  testWidgets('iOS detail edge swipe preserves the selected tab and other stacks', (tester) async {
    await _pumpShell(tester);
    final exitNavigator = Navigator.of(tester.element(find.byType(ExitPage)));
    _pushDetail(exitNavigator.context, 'Exit detail');
    await tester.pumpAndSettle();
    await _selectTab(tester, 'Settings');
    final settingsNavigator = Navigator.of(tester.element(find.byType(SettingsPage)));
    _pushDetail(settingsNavigator.context, 'Settings detail');
    await tester.pumpAndSettle();

    await tester.dragFrom(const Offset(1, 200), const Offset(650, 0));
    await tester.pumpAndSettle();
    expect(settingsNavigator.canPop(), isFalse);
    expect(_selectedTab(tester), 2);
    expect(exitNavigator.canPop(), isTrue);
    await _selectTab(tester, 'Exit Node');
    expect(find.text('Exit detail'), findsOneWidget);
    await tester.pumpWidget(const SizedBox.shrink());
  }, variant: TargetPlatformVariant.only(TargetPlatform.iOS));
}

Future<void> _pumpShell(WidgetTester tester) async {
  final root = Directory.systemTemp.createTempSync('tailsplit-navigation-review');
  final controller = AppController(paths: AppPaths.at(root, root));
  addTearDown(() {
    controller.dispose();
    root.deleteSync(recursive: true);
  });
  await tester.pumpWidget(TailSplitApp(controller: controller));
  await tester.pumpAndSettle();
}

List<MethodCall> _recordPlatformPops(WidgetTester tester) {
  final calls = <MethodCall>[];
  tester.binding.defaultBinaryMessenger.setMockMethodCallHandler(SystemChannels.platform, (
    call,
  ) async {
    if (call.method == 'SystemNavigator.pop') calls.add(call);
    return null;
  });
  addTearDown(
    () => tester.binding.defaultBinaryMessenger.setMockMethodCallHandler(
      SystemChannels.platform,
      null,
    ),
  );
  return calls;
}

Future<void> _systemBack(WidgetTester tester) async {
  await tester.binding.handlePopRoute();
  await tester.pumpAndSettle();
}

Future<void> _selectTab(WidgetTester tester, String label) async {
  await tester.tap(find.descendant(of: find.byType(NavigationBar), matching: find.text(label)));
  await tester.pumpAndSettle();
}

int _selectedTab(WidgetTester tester) =>
    tester.widget<NavigationBar>(find.byType(NavigationBar)).selectedIndex;

void _pushDetail(BuildContext context, String label) {
  pushPage<void>(context, (_) => CupertinoPageScaffold(child: Center(child: Text(label))));
}
