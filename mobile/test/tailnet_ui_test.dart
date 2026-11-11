import 'dart:async';

import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:tailsplit_mobile/src/app.dart';
import 'package:tailsplit_mobile/src/ui/native.dart';
import 'package:tailsplit_mobile/src/ui/node_picker.dart';

import 'multi_tailnet_test.dart' show Fixture, fixture;

Future<({Fixture f, String a, String b})> setup(WidgetTester tester, {bool selected = true}) async {
  tester.view.physicalSize = const Size(402, 874);
  tester.view.devicePixelRatio = 1;
  addTearDown(tester.view.resetPhysicalSize);
  addTearDown(tester.view.resetDevicePixelRatio);
  late Fixture f;
  late String a, b;
  await tester.runAsync(() async {
    f = await fixture();
    a = await f.add('a', name: 'Personal');
    b = await f.add('b', name: 'Studio');
    if (selected) await f.choose(a);
    f.app.pickerRequested = false;
  });
  await tester.pumpWidget(TailSplitApp(controller: f.app));
  await tester.pumpAndSettle();
  return (f: f, a: a, b: b);
}

void main() {
  final platforms = TargetPlatformVariant({TargetPlatform.android, TargetPlatform.iOS});

  testWidgets('signed-in Tailnets remain accessible before selecting any exit', (tester) async {
    final data = await setup(tester, selected: false);
    expect(find.text('Personal'), findsOneWidget);
    expect(find.text('Studio'), findsOneWidget);
    expect(find.text('Add Tailnet'), findsOneWidget);
    expect(find.text('No Tailnets yet'), findsNothing);
    expect(data.f.app.exit, isNull);
    await tester.tap(find.byKey(ValueKey('tailnet-${data.b}')));
    await tester.pumpAndSettle();
    expect(find.byType(NodePicker), findsOneWidget);
    expect(find.text('exit-b'), findsOneWidget);
    expect(find.text('exit-a'), findsNothing);
    expect(data.f.app.exit, isNull);
    await tester.pumpWidget(const SizedBox.shrink());
    NativeChrome.sheetPresented.value = false;
  }, variant: platforms);

  testWidgets('canceling another Tailnet picker keeps the selected exit', (tester) async {
    final data = await setup(tester);
    await tester.tap(find.byKey(ValueKey('tailnet-${data.b}')));
    await tester.pumpAndSettle();
    await tester.tap(find.text('exit-b'));
    await tester.pump();
    await tester.tap(find.text('Cancel'));
    await tester.pumpAndSettle();
    expect(data.f.app.selectedTailnetId, data.a);
    expect(data.f.app.exit!.nodeId, 'exit-a');
    expect(data.f.app.tailnets.length, 2);
    await tester.pumpWidget(const SizedBox.shrink());
  }, variant: platforms);

  testWidgets('confirm replaces the sole selection across Tailnets', (tester) async {
    final data = await setup(tester);
    await tester.tap(find.byKey(ValueKey('tailnet-${data.b}')));
    await tester.pumpAndSettle();
    await tester.tap(find.text('exit-b'));
    await tester.pump();
    await tester.runAsync(() async {
      final changed = Completer<void>();
      void listener() {
        if (data.f.app.selectedTailnetId == data.b && !changed.isCompleted) changed.complete();
      }
      data.f.app.addListener(listener);
      try {
        await tester.tap(find.text('Confirm'));
        await changed.future.timeout(const Duration(seconds: 3));
      } finally {
        data.f.app.removeListener(listener);
      }
    });
    await tester.pumpAndSettle();
    expect(find.byType(NodePicker), findsNothing);
    expect(data.f.app.selectedTailnetId, data.b);
    expect(data.f.app.exit!.nodeId, 'exit-b');
    expect(data.f.app.tailnets.length, 2);
    expect(data.f.vpn.starts, 0);
    await tester.pumpWidget(const SizedBox.shrink());
  }, variant: platforms);
}
