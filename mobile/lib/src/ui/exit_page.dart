import 'dart:async';

import 'package:adaptive_platform_ui/adaptive_platform_ui.dart';
import 'package:flutter/cupertino.dart';
import 'package:flutter/material.dart';

import '../app.dart';
import '../l10n/strings.dart';
import '../model/settings.dart';
import '../services/vpn.dart';
import '../state/app_controller.dart';
import '../theme/tokens.dart';
import 'common.dart';
import 'native.dart';
import 'node_picker.dart';

/// First tab: the connection switch, the routing choice and the one exit.
class ExitPage extends StatefulWidget {
  const ExitPage({super.key});

  @override
  State<ExitPage> createState() => _ExitPageState();
}

class _ExitPageState extends State<ExitPage> {
  // "Updated 2 min ago" stays truthful while the page is open.
  Timer? _clock;

  @override
  void initState() {
    super.initState();
    _clock = Timer.periodic(const Duration(seconds: 30), (_) {
      if (mounted) setState(() {});
    });
  }

  @override
  void dispose() {
    _clock?.cancel();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    final app = AppScope.of(context);
    final s = S.of(context);
    if (app.pickerRequested) {
      app.pickerRequested = false;
      WidgetsBinding.instance.addPostFrameCallback((_) {
        if (mounted) showNodePicker(context);
      });
    }
    final exit = app.exit;
    return NativePage(
      title: s.tabExit,
      onRefresh: exit == null ? null : app.refresh,
      actions: [
        if (exit != null)
          PageAction(
            label: s.refresh,
            iosSymbol: 'arrow.clockwise',
            icon: Icons.refresh,
            onPressed: app.refreshing || app.busy ? null : app.refresh,
          ),
      ],
      bottom: app.needsReconnect ? const ReconnectBar() : null,
      slivers: [
        const SliverToBoxAdapter(child: ProblemCard()),
        SliverToBoxAdapter(child: _ConnectionSection(app: app)),
        SliverToBoxAdapter(
          child: exit == null ? _AddExitSection(app: app) : _ExitSection(app: app),
        ),
      ],
    );
  }
}

class _ConnectionSection extends StatelessWidget {
  const _ConnectionSection({required this.app});
  final AppController app;

  @override
  Widget build(BuildContext context) {
    final s = S.of(context);
    final exit = app.exit;
    final (label, color) = switch (app.vpnState) {
      _ when app.preparing => (s.connecting, TsColors.warn),
      VpnConnectionState.connected => (s.connected, TsColors.good),
      VpnConnectionState.connecting => (s.connecting, TsColors.warn),
      VpnConnectionState.disconnecting => (s.disconnecting, TsColors.warn),
      VpnConnectionState.error => (s.connectionFailed, TsColors.error),
      VpnConnectionState.disconnected => (s.disconnected, null),
    };
    final step = switch (app.connectStep) {
      ConnectStep.checking => s.stepChecking,
      ConnectStep.rules => s.stepRules,
      ConnectStep.starting => s.stepStarting,
      null => null,
    };
    final summary = exit == null
        ? s.addExitToConnect
        : !exit.hasNode
            ? s.chooseNodeToConnect
            : step != null
                ? '$step ${s.turnOffToCancel}'
                : app.active && app.needsReconnect
                    ? s.reconnectToApply
                    : s.via(exit.displayName(exit.nodeName));
    return NativeSection(
      header: s.connection,
      footer: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [Text(summary), const SizedBox(height: 6), Text(s.routingFooter)],
      ),
      children: [
        NativeRow(
          leading: NativeRowIcon(
            ios: CupertinoIcons.power,
            android: Icons.power_settings_new,
            color: color == null ? null : TsColors.of(context, color),
          ),
          title: Text(label, style: const TextStyle(fontWeight: FontWeight.w600)),
          // The switch stays usable while connecting so it can cancel.
          trailing: NativeSwitch(
            label: s.connection,
            value: app.switchOn,
            onChanged: app.canToggleConnection ? app.setConnected : null,
          ),
        ),
        NativeRow(
          leading: const NativeRowIcon(ios: CupertinoIcons.arrow_branch, android: Icons.alt_route),
          title: Text(s.routing),
          trailing: RoutingMenu(app: app),
        ),
      ],
    );
  }
}

