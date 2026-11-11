import 'dart:math' as math;
import 'dart:ui' show ImageFilter;

import 'package:adaptive_platform_ui/adaptive_platform_ui.dart';
import 'package:flutter/cupertino.dart';
import 'package:flutter/material.dart';

import '../theme/tokens.dart';

/// UIKit toolbars live above Flutter's navigator. Hide their page actions
/// while a Flutter sheet presents its own navigation bar.
abstract final class NativeChrome {
  static final sheetPresented = ValueNotifier(false);
}

/// A toolbar action rendered as an SF Symbol glass button on iOS and an
/// icon button on Android.
class PageAction {
  const PageAction({
    required this.label,
    required this.iosSymbol,
    required this.icon,
    this.onPressed,
    this.menu,
    this.onMenuSelected,
  });

  final String label;
  final String iosSymbol;
  final IconData icon;
  final VoidCallback? onPressed;
  final List<AdaptivePopupMenuEntry>? menu;
  final void Function(int index)? onMenuSelected;
}

/// A top-level or pushed page. iOS: native Liquid Glass toolbar with a
/// Montserrat large title that collapses into the bar as it scrolls away.
/// Android: Material 3 large top app bar.
class NativePage extends StatefulWidget {
  const NativePage({
    super.key,
    required this.title,
    required this.slivers,
    this.actions = const [],
    this.largeTitle = true,
    this.bottom,
    this.onRefresh,
    this.backgroundColor,
    this.scrollController,
  });

  final String title;
  final List<Widget> slivers;
  final List<PageAction> actions;
  final bool largeTitle;

  /// Pinned above the tab bar, e.g. the Reconnect glass bar.
  final Widget? bottom;
  final Future<void> Function()? onRefresh;
  final Color? backgroundColor;

  /// Supplied when the page needs its scroll position, e.g. to follow logs.
  final ScrollController? scrollController;

  @override
  State<NativePage> createState() => _NativePageState();
}

class _NativePageState extends State<NativePage> {
  late final ScrollController _scroll = widget.scrollController ?? ScrollController();
  bool _inlineTitle = false;

  /// Space for the bar pinned above the tab region, so the last row stays
  /// reachable behind it.
  static const _bottomBarSpace = 64.0;

  void _onScroll() {
    final show = !widget.largeTitle || (_scroll.hasClients && _scroll.offset > 36);
    if (show != _inlineTitle) setState(() => _inlineTitle = show);
  }

  @override
  void initState() {
    super.initState();
    _inlineTitle = !widget.largeTitle;
    _scroll.addListener(_onScroll);
  }

  @override
  void dispose() {
    _scroll.removeListener(_onScroll);
    if (widget.scrollController == null) _scroll.dispose();
    super.dispose();
  }

  /// Large titles grow with Dynamic Type but stop before they crowd content.
  Widget _largeTitle(BuildContext context, double size) => MediaQuery.withClampedTextScaling(
        maxScaleFactor: 1.5,
        child: Text(widget.title, style: brandTitle(context, size: size)),
      );

  @override
  Widget build(BuildContext context) => isCupertino
      ? ValueListenableBuilder<bool>(
          valueListenable: NativeChrome.sheetPresented,
          builder: (context, presented, _) =>
              _buildCupertino(context, sheetPresented: presented),
        )
      : _buildMaterial(context);

  Widget _buildCupertino(BuildContext context, {required bool sheetPresented}) {
    final top = MediaQuery.paddingOf(context).top;
    final background = widget.backgroundColor ??
        CupertinoColors.systemGroupedBackground.resolveFrom(context);
    final scroll = CustomScrollView(
      controller: _scroll,
      physics: const AlwaysScrollableScrollPhysics(parent: BouncingScrollPhysics()),
      slivers: [
        SliverPadding(padding: EdgeInsets.only(top: top + 56)),
        if (widget.onRefresh != null)
          CupertinoSliverRefreshControl(onRefresh: widget.onRefresh),
        if (widget.largeTitle)
          SliverToBoxAdapter(
            child: Padding(
              padding: const EdgeInsets.fromLTRB(20, 4, 20, 6),
              child: Semantics(header: true, child: _largeTitle(context, 32)),
            ),
          ),
        ...widget.slivers,
        SliverPadding(
          padding: EdgeInsets.only(
            bottom: MediaQuery.paddingOf(context).bottom + 96 +
                (widget.bottom == null ? 0 : _bottomBarSpace),
          ),
        ),
      ],
    );
    return AdaptiveScaffold(
      backgroundColor: background,
      appBar: sheetPresented
          ? null
          : AdaptiveAppBar(
              title: _inlineTitle ? widget.title : '',
              useNativeToolbar: true,
              // The native toolbar has no disabled state; an unavailable
              // action is left out instead of shown as a dead button.
              actions: [
                for (final action in widget.actions)
                  if (action.onPressed != null || action.menu != null) _iosAction(action),
              ],
            ),
      body: ColoredBox(
        color: background,
        child: Stack(
          children: [
            scroll,
            if (widget.bottom != null)
              Positioned(
                left: 0,
                right: 0,
                bottom: MediaQuery.paddingOf(context).bottom + 64,
                child: widget.bottom!,
              ),
          ],
        ),
      ),
    );
  }

