import 'package:adaptive_platform_ui/adaptive_platform_ui.dart';
import 'package:flutter/material.dart';
import 'package:flutter_localizations/flutter_localizations.dart';

import 'l10n/strings.dart';
import 'model/settings.dart';
import 'state/app_controller.dart';
import 'theme/theme.dart';
import 'ui/root_shell.dart';
import 'ui/vpn_privacy_page.dart';

/// Makes the controller available to every page and rebuilds on change.
class AppScope extends InheritedNotifier<AppController> {
  const AppScope({super.key, required AppController controller, required super.child})
      : super(notifier: controller);

  static AppController of(BuildContext context) =>
      context.dependOnInheritedWidgetOfExactType<AppScope>()!.notifier!;

  static AppController read(BuildContext context) =>
      context.getInheritedWidgetOfExactType<AppScope>()!.notifier!;
}

class TailSplitApp extends StatelessWidget {
  const TailSplitApp({super.key, required this.controller});

  final AppController controller;

  @override
  Widget build(BuildContext context) => AppScope(
        controller: controller,
        child: ListenableBuilder(
          listenable: controller,
          builder: (context, _) {
            final language = controller.settings.language;
            return AdaptiveApp(
              title: 'TailSplit',
              themeMode: ThemeMode.system,
              cupertinoLightTheme: TsTheme.cupertino(Brightness.light),
              cupertinoDarkTheme: TsTheme.cupertino(Brightness.dark),
              materialLightTheme: TsTheme.material(Brightness.light),
              materialDarkTheme: TsTheme.material(Brightness.dark),
              locale: switch (language) {
                AppLanguage.en => const Locale('en'),
                AppLanguage.zh => const Locale('zh'),
                AppLanguage.system => null,
              },
              supportedLocales: S.supportedLocales,
              localizationsDelegates: const [
                GlobalMaterialLocalizations.delegate,
                GlobalCupertinoLocalizations.delegate,
                GlobalWidgetsLocalizations.delegate,
              ],
              home: controller.mayUseNetwork
                  ? const RootShell()
                  : const VpnPrivacyPage(requestConsent: true),
            );
          },
        ),
      );
}