/// Unboxed pull-down for Rule-based / Global (Kill-Switch), like the desktop
/// titlebar dropdown. Saves immediately; an active session reconnects.
class RoutingMenu extends StatelessWidget {
  const RoutingMenu({super.key, required this.app});
  final AppController app;

  @override
  Widget build(BuildContext context) {
    final s = S.of(context);
    final global = app.settings.mode == RoutingMode.global;
    return ChoiceMenu<RoutingMode>(
      label: global ? s.global : s.ruleBased,
      enabled: !app.transitioning && !app.busy,
      options: [
        (RoutingMode.split, s.ruleBased),
        (RoutingMode.global, s.global),
      ],
      selected: app.settings.mode,
      onSelected: (mode) {
        if (mode != app.settings.mode) app.setRoutingMode(mode);
      },
    );
  }
}

class _AddExitSection extends StatelessWidget {
  const _AddExitSection({required this.app});
  final AppController app;

  @override
  Widget build(BuildContext context) {
    final s = S.of(context);
    final waiting = app.signIn != SignInPhase.idle;
    final secondary = secondaryLabelColor(context);
    return NativeSection(
      header: s.exit,
      children: [
        Padding(
          padding: const EdgeInsets.fromLTRB(20, 18, 20, 18),
          child: Column(
            crossAxisAlignment: CrossAxisAlignment.start,
            children: [
              Row(
                children: [
                  if (waiting) ...[const Spinner(), const SizedBox(width: 10)],
                  Expanded(
                    child: Text(
                      waiting ? s.signingIn : s.addExitTitle,
                      style: TextStyle(
                        fontSize: 17,
                        fontWeight: FontWeight.w600,
                        color: labelColor(context),
                      ),
                    ),
                  ),
                ],
              ),
              const SizedBox(height: 6),
              Text(
                waiting ? s.signingInBody : s.addExitBody,
                style: TextStyle(fontSize: 15, height: 1.35, color: secondary),
              ),
              const SizedBox(height: 16),
              if (!waiting)
                PrimaryButton(label: s.addExit, onPressed: () => signInWithConsent(context, app))
              else
                Row(
                  children: [
                    Expanded(
                      child: PrimaryButton(
                        label: s.openSignInAgain,
                        onPressed: app.hasAuthUrl ? app.openSignInPage : null,
                        busy: app.signIn != SignInPhase.browser,
                      ),
                    ),
                    const SizedBox(width: 8),
                    TextAction(
                      label: s.cancel,
                      onPressed: app.canCancelSignIn ? app.cancelSignIn : null,
                    ),
                  ],
                ),
            ],
          ),
        ),
      ],
    );
  }
}

class _ExitSection extends StatelessWidget {
  const _ExitSection({required this.app});
  final AppController app;

  @override
  Widget build(BuildContext context) {
    final s = S.of(context);
    final exit = app.exit!;
    final snapshot = exit.snapshot;
    final node = exit.node;
    final signedOut = app.problem == AppProblem.needsSignIn;
    final Color dot;
    if (signedOut) {
      dot = secondaryLabelColor(context);
    } else if (node != null && node.online) {
      dot = TsColors.of(context, TsColors.good);
    } else {
      dot = TsColors.of(context, TsColors.warn);
    }
    final awaitingApproval = snapshot != null && !snapshot.machineAuthorized;
    final subtitle = signedOut
        ? s.signedOut
        : awaitingApproval
            ? s.awaitingApproval
            : !exit.hasNode
                ? s.noNodeSelected
                : snapshot != null && node == null
                    ? '${exit.nodeName} · ${s.nodeMissing}'
                    : node == null || !node.online
                        ? '${exit.nodeName} · ${s.nodeOffline}'
                        : [exit.nodeName, exit.networkName].where((v) => v.isNotEmpty).join(' · ');
    final self = snapshot == null
        ? null
        : [snapshot.selfName, if (snapshot.selfAddresses.isNotEmpty) snapshot.selfAddresses.first]
            .join(' · ');
    return NativeSection(
      header: s.exit,
      footer: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          if (signedOut || awaitingApproval || !exit.hasNode || node?.online != true)
            Text(subtitle),
          if (self != null) Text(s.thisDevice(self)),
          if (snapshot != null)
            Text(
              app.refreshing ? s.refreshing : s.updated(relativeTime(s, snapshot.fetchedAt)),
            ),
        ],
      ),
      children: [
        NativeRow(
          leading: NativeRowIcon(
            ios: CupertinoIcons.globe,
            android: Icons.hub_outlined,
            color: dot,
          ),
          title: Text(
            exit.displayName(s.newExit),
            maxLines: 1,
            overflow: TextOverflow.ellipsis,
            style: const TextStyle(fontWeight: FontWeight.w500),
          ),
          trailing: signedOut
              ? TextAction(label: s.signIn, onPressed: () => signInWithConsent(context, app))
              : null,
          chevron: !signedOut,
          enabled: !app.removing,
          onTap: signedOut ? null : () => showNodePicker(context),
        ),
      ],
    );
  }
}

