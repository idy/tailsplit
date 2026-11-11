import 'package:flutter/cupertino.dart';
import 'package:flutter/material.dart';

import '../app.dart';
import '../l10n/strings.dart';
import '../state/app_controller.dart';
import '../tailscale/tailscale.dart';
import '../theme/tokens.dart';
import 'common.dart';
import 'native.dart';

enum _PickerAction { signIn }

/// Opens one Tailnet's nodes without changing the global selection on cancel.
Future<void> showNodePicker(BuildContext context, {String? tailnetId}) async {
  final app = AppScope.read(context);
  final id = tailnetId ?? app.selectedTailnetId ?? app.tailnets.firstOrNull?.id;
  if (id == null || app.tailnetById(id) == null) return;
  _PickerAction? action;
  if (isCupertino) {
    if (NativeChrome.sheetPresented.value) return;
    NativeChrome.sheetPresented.value = true;
    try {
      action = await showCupertinoSheet<_PickerAction>(
        context: context,
        scrollableBuilder: (_, scroll) => AppScope(
          controller: app,
          child: NodePicker(tailnetId: id, scrollController: scroll),
        ),
      );
    } finally {
      NativeChrome.sheetPresented.value = false;
    }
  } else {
    action = await showModalBottomSheet<_PickerAction>(
      context: context,
      useRootNavigator: true,
      isScrollControlled: true,
      useSafeArea: true,
      showDragHandle: true,
      builder: (_) => AppScope(
        controller: app,
        child: FractionallySizedBox(heightFactor: .92, child: NodePicker(tailnetId: id)),
      ),
    );
  }
  if (action == _PickerAction.signIn && context.mounted && app.tailnetById(id) != null) {
    await signInWithConsent(context, app, tailnetId: id);
  }
}

class NodePicker extends StatefulWidget {
  const NodePicker({super.key, required this.tailnetId, this.scrollController});

