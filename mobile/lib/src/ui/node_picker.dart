import 'package:flutter/cupertino.dart';
import 'package:flutter/material.dart';

import '../app.dart';
import '../l10n/strings.dart';
import '../state/app_controller.dart';
import '../tailscale/tailscale.dart';
import '../theme/tokens.dart';
import 'common.dart';
import 'native.dart';

/// Opens the exit's node picker: an iOS sheet or a Material bottom sheet.
/// Cancel and swipe-down discard the staged choice; Confirm saves it.
Future<void> showNodePicker(BuildContext context) async {
  final app = AppScope.read(context);
  if (app.exit == null) return;
  if (isCupertino) {
    if (NativeChrome.sheetPresented.value) return;
    NativeChrome.sheetPresented.value = true;
    try {
      await showCupertinoSheet<void>(
        context: context,
        scrollableBuilder: (_, scroll) =>
            AppScope(controller: app, child: NodePicker(scrollController: scroll)),
      );
    } finally {
      NativeChrome.sheetPresented.value = false;
    }
    return;
  }
  return showModalBottomSheet<void>(
    context: context,
    useRootNavigator: true,
    isScrollControlled: true,
    useSafeArea: true,
    showDragHandle: true,
    builder: (_) => AppScope(
      controller: app,
      child: const FractionallySizedBox(heightFactor: .92, child: NodePicker()),
    ),
  );
}

class NodePicker extends StatefulWidget {
  const NodePicker({super.key, this.scrollController});

  /// The sheet's controller, so dragging the list down dismisses the sheet.
  final ScrollController? scrollController;

  @override
  State<NodePicker> createState() => _NodePickerState();
}

class _NodePickerState extends State<NodePicker> {
  String? _staged;
  bool _saving = false;
  bool _saveFailed = false;

  @override
  void initState() {
    super.initState();
    // Keep the list current without a Refresh button (desktop parity): an
    // idle app quietly updates a netmap older than half a minute.
    WidgetsBinding.instance.addPostFrameCallback((_) {
      if (!mounted) return;
      final app = AppScope.read(context);
      final fetched = app.exit?.snapshot?.fetchedAt;
      final stale = fetched == null || DateTime.now().difference(fetched) > const Duration(seconds: 30);
      if (stale && !app.switchOn) app.refresh(silent: true);
    });
  }

  @override
  Widget build(BuildContext context) {
    final app = AppScope.of(context);
    final s = S.of(context);
    final exit = app.exit;
    if (exit == null) return const SizedBox.shrink();
    final snapshot = exit.snapshot;
    final nodes = snapshot?.exitNodes ?? const <TailnetPeer>[];
    final staged = _staged ?? exit.nodeId;
    final stagedPeer = nodes.where((p) => p.stableId == staged).firstOrNull;
    final canConfirm = !_saving &&
        !app.removing &&
        stagedPeer != null &&
        AppController.selectable(stagedPeer) &&
        stagedPeer.stableId != exit.nodeId;
    final awaitingApproval = snapshot != null && !snapshot.machineAuthorized;

    Future<void> confirm() async {
      if (!canConfirm) return;
      setState(() {
        _saving = true;
        _saveFailed = false;
      });
      final navigator = Navigator.of(context);
      final saved = await app.confirmNode(stagedPeer);
      if (!mounted) return;
      if (saved) {
        navigator.pop();
      } else {
        setState(() {
          _saving = false;
          _saveFailed = true;
        });
      }
    }

    Future<void> remove() async {
      final ok = await confirmDialog(
        context,
        title: s.removeExitTitle,
        message: s.removeExitBody,
        confirm: s.remove,
        cancel: s.cancel,
        destructive: true,
      );
      if (!ok || !context.mounted) return;
      final navigator = Navigator.of(context);
      final removed = await app.removeExit();
      if (removed && mounted && navigator.canPop()) navigator.pop();
    }

    final identity = [
      if (snapshot != null && snapshot.userLoginName.isNotEmpty)
        s.signedInAs(snapshot.userLoginName),
      if (snapshot != null && snapshot.selfName.isNotEmpty) s.thisDevice(snapshot.selfName),
    ];

    final list = <Widget>[
      const ProblemCard(only: {AppProblem.disconnect}),
      if (_saveFailed)
        Padding(
          padding: const EdgeInsets.fromLTRB(36, 0, 36, 12),
          child: Text(
            '${s.errorStorage}. ${s.errorStorageBody}',
            style: TextStyle(fontSize: 14, height: 1.3, color: TsColors.of(context, TsColors.error)),
          ),
        ),
      NativeSection(
        header: exit.networkName.isEmpty ? null : exit.networkName,
        footer: Column(
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            Text(app.reconnectRequiresConsent
                ? s.pickerFooterProtected
                : app.switchOn ? s.pickerFooterActive : s.pickerFooterIdle),
            if (snapshot != null)
              Text(app.refreshing ? s.refreshing : s.updated(relativeTime(s, snapshot.fetchedAt))),
          ],
        ),
        children: [
          if (nodes.isEmpty)
            Padding(
              padding: const EdgeInsets.fromLTRB(20, 16, 20, 16),
              child: Column(
                crossAxisAlignment: CrossAxisAlignment.start,
                children: [
                  Text(
                    awaitingApproval ? s.awaitingApproval : s.noExitNodes,
                    style: TextStyle(fontSize: 17, fontWeight: FontWeight.w600, color: labelColor(context)),
                  ),
                  const SizedBox(height: 4),
                  Text(
                    awaitingApproval ? s.noExitNodesApproval : s.noExitNodesBody,
                    style: TextStyle(fontSize: 15, height: 1.35, color: secondaryLabelColor(context)),
                  ),
                ],
              ),
            )
          else
            for (final peer in nodes)
              _NodeRow(
                peer: peer,
                selected: peer.stableId == staged,
                enabled: AppController.selectable(peer) && !_saving && !app.removing,
                onTap: () => setState(() {
                  _staged = peer.stableId;
                  _saveFailed = false;
                }),
              ),
        ],
      ),
      if (identity.isNotEmpty)
        Padding(
          padding: const EdgeInsets.fromLTRB(36, 0, 36, 16),
          child: Text(
            identity.join('\n'),
            style: TextStyle(fontSize: 13, height: 1.4, color: secondaryLabelColor(context)),
          ),
        ),
      NativeSection(
        children: [
          NativeRow(
            leading: NativeRowIcon(
              ios: CupertinoIcons.trash,
              android: Icons.delete_outline,
              color: isCupertino
                  ? CupertinoColors.systemRed.resolveFrom(context)
                  : Theme.of(context).colorScheme.error,
            ),
            title: Text(app.removing ? s.removing : s.removeExit),
            destructive: true,
            enabled: !_saving && !app.removing && app.signIn == SignInPhase.idle,
            onTap: remove,
          ),
        ],
      ),
      SizedBox(height: MediaQuery.paddingOf(context).bottom + 24),
    ];

