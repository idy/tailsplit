import 'dart:io';

import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:tailsplit_mobile/src/app.dart';
import 'package:tailsplit_mobile/src/services/errors.dart';
import 'package:tailsplit_mobile/src/services/storage.dart';
import 'package:tailsplit_mobile/src/state/app_controller.dart';
import 'package:tailsplit_mobile/src/tailscale/control_conn.dart'
    show ControlTransientException;
import 'package:tailsplit_mobile/src/tailscale/tailscale.dart';
import 'package:tailsplit_mobile/src/theme/theme.dart';
import 'package:tailsplit_mobile/src/ui/logs_page.dart';

void main() {
  testWidgets('bounded logs retain order, batch notifications and expose a live read-only view', (tester) async {
    final buffer = LogBuffer();
    addTearDown(buffer.dispose);
    final view = buffer.lines;
    var notifications = 0;
    buffer.addListener(() => notifications++);

    for (var i = 0; i < LogBuffer.maxLines + 32; i++) {
      buffer.add(LogLine('info', 'line $i'));
    }
    expect(view.length, LogBuffer.maxLines);
    expect(view.first.message, 'line 32');
    expect(view[100].message, 'line 132');
    expect(view.last.message, 'line ${LogBuffer.maxLines + 31}');
    expect(() => view.clear(), throwsUnsupportedError);
    expect(notifications, 0);

    await tester.pump(const Duration(milliseconds: 119));
    expect(notifications, 0);
    await tester.pump(const Duration(milliseconds: 1));
    expect(notifications, 1);

    buffer.add(LogLine('info', 'pending'));
    buffer.clear();
    expect(view, isEmpty);
    expect(notifications, 2);
    await tester.pump(const Duration(milliseconds: 120));
    expect(notifications, 2, reason: 'Clear cancels the pending batch.');
  });

  testWidgets('hidden Logs defers rebuilding until reactivated', (tester) async {
    final root = Directory.systemTemp.createTempSync('tailsplit-log-review');
    final app = AppController(paths: AppPaths.at(root, root));
    final visible = ValueNotifier(false);
    addTearDown(() {
      app.dispose();
      visible.dispose();
      root.deleteSync(recursive: true);
    });

    await tester.pumpWidget(
      MaterialApp(
        theme: TsTheme.material(Brightness.light),
        home: AppScope(
          controller: app,
          child: ValueListenableBuilder<bool>(
            valueListenable: visible,
            builder: (context, enabled, child) => TickerMode(
              enabled: enabled,
              child: child!,
            ),
            child: const LogsPage(),
          ),
        ),
      ),
    );
    app.logBuffer.add(LogLine('info', 'collected while hidden'));
    await tester.pump(const Duration(milliseconds: 120));
    expect(find.textContaining('collected while hidden', findRichText: true), findsNothing);

    visible.value = true;
    await tester.pumpAndSettle();
    expect(find.textContaining('collected while hidden', findRichText: true), findsOneWidget);

    app.logBuffer.add(LogLine('info', 'collected while visible'));
    await tester.pump(const Duration(milliseconds: 120));
    await tester.pumpAndSettle();
    expect(find.textContaining('collected while visible', findRichText: true), findsOneWidget);
    expect(tester.takeException(), isNull);
    await tester.pumpWidget(const SizedBox.shrink());
  });

  test('theme instances are reused while retaining their appearance', () {
    for (final brightness in Brightness.values) {
      expect(identical(TsTheme.material(brightness), TsTheme.material(brightness)), isTrue);
      expect(identical(TsTheme.cupertino(brightness), TsTheme.cupertino(brightness)), isTrue);
      expect(TsTheme.material(brightness).brightness, brightness);
      expect(TsTheme.cupertino(brightness).brightness, brightness);
    }
    expect(identical(TsTheme.material(Brightness.light), TsTheme.material(Brightness.dark)), isFalse);
  });

  test('control categories do not depend on diagnostic wording or retry policy', () {
    const causes = {
      ControlFailureKind.offline: FailureCause.offline,
      ControlFailureKind.timeout: FailureCause.timeout,
      ControlFailureKind.tls: FailureCause.tls,
      ControlFailureKind.rateLimited: FailureCause.rateLimited,
      ControlFailureKind.server: FailureCause.server,
      ControlFailureKind.rejected: FailureCause.rejected,
      ControlFailureKind.cancelled: FailureCause.cancelled,
    };
    for (final entry in causes.entries) {
      for (final message in ['Changed diagnostic', '连接失败']) {
        expect(classifyFailure(TailscaleControlException(message, kind: entry.key)), entry.value);
        final transient = ControlTransientException(
          message,
          kind: entry.key,
          retryAfter: const Duration(seconds: 10),
        );
        expect(classifyFailure(transient), entry.value);
        expect(transient.retryAfter, const Duration(seconds: 10));
      }
    }
    expect(classifyFailure(const TailscaleNeedsLogin()), FailureCause.rejected);
    expect(classifyFailure(const TailscaleControlException('Unknown response')), FailureCause.server);
  });
}