  final String tailnetId;
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
    WidgetsBinding.instance.addPostFrameCallback((_) {
      if (!mounted) return;
      final app = AppScope.read(context);
      final fetched = app.tailnetById(widget.tailnetId)?.snapshot?.fetchedAt;
      final stale = fetched == null || DateTime.now().difference(fetched) > const Duration(seconds: 30);
      if (stale) app.refresh(silent: true, tailnetId: widget.tailnetId);
    });
  }

  @override
  Widget build(BuildContext context) {
    final app = AppScope.of(context);
    final s = S.of(context);
    final account = app.tailnetById(widget.tailnetId);
    if (account == null) return const SizedBox.shrink();
    final snapshot = account.snapshot;
    final nodes = snapshot?.exitNodes ?? const <TailnetPeer>[];
    final selectedHere = app.selectedTailnetId == account.id;
    final selectedId = selectedHere ? app.exit?.nodeId : null;
    final staged = _staged ?? selectedId;
    final stagedPeer = nodes.where((p) => p.stableId == staged).firstOrNull;
    final refreshing = app.isRefreshingTailnet(account.id);
    final canEdit = !_saving && !app.busy;
    final canConfirm = canEdit && stagedPeer != null &&
        AppController.selectable(stagedPeer) && stagedPeer.stableId != selectedId;
    final awaitingApproval = snapshot != null && !snapshot.machineAuthorized;
    final diagnostics = snapshot?.discoveryDiagnostics;
    final unapprovedRoutes = diagnostics != null && diagnostics.advertisedExitRoutePeerCount > 0 &&
        diagnostics.exitRoutePeerCount == 0;
    final signedOut = app.problem == AppProblem.needsSignIn &&
        (app.problemTailnetId ?? app.selectedTailnetId) == account.id;

    Future<void> confirm() async {
      if (!canConfirm) return;
      setState(() {
        _saving = true;
        _saveFailed = false;
      });
      final navigator = Navigator.of(context);
      final saved = await app.confirmNode(stagedPeer, tailnetId: account.id);
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
        title: s.removeTailnetTitle,
        message: s.removeTailnetBody,
        confirm: s.signOut,
        cancel: s.cancel,
        destructive: true,
      );
      if (!ok || !context.mounted) return;
      final navigator = Navigator.of(context);
      final removed = await app.removeTailnet(account.id);
      if (removed && mounted && navigator.canPop()) navigator.pop();
    }

    final identity = [
      if (snapshot != null && snapshot.userLoginName.isNotEmpty) s.signedInAs(snapshot.userLoginName),
      if (snapshot != null && snapshot.selfName.isNotEmpty) s.thisDevice(snapshot.selfName),
    ];
    final list = <Widget>[
      if (app.problem == AppProblem.disconnect || app.problemTailnetId == account.id)
        const ProblemCard(only: {AppProblem.disconnect, AppProblem.refresh}),
      if (_saveFailed)
        Padding(
          padding: const EdgeInsets.fromLTRB(36, 0, 36, 12),
          child: Text(
            s.selectionNotSaved,
            style: TextStyle(fontSize: 14, height: 1.3, color: TsColors.of(context, TsColors.error)),
          ),
        ),
      NativeSection(
        header: account.networkName.isEmpty ? s.tailnet : account.networkName,
        footer: Column(
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            Text(app.reconnectRequiresConsent
                ? s.pickerFooterProtected
                : app.switchOn ? s.pickerFooterActive : s.pickerFooterIdle),
            if (snapshot != null)
              Text(refreshing ? s.refreshing : s.updated(relativeTime(s, snapshot.fetchedAt))),
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
                    signedOut ? s.signedOut : awaitingApproval ? s.awaitingApproval
                        : unapprovedRoutes ? s.exitRoutesUnavailable : s.noExitNodes,
                    style: TextStyle(fontSize: 17, fontWeight: FontWeight.w600, color: labelColor(context)),
                  ),
                  const SizedBox(height: 4),
                  Text(
                    signedOut ? s.needsSignInAgain : awaitingApproval ? s.noExitNodesApproval
                        : unapprovedRoutes ? s.exitRoutesUnavailableBody
                        : diagnostics?.peerCount == 0 ? s.noVisiblePeers : s.noExitNodesBody,
                    style: TextStyle(fontSize: 15, height: 1.35, color: secondaryLabelColor(context)),
                  ),
                  if (diagnostics != null) ...[
                    const SizedBox(height: 8),
                    Text(s.visibleDevices(diagnostics.peerCount),
                        style: TextStyle(fontSize: 13, color: secondaryLabelColor(context))),
                  ],
                ],
              ),
            )
          else
            for (final peer in nodes)
              _NodeRow(
                peer: peer,
                selected: peer.stableId == staged,
                enabled: AppController.selectable(peer) && canEdit,
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
          child: Text(identity.join('\n'),
              style: TextStyle(fontSize: 13, height: 1.4, color: secondaryLabelColor(context))),
        ),
      NativeSection(
        children: [
          NativeRow(
            leading: const NativeRowIcon(ios: CupertinoIcons.refresh, android: Icons.refresh),
            title: Text(refreshing ? s.refreshing : s.refresh),
            trailing: refreshing ? const Spinner() : null,
            enabled: canEdit && !refreshing,
            onTap: () => app.refresh(tailnetId: account.id),
          ),
          NativeRow(
            leading: const NativeRowIcon(ios: CupertinoIcons.person_crop_circle, android: Icons.account_circle_outlined),
            title: Text(s.signInAgain),
            enabled: canEdit,
            onTap: () => Navigator.of(context).pop(_PickerAction.signIn),
          ),
          NativeRow(
            leading: NativeRowIcon(
              ios: CupertinoIcons.square_arrow_right,
              android: Icons.logout,
              color: isCupertino ? CupertinoColors.systemRed.resolveFrom(context) : Theme.of(context).colorScheme.error,
            ),
            title: Text(app.removing ? s.removing : s.removeTailnet),
            destructive: true,
            enabled: canEdit,
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
            child: _saving ? const CupertinoActivityIndicator()
                : Text(s.confirm, style: const TextStyle(fontWeight: FontWeight.w600)),
          ),
        ),
        child: PopScope(
          canPop: !_saving && !app.removing,
          child: SafeArea(
            bottom: false,
            child: ListView(controller: widget.scrollController, padding: const EdgeInsets.only(top: 12), children: list),
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
                TextButton(onPressed: _saving || app.removing ? null : () => Navigator.of(context).pop(), child: Text(s.cancel)),
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

/// A single-line node row with a leading icon/check and reported reachability.
class _NodeRow extends StatelessWidget {
  const _NodeRow({required this.peer, required this.selected, required this.enabled, required this.onTap});

  final TailnetPeer peer;
  final bool selected;
  final bool enabled;
  final VoidCallback onTap;

  @override
  Widget build(BuildContext context) {
    final s = S.of(context);
    final status = peer.online ? s.online : peer.lastSeen != null ? s.lastSeen(relativeTime(s, peer.lastSeen!)) : s.offline;
    return Semantics(
      selected: selected,
      inMutuallyExclusiveGroup: true,
      child: NativeRow(
        leading: NativeRowIcon(
          ios: selected ? CupertinoIcons.checkmark_alt : CupertinoIcons.desktopcomputer,
          android: selected ? Icons.check : Icons.dns_outlined,
          color: selected ? TsColors.of(context, TsColors.good) : null,
        ),
        title: Text(peer.name, maxLines: 1, overflow: TextOverflow.ellipsis,
            style: TextStyle(fontWeight: selected ? FontWeight.w600 : FontWeight.w400)),
        trailing: Text(status, style: TextStyle(
          fontSize: 15,
          color: peer.online ? TsColors.of(context, TsColors.softGood) : secondaryLabelColor(context),
          fontFeatures: const [FontFeature.tabularFigures()],
        )),
        enabled: enabled,
        onTap: onTap,
      ),
    );
  }
}
