import 'package:flutter/cupertino.dart';
import 'package:flutter/material.dart';

import 'tokens.dart';

/// Native themes: Cupertino keeps Apple's system colours and San Francisco;
/// Material 3 uses a static TailSplit-blue scheme and Roboto. Only the tint and
/// semantic states come from the desktop panel.
abstract final class TsTheme {
  static final _cupertinoLight = _buildCupertino(Brightness.light);
  static final _cupertinoDark = _buildCupertino(Brightness.dark);
  static final _materialLight = _buildMaterial(Brightness.light);
  static final _materialDark = _buildMaterial(Brightness.dark);

  static CupertinoThemeData cupertino(Brightness brightness) =>
      brightness == Brightness.light ? _cupertinoLight : _cupertinoDark;

  static ThemeData material(Brightness brightness) =>
      brightness == Brightness.light ? _materialLight : _materialDark;

  static CupertinoThemeData _buildCupertino(Brightness brightness) => CupertinoThemeData(
        brightness: brightness,
        primaryColor: TsColors.tint,
        scaffoldBackgroundColor: CupertinoColors.systemGroupedBackground,
        barBackgroundColor: CupertinoColors.systemGroupedBackground,
      );

  static ThemeData _buildMaterial(Brightness brightness) {
    final base = ColorScheme.fromSeed(
      seedColor: const Color(0xFF2F62D5),
      brightness: brightness,
      dynamicSchemeVariant: DynamicSchemeVariant.tonalSpot,
    );
    final scheme = brightness == Brightness.light
        ? base.copyWith(primary: const Color(0xFF2F62D5), onPrimary: Colors.white)
        : base;
    return ThemeData(
      useMaterial3: true,
      brightness: brightness,
      colorScheme: scheme,
      scaffoldBackgroundColor: scheme.surfaceContainerLowest,
      listTileTheme: ListTileThemeData(
        contentPadding: const EdgeInsets.symmetric(horizontal: 20),
        minVerticalPadding: 12,
        iconColor: scheme.onSurfaceVariant,
      ),
      dividerTheme: DividerThemeData(color: scheme.outlineVariant, space: 1),
      snackBarTheme: const SnackBarThemeData(behavior: SnackBarBehavior.floating),
      pageTransitionsTheme: const PageTransitionsTheme(
        builders: {TargetPlatform.android: PredictiveBackPageTransitionsBuilder()},
      ),
    );
  }
}