  AdaptiveAppBarAction _iosAction(PageAction action) => AdaptiveAppBarAction(
        iosSymbol: action.iosSymbol,
        icon: action.icon,
        label: action.label,
        onPressed: action.onPressed ?? () {},
        menuItems: action.menu,
        onMenuItemSelected: action.onMenuSelected == null
            ? null
            : (index, _) => action.onMenuSelected!(index),
      );

  Widget _buildMaterial(BuildContext context) {
    final scheme = Theme.of(context).colorScheme;
    final actions = [
      for (final action in widget.actions)
        if (action.menu != null)
          PopupMenuButton<int>(
            tooltip: action.label,
            icon: Icon(action.icon),
            onSelected: action.onMenuSelected,
            itemBuilder: (context) => [
              for (final (index, entry) in action.menu!.indexed)
                if (entry is AdaptivePopupMenuItem)
                  CheckedPopupMenuItem(
                    value: index,
                    checked: entry.icon == 'checkmark',
                    child: Text(entry.label),
                  ),
            ],
          )
        else
          IconButton(
            tooltip: action.label,
            icon: Icon(action.icon),
            onPressed: action.onPressed,
          ),
      const SizedBox(width: 4),
    ];
    final title = widget.largeTitle ? _largeTitle(context, 30) : Text(widget.title);
    Widget scroll = CustomScrollView(
      controller: _scroll,
      slivers: [
        if (widget.largeTitle)
          SliverAppBar.large(
            title: title,
            actions: actions,
            backgroundColor: widget.backgroundColor ?? scheme.surfaceContainerLowest,
            scrolledUnderElevation: 3,
          )
        else
          SliverAppBar(
            pinned: true,
            title: title,
            actions: actions,
            backgroundColor: widget.backgroundColor ?? scheme.surfaceContainerLowest,
          ),
        ...widget.slivers,
        SliverPadding(
          padding: EdgeInsets.only(bottom: 88 + (widget.bottom == null ? 0 : _bottomBarSpace)),
        ),
      ],
    );
    if (widget.onRefresh != null) {
      scroll = RefreshIndicator(onRefresh: widget.onRefresh!, child: scroll);
    }
    return Scaffold(
      backgroundColor: widget.backgroundColor ?? scheme.surfaceContainerLowest,
      body: Stack(
        children: [
          scroll,
          if (widget.bottom != null)
            Positioned(left: 0, right: 0, bottom: 12, child: widget.bottom!),
        ],
      ),
    );
  }
}

/// Inset grouped list section (iOS) or a Material 3 grouped list surface.
class NativeSection extends StatelessWidget {
  const NativeSection({
    super.key,
    this.header,
    this.footer,
    required this.children,
  });

  final String? header;
  final Widget? footer;
  final List<Widget> children;