/// Native pull-down showing the current choice, with a check on the selected
/// option (UIMenu on iOS 26+, Material menu on Android).
class ChoiceMenu<T> extends StatelessWidget {
  const ChoiceMenu({
    super.key,
    required this.label,
    required this.options,
    required this.selected,
    required this.onSelected,
    this.enabled = true,
  });

  final String label;
  final List<(T, String)> options;
  final T selected;
  final ValueChanged<T> onSelected;
  final bool enabled;

  @override
  Widget build(BuildContext context) {
    if (!isCupertino) {
      final scheme = Theme.of(context).colorScheme;
      return MenuAnchor(
        menuChildren: [
          for (final (value, text) in options)
            MenuItemButton(
              leadingIcon: Icon(
                Icons.check,
                color: value == selected ? TsColors.of(context, TsColors.good) : Colors.transparent,
              ),
              onPressed: () => onSelected(value),
              child: Text(text),
            ),
        ],
        builder: (context, controller, _) => TextButton.icon(
          onPressed: enabled ? () => controller.isOpen ? controller.close() : controller.open() : null,
          iconAlignment: IconAlignment.end,
          icon: const Icon(Icons.arrow_drop_down),
          style: TextButton.styleFrom(foregroundColor: scheme.onSurfaceVariant),
          label: Text(label),
        ),
      );
    }
    final items = [
      for (final (value, text) in options)
        AdaptivePopupMenuItem<T>(
          label: text,
          value: value,
          icon: value == selected ? 'checkmark' : null,
        ),
    ];
    final nativeMenu = PlatformInfo.isIOS26OrHigher();
    final menu = AdaptivePopupMenuButton.widget<T>(
      items: items,
      onSelected: (index, entry) {
        if (enabled) onSelected(options[index].$1);
      },
      child: _MenuLabel(label: label, enabled: nativeMenu || enabled),
    );
    // Both the UIKit menu and the older action-sheet trigger must be inert.
    return Semantics(
      button: true,
      enabled: enabled,
      child: IgnorePointer(
        ignoring: !enabled,
        child: nativeMenu ? Opacity(opacity: enabled ? 1 : .45, child: menu) : menu,
      ),
    );
  }
}

class _MenuLabel extends StatelessWidget {
  const _MenuLabel({required this.label, this.enabled = true});
  final String label;
  final bool enabled;

  @override
  Widget build(BuildContext context) {
    final color = enabled
        ? CupertinoColors.secondaryLabel.resolveFrom(context)
        : CupertinoColors.tertiaryLabel.resolveFrom(context);
    return ConstrainedBox(
      constraints: const BoxConstraints(minHeight: 44),
      child: Row(
        mainAxisSize: MainAxisSize.min,
        children: [
          Flexible(
            child: Text(
              label,
              maxLines: 1,
              overflow: TextOverflow.ellipsis,
              style: TextStyle(fontSize: 17, color: color),
            ),
          ),
          const SizedBox(width: 4),
          Icon(CupertinoIcons.chevron_up_chevron_down, size: 15, color: color),
        ],
      ),
    );
  }
}