    if (isCupertino) {
      return CupertinoPageScaffold(
        backgroundColor: CupertinoColors.systemGroupedBackground.resolveFrom(context),
        navigationBar: CupertinoNavigationBar(
          automaticBackgroundVisibility: false,
          backgroundColor: CupertinoColors.systemGroupedBackground.resolveFrom(context),
          border: null,
          leading: CupertinoButton(
            padding: EdgeInsets.zero,
            onPressed: _saving || app.removing ? null : () => Navigator.of(context).pop(),
            child: Text(s.cancel),
          ),
          middle: Text(s.chooseExitNode),
          trailing: CupertinoButton(
            padding: EdgeInsets.zero,
            onPressed: canConfirm ? confirm : null,
            child: _saving
                ? const CupertinoActivityIndicator()
                : Text(s.confirm, style: const TextStyle(fontWeight: FontWeight.w600)),
          ),
        ),
        child: PopScope(
          canPop: !_saving && !app.removing,
          child: SafeArea(
            bottom: false,
            child: ListView(
              controller: widget.scrollController,
              padding: const EdgeInsets.only(top: 12),
              children: list,
            ),
          ),
        ),
      );
    }
    final scheme = Theme.of(context).colorScheme;
    return PopScope(
      canPop: !_saving && !app.removing,
      child: Column(
        children: [
          Padding(
            padding: const EdgeInsets.fromLTRB(24, 0, 12, 8),
            child: Row(
              children: [
                Expanded(child: Text(s.chooseExitNode, style: Theme.of(context).textTheme.titleLarge)),
                TextButton(onPressed: _saving ? null : () => Navigator.of(context).pop(), child: Text(s.cancel)),
                const SizedBox(width: 4),
                FilledButton(
                  onPressed: canConfirm ? confirm : null,
                  child: _saving
                      ? SizedBox.square(dimension: 18, child: CircularProgressIndicator(strokeWidth: 2, color: scheme.onPrimary))
                      : Text(s.confirm),
                ),
              ],
            ),
          ),
          Expanded(child: ListView(children: list)),
        ],
      ),
    );
  }
}

/// One node: a reserved check slot (plain green check when chosen), the name,
/// and Tailscale's reachability at the trailing edge. No radio, IP or icon.
class _NodeRow extends StatelessWidget {
  const _NodeRow({
    required this.peer,
    required this.selected,
    required this.enabled,
    required this.onTap,
  });

  final TailnetPeer peer;
  final bool selected;
  final bool enabled;
  final VoidCallback onTap;

  @override
  Widget build(BuildContext context) {
    final s = S.of(context);
    final good = TsColors.of(context, TsColors.good);
    final status = peer.online
        ? s.online
        : peer.lastSeen != null
            ? s.lastSeen(relativeTime(s, peer.lastSeen!))
            : s.offline;
    final statusColor = peer.online
        ? TsColors.of(context, TsColors.softGood)
        : secondaryLabelColor(context);
    final check = NativeRowIcon(
      ios: selected ? CupertinoIcons.checkmark_alt : CupertinoIcons.desktopcomputer,
      android: selected ? Icons.check : Icons.dns_outlined,
      color: selected ? good : null,
    );
    return Semantics(
      selected: selected,
      inMutuallyExclusiveGroup: true,
      child: NativeRow(
        leading: check,
        title: Text(
          peer.name,
          maxLines: 1,
          overflow: TextOverflow.ellipsis,
          style: TextStyle(fontWeight: selected ? FontWeight.w600 : FontWeight.w400),
        ),
        trailing: Text(
          status,
          style: TextStyle(
            fontSize: 15,
            color: statusColor,
            fontFeatures: const [FontFeature.tabularFigures()],
          ),
        ),
        enabled: enabled,
        onTap: onTap,
      ),
    );
  }
}