  @override
  Widget build(BuildContext context) {
    if (isCupertino) {
      return CupertinoListSection.insetGrouped(
        // Native inset-grouped headers: footnote size, secondary label.
        header: header == null
            ? null
            : Text(
                header!,
                style: TextStyle(
                  fontSize: 13,
                  fontWeight: FontWeight.w400,
                  letterSpacing: -0.08,
                  color: CupertinoColors.secondaryLabel.resolveFrom(context),
                ),
              ),
        footer: footer == null
            ? null
            : DefaultTextStyle.merge(
                style: TextStyle(
                  fontSize: 13,
                  color: CupertinoColors.secondaryLabel.resolveFrom(context),
                ),
                child: footer!,
              ),
        backgroundColor: CupertinoColors.systemGroupedBackground.resolveFrom(context),
        margin: const EdgeInsetsDirectional.fromSTEB(20, 0, 20, 10),
        // Clip the complete group, including row press feedback, to the same
        // larger continuous corners instead of Cupertino's fixed small radius.
        decoration: const BoxDecoration(color: Colors.transparent),
        clipBehavior: Clip.none,
        children: [
          ClipRSuperellipse(
            borderRadius: BorderRadius.circular(TsSpace.groupRadius),
            child: ColoredBox(
              color: CupertinoColors.secondarySystemGroupedBackground.resolveFrom(context),
              child: Column(
                children: [
                  for (final (index, child) in children.indexed) ...[
                    if (index > 0)
                      Padding(
                        padding: const EdgeInsetsDirectional.only(start: 58),
                        child: SizedBox(
                          width: double.infinity,
                          height: .5,
                          child: ColoredBox(color: CupertinoColors.separator.resolveFrom(context)),
                        ),
                      ),
                    child,
                  ],
                ],
              ),
            ),
          ),
        ],
      );
    }
    final scheme = Theme.of(context).colorScheme;
    final text = Theme.of(context).textTheme;
    final rows = <Widget>[];
    for (final (index, child) in children.indexed) {
      if (index > 0) rows.add(Divider(height: 1, indent: 20, endIndent: 20, color: scheme.outlineVariant.withValues(alpha: .6)));
      rows.add(child);
    }
    return Padding(
      padding: const EdgeInsets.fromLTRB(16, 8, 16, 8),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          if (header != null)
            Padding(
              padding: const EdgeInsets.fromLTRB(20, 8, 20, 8),
              child: Text(header!, style: text.labelLarge?.copyWith(color: scheme.primary)),
            ),
          Material(
            color: scheme.surfaceContainer,
            shape: RoundedRectangleBorder(borderRadius: BorderRadius.circular(TsSpace.groupRadius)),
            clipBehavior: Clip.antiAlias,
            child: Column(children: rows),
          ),
          if (footer != null)
            Padding(
              padding: const EdgeInsets.fromLTRB(20, 8, 20, 4),
              child: DefaultTextStyle.merge(
                style: text.bodySmall?.copyWith(color: scheme.onSurfaceVariant),
                child: footer!,
              ),
            ),
        ],
      ),
    );
  }
}

/// A single-line card-list item. Leading icons are required so every row
/// shares the same alignment. Explanations belong in the section footer.
class NativeRow extends StatelessWidget {
  const NativeRow({
    super.key,
    required this.title,
    required this.leading,
    this.trailing,
    this.value,
    this.onTap,
    this.chevron = false,
    this.destructive = false,
    this.enabled = true,
    this.semanticsLabel,
  });

  final Widget title;
  final Widget leading;
  final Widget? trailing;
  final String? value;
  final VoidCallback? onTap;
  final bool chevron;
  final bool destructive;
  final bool enabled;
  final String? semanticsLabel;

