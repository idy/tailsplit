import 'package:flutter/cupertino.dart';
import 'package:flutter/foundation.dart' show defaultTargetPlatform;
import 'package:flutter/material.dart';

/// TailSplit desktop tokens (DESIGN.md) mapped to native colour roles.
///
/// Only the brand tint and semantic states come from the desktop panel; surfaces,
/// labels and separators stay on each platform's own system colours so they
/// follow appearance, contrast and accessibility settings.
abstract final class TsColors {
  /// Desktop `accent`, the one interactive tint. Dark appearance lifts it for
  /// legibility as text on near-black, as desktop `accent-ink-dark` does.
  static const tint = CupertinoDynamicColor.withBrightnessAndContrast(
    color: Color(0xFF2F62D5),
    darkColor: Color(0xFF5B8CF5),
    highContrastColor: Color(0xFF2456C2),
    darkHighContrastColor: Color(0xFF8AB4FF),
  );

  /// Connected, saved, the chosen node's check (desktop `good`).
  static const good = CupertinoDynamicColor.withBrightness(
    color: Color(0xFF167536),
    darkColor: Color(0xFF30D158),
  );

  /// Attention, pending choice, signed in without a usable node (desktop `warn`).
  static const warn = CupertinoDynamicColor.withBrightness(
    color: Color(0xFF855700),
    darkColor: Color(0xFFF0C46B),
  );

  /// Failure (desktop `error`).
  static const error = CupertinoDynamicColor.withBrightness(
    color: Color(0xFFB42318),
    darkColor: Color(0xFFFF8A80),
  );

  /// Softened latency/state readouts (desktop `latency-*`).
  static const softGood = CupertinoDynamicColor.withBrightness(
    color: Color(0xFF3A8154),
    darkColor: Color(0xFF7AB58B),
  );

  /// Diagnostic terminal: fixed deep gray in both appearances (desktop logs).
  static const logBackground = Color(0xFF272A32);
  static const logInk = Color(0xFFE5E5EA);
  static const logMuted = Color(0xFFAEB4C0);
  static const logCyan = Color(0xFF70CBD6);
  static const logMagenta = Color(0xFFDCA0E6);
  static const logWarn = Color(0xFFF0C46B);
  static const logError = Color(0xFFFF8A80);
  static const logGood = Color(0xFF7AB58B);
  static const logLine = Color(0x1FFFFFFF);

  /// Resolves a dynamic token for the current appearance on either platform.
  static Color of(BuildContext context, CupertinoDynamicColor color) =>
      CupertinoDynamicColor.resolve(color, context);
}

/// Spacing and radii shared with the desktop system where they still apply on
/// touch screens. Touch targets follow each platform's minimum instead.
abstract final class TsSpace {
  static const double compact = 4;
  static const double controlGap = 8;
  static const double rowGap = 12;
  static const double inset = 16;
  static const double section = 24;
  static const double panelRadius = 16;
  static const double groupRadius = 24;
  static const double dialogRadius = 20;
}

/// The wordmark/display face. All other UI text is the system face.
const brandFamily = 'Montserrat';

/// True where the app renders Apple's controls (iOS and iPadOS).
bool get isCupertino => defaultTargetPlatform == TargetPlatform.iOS;

/// Primary text colour from the platform's own system roles.
Color labelColor(BuildContext context) => isCupertino
    ? CupertinoColors.label.resolveFrom(context)
    : Theme.of(context).colorScheme.onSurface;

/// Secondary text colour from the platform's own system roles.
Color secondaryLabelColor(BuildContext context) => isCupertino
    ? CupertinoColors.secondaryLabel.resolveFrom(context)
    : Theme.of(context).colorScheme.onSurfaceVariant;

/// Montserrat display title, scaled with the system text size.
TextStyle brandTitle(BuildContext context, {double size = 30}) => TextStyle(
      fontFamily: brandFamily,
      fontSize: size,
      fontWeight: FontWeight.w600,
      fontVariations: const [FontVariation('wght', 600)],
      letterSpacing: -0.4,
      height: 1.15,
      color: labelColor(context),
    );