  @override
  Widget build(BuildContext context) => LayoutBuilder(
        builder: (context, constraints) {
          final tap = enabled ? onTap : null;
          Widget bounded(Widget child) => ConstrainedBox(
                constraints: BoxConstraints(maxWidth: constraints.maxWidth * .5),
                child: DefaultTextStyle.merge(
                  maxLines: 1,
                  softWrap: false,
                  overflow: TextOverflow.ellipsis,
                  child: child,
                ),
              );
          final styledTitle = DefaultTextStyle.merge(
            maxLines: 1,
            softWrap: false,
            overflow: TextOverflow.ellipsis,
            style: TextStyle(
              color: destructive
                  ? (isCupertino
                      ? CupertinoColors.systemRed.resolveFrom(context)
                      : Theme.of(context).colorScheme.error)
                  : !enabled
                      ? (isCupertino
                          ? CupertinoColors.tertiaryLabel.resolveFrom(context)
                          : Theme.of(context).disabledColor)
                      : null,
            ),
            child: title,
          );
          if (isCupertino) {
            // Menus reserve a 44pt hit target; UISwitch is shorter. Row geometry
            // belongs to the list, not the trailing control's intrinsic height.
            // All rows grow together for large text while remaining single-line.
            final rowHeight = math.max(
              TsSpace.rowHeight,
              MediaQuery.textScalerOf(context).scale(17) * 1.3 + TsSpace.rowGap,
            );
            return Semantics(
              label: semanticsLabel,
              button: tap != null,
              enabled: enabled,
              child: SizedBox(
                height: rowHeight,
                child: CupertinoListTile.notched(
                  leading: leading,
                  leadingSize: 22,
                  padding: const EdgeInsetsDirectional.fromSTEB(20, 6, 14, 6),
                  // CupertinoListTile's title column otherwise top-aligns its
                  // only child when the row receives a tight height.
                  title: SizedBox(
                    height: rowHeight - TsSpace.rowGap,
                    child: Align(alignment: AlignmentDirectional.centerStart, child: styledTitle),
                  ),
                  additionalInfo: value == null
                      ? null
                      : bounded(Text(value!, maxLines: 1, overflow: TextOverflow.ellipsis)),
                  trailing: trailing != null
                      ? bounded(trailing!)
                      : chevron
                          ? const CupertinoListTileChevron()
                          : null,
                  onTap: tap,
                ),
              ),
            );
          }
          final scheme = Theme.of(context).colorScheme;
          final Widget? rowTrailing = trailing ??
              (value != null || chevron
                  ? Row(
                      mainAxisSize: MainAxisSize.min,
                      children: [
                        if (value != null)
                          Flexible(
                            child: Text(
                              value!,
                              maxLines: 1,
                              overflow: TextOverflow.ellipsis,
                              style: TextStyle(color: scheme.onSurfaceVariant),
                            ),
                          ),
                        if (chevron) ...[
                          const SizedBox(width: 4),
                          Icon(Icons.chevron_right, color: scheme.onSurfaceVariant),
                        ],
                      ],
                    )
                  : null);
          return ListTile(
            enabled: enabled,
            leading: leading,
            title: styledTitle,
            trailing: rowTrailing == null ? null : bounded(rowTrailing),
            onTap: tap,
          );
        },
      );
}

/// Plain platform icon for a card-list row; state colours are supplied only
/// where they convey connection, selection or a destructive action.
class NativeRowIcon extends StatelessWidget {
  const NativeRowIcon({super.key, required this.ios, required this.android, this.color});

  final IconData ios;
  final IconData android;
  final Color? color;

  @override
  Widget build(BuildContext context) => ExcludeSemantics(
        child: Icon(
          isCupertino ? ios : android,
          size: 21,
          color: color ?? secondaryLabelColor(context),
        ),
      );
}

/// Native switch: UISwitch (Liquid Glass) on iOS 26+, Material 3 on Android.
class NativeSwitch extends StatelessWidget {
  const NativeSwitch({super.key, required this.value, required this.onChanged, this.label});

  final bool value;
  final ValueChanged<bool>? onChanged;
  final String? label;

  @override
  Widget build(BuildContext context) => Semantics(
        label: label,
        toggled: value,
        child: AdaptiveSwitch(
          value: value,
          onChanged: onChanged,
          activeColor: isCupertino ? TsColors.of(context, TsColors.tint) : null,
        ),
      );
}

/// Translucent bar using the system material, pinned above the tab bar.
/// It replaces the desktop's blurred settings actionbar.
class GlassBar extends StatelessWidget {
  const GlassBar({super.key, required this.child});

  final Widget child;

  @override
  Widget build(BuildContext context) {
    if (!isCupertino) {
      final scheme = Theme.of(context).colorScheme;
      return Padding(
        padding: const EdgeInsets.symmetric(horizontal: 16),
        child: Material(
          color: scheme.inverseSurface,
          elevation: 3,
          shape: RoundedRectangleBorder(borderRadius: BorderRadius.circular(16)),
          child: DefaultTextStyle.merge(
            style: TextStyle(color: scheme.onInverseSurface),
            child: Padding(
              padding: const EdgeInsets.fromLTRB(16, 6, 8, 6),
              child: child,
            ),
          ),
        ),
      );
    }
    // iOS 26+: UIVisualEffectView system material. Older iOS: the same
    // material approximated with a backdrop blur over the grouped background.
    final radius = BorderRadius.circular(26);
    final content = Padding(
      padding: const EdgeInsets.fromLTRB(18, 6, 6, 6),
      child: child,
    );
    return Padding(
      padding: const EdgeInsets.symmetric(horizontal: 16),
      child: PlatformInfo.isIOS26OrHigher()
          ? AdaptiveBlurView(
              blurStyle: BlurStyle.systemMaterial,
              borderRadius: radius,
              child: content,
            )
          : ClipRRect(
              borderRadius: radius,
              child: BackdropFilter(
                filter: ImageFilter.blur(sigmaX: 20, sigmaY: 20),
                child: ColoredBox(
                  color: CupertinoColors.secondarySystemGroupedBackground
                      .resolveFrom(context)
                      .withValues(alpha: .78),
                  child: content,
                ),
              ),
            ),
    );
  }
}

/// Primary filled button: Flutter Cupertino on iOS, Material on Android.
class PrimaryButton extends StatelessWidget {
  const PrimaryButton({super.key, required this.label, this.onPressed, this.busy = false});

  final String label;
  final VoidCallback? onPressed;
  final bool busy;

  @override
  Widget build(BuildContext context) {
    if (isCupertino) {
      return SizedBox(
        width: double.infinity,
        child: CupertinoButton.filled(
          onPressed: busy ? null : onPressed,
          color: TsColors.of(context, TsColors.tint),
          borderRadius: BorderRadius.circular(14),
          child: busy
              ? const CupertinoActivityIndicator(color: CupertinoColors.white)
              : Text(label, style: const TextStyle(fontWeight: FontWeight.w600)),
        ),
      );
    }
    return SizedBox(
      width: double.infinity,
      child: FilledButton(
        onPressed: busy ? null : onPressed,
        style: FilledButton.styleFrom(minimumSize: const Size.fromHeight(48)),
        child: busy
            ? const SizedBox.square(dimension: 20, child: CircularProgressIndicator(strokeWidth: 2))
            : Text(label),
      ),
    );
  }
}

/// Small text action used inside rows and banners.
class TextAction extends StatelessWidget {
  const TextAction({super.key, required this.label, this.onPressed});

  final String label;
  final VoidCallback? onPressed;

  @override
  Widget build(BuildContext context) => isCupertino
      ? CupertinoButton(
          padding: const EdgeInsets.symmetric(horizontal: 12),
          minimumSize: const Size(44, 44),
          onPressed: onPressed,
          child: Text(label, style: const TextStyle(fontWeight: FontWeight.w600)),
        )
      : TextButton(onPressed: onPressed, child: Text(label));
}

/// Platform activity indicator.
class Spinner extends StatelessWidget {
  const Spinner({super.key, this.size = 16});
  final double size;

  @override
  Widget build(BuildContext context) => isCupertino
      ? CupertinoActivityIndicator(radius: size / 2)
      : SizedBox.square(dimension: size, child: const CircularProgressIndicator(strokeWidth: 2));
}

/// Confirmation for destructive or interrupting decisions.
Future<bool> confirmDialog(
  BuildContext context, {
  required String title,
  required String message,
  required String confirm,
  required String cancel,
  bool destructive = false,
}) async {
  if (isCupertino) {
    final result = await showCupertinoDialog<bool>(
      context: context,
      builder: (context) => CupertinoAlertDialog(
        title: Text(title),
        content: Text(message),
        actions: [
          CupertinoDialogAction(
            isDefaultAction: true,
            onPressed: () => Navigator.pop(context, false),
            child: Text(cancel),
          ),
          CupertinoDialogAction(
            isDestructiveAction: destructive,
            onPressed: () => Navigator.pop(context, true),
            child: Text(confirm),
          ),
        ],
      ),
    );
    return result ?? false;
  }
  final result = await showDialog<bool>(
    context: context,
    builder: (context) => AlertDialog(
      title: Text(title),
      content: Text(message),
      actions: [
        TextButton(onPressed: () => Navigator.pop(context, false), child: Text(cancel)),
        TextButton(
          onPressed: () => Navigator.pop(context, true),
          style: destructive
              ? TextButton.styleFrom(foregroundColor: Theme.of(context).colorScheme.error)
              : null,
          child: Text(confirm),
        ),
      ],
    ),
  );
  return result ?? false;
}

/// Pushes a detail page with the platform's own transition.
Future<T?> pushPage<T>(BuildContext context, WidgetBuilder builder) =>
    Navigator.of(context).push<T>(
      isCupertino
          ? CupertinoPageRoute<T>(builder: builder)
          : MaterialPageRoute<T>(builder: builder),
    );
