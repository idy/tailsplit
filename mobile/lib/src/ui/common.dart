import 'dart:async';

import 'package:flutter/cupertino.dart';
import 'package:flutter/material.dart';
import 'package:flutter/semantics.dart';

import '../app.dart';
import '../l10n/strings.dart';
import '../services/errors.dart';
import '../state/app_controller.dart';
import '../theme/tokens.dart';
import 'native.dart';
import 'node_picker.dart';

/// "just now", "5 min ago" … then a calendar date after a month. Clock skew
/// that puts a time in the future reads as "just now".
String relativeTime(S s, DateTime time) {
  final age = DateTime.now().difference(time);
  if (age.inMinutes < 1) return s.justNow;
  if (age.inHours < 1) return s.minutesAgo(age.inMinutes);
  if (age.inDays < 1) return s.hoursAgo(age.inHours);
  if (age.inDays < 31) return s.daysAgo(age.inDays);
  return s.date(time);
}

/// The localized explanation of a failure cause, or null when only the raw
/// detail can explain it.
String? causeText(S s, FailureCause? cause) => switch (cause) {
      FailureCause.offline => s.causeOffline,
      FailureCause.timeout => s.causeTimeout,
      FailureCause.tls => s.causeTls,
      FailureCause.rateLimited => s.causeRateLimited,
      FailureCause.server => s.causeServer,
      FailureCause.rejected => s.causeRejected,
      FailureCause.invalid => s.causeInvalidRules,
      FailureCause.noBrowser => s.errorOpenBrowser,
      FailureCause.storage => s.errorStorageBody,
      FailureCause.cancelled || FailureCause.unknown || null => null,
    };

/// The current problem, explained in place with its recovery action.
/// [only] limits the card to problems that belong on the hosting page.
class ProblemCard extends StatelessWidget {
  const ProblemCard({super.key, this.only});

  final Set<AppProblem>? only;

  @override
  Widget build(BuildContext context) {
    final app = AppScope.of(context);
    final problem = app.problem;
    if (problem == null || (only != null && !only!.contains(problem))) {
      return const SizedBox.shrink();
    }
    final s = S.of(context);
    final cause = causeText(s, app.problemCause);
    final detail = cause ?? app.problemDetail;
    final subject = app.problemSubject ?? app.exit?.nodeName ?? '';
    final signingIn = app.signIn != SignInPhase.idle;
    final (String title, String? body, String action, VoidCallback? onAction) = switch (problem) {
      AppProblem.signIn when signingIn =>
        (s.errorSignIn, detail, s.openSignInAgain, app.openSignInPage),
      AppProblem.signIn => (s.errorSignIn, detail, s.retry, app.signInExit),
      AppProblem.refresh => (s.errorRefresh, detail, s.retry, app.refreshing ? null : app.refresh),
      AppProblem.rules => (
          s.errorRules,
          [?detail, if (app.rulesUpdatedAt == null) s.rulesFirstDownload].join(' '),
          s.retry,
          app.canConnect ? app.connect : null,
        ),
      AppProblem.permission => (s.connectionFailed, s.vpnPermissionDenied, s.retry, app.canConnect ? app.connect : null),
      AppProblem.connect => (s.connectionFailed, detail, s.retry, app.canConnect && !app.switchOn ? app.connect : null),
      AppProblem.needsSignIn => (s.signedOut, s.needsSignInAgain, s.signIn, app.busy ? null : app.signInExit),
      AppProblem.nodeOffline => (
          s.errorNodeOffline(subject),
          s.errorNodeOfflineBody,
          s.chooseNode,
          () => showNodePicker(context),
        ),
      AppProblem.nodeMissing => (
          s.errorNodeMissing,
          s.errorNodeMissingBody(subject),
          s.chooseNode,
          () => showNodePicker(context),
        ),
      AppProblem.awaitingApproval => (
          s.awaitingApproval,
          s.awaitingApprovalBody,
          s.refresh,
          app.refreshing ? null : app.refresh,
        ),
      AppProblem.storage => (s.errorStorage, s.errorStorageBody, s.retry, app.retrySave),
    };
    final warning = problem == AppProblem.awaitingApproval;
    final tone = TsColors.of(context, warning ? TsColors.warn : TsColors.error);
    final secondary = secondaryLabelColor(context);
    return Semantics(
      liveRegion: true,
      container: true,
      child: NativeSection(
        children: [
          Padding(
            padding: const EdgeInsets.fromLTRB(16, 12, 4, 8),
            child: Column(
              crossAxisAlignment: CrossAxisAlignment.start,
              children: [
                Row(
                  crossAxisAlignment: CrossAxisAlignment.start,
                  children: [
                    Padding(
                      padding: const EdgeInsets.only(top: 1),
                      child: Icon(
                        warning
                            ? (isCupertino ? CupertinoIcons.clock_fill : Icons.schedule)
                            : (isCupertino ? CupertinoIcons.exclamationmark_circle_fill : Icons.error_outline),
                        size: 20,
                        color: tone,
                      ),
                    ),
                    const SizedBox(width: 10),
                    Expanded(
                      child: Padding(
                        padding: const EdgeInsets.only(top: 1),
                        child: Column(
                          crossAxisAlignment: CrossAxisAlignment.start,
                          children: [
                            Text(
                              title,
                              style: TextStyle(fontSize: 15, fontWeight: FontWeight.w600, color: tone),
                            ),
                            if (body != null && body.isNotEmpty) ...[
                              const SizedBox(height: 2),
                              Text(body, style: TextStyle(fontSize: 14, height: 1.3, color: secondary)),
                            ],
                          ],
                        ),
                      ),
                    ),
                    _DismissButton(label: s.dismiss, onPressed: app.dismissProblem),
                  ],
                ),
                // The action sits under the text so long translations wrap
                // instead of squeezing it.
                Padding(
                  padding: const EdgeInsets.only(left: 18),
                  child: TextAction(label: action, onPressed: onAction),
                ),
              ],
            ),
          ),
        ],
      ),
    );
  }
}

class _DismissButton extends StatelessWidget {
  const _DismissButton({required this.label, required this.onPressed});
  final String label;
  final VoidCallback onPressed;

  @override
  Widget build(BuildContext context) => Semantics(
        button: true,
        label: label,
        excludeSemantics: true,
        child: GestureDetector(
          onTap: onPressed,
          behavior: HitTestBehavior.opaque,
          child: SizedBox.square(
            dimension: 44,
            child: Icon(
              isCupertino ? CupertinoIcons.xmark : Icons.close,
              size: 16,
              color: secondaryLabelColor(context),
            ),
          ),
        ),
      );
}

/// Settings saved while connected wait for an explicit reconnect, like the
/// desktop's Save & apply actionbar.
class ReconnectBar extends StatelessWidget {
  const ReconnectBar({super.key});

  @override
  Widget build(BuildContext context) {
    final app = AppScope.of(context);
    final s = S.of(context);
    return Semantics(
      liveRegion: true,
      container: true,
      child: GlassBar(
        child: Row(
          children: [
            Icon(
              isCupertino ? CupertinoIcons.arrow_2_circlepath : Icons.sync,
              size: 18,
              color: isCupertino ? TsColors.of(context, TsColors.warn) : null,
            ),
            const SizedBox(width: 10),
            Expanded(
              child: Text(
                s.reconnectToApply,
                style: TextStyle(
                  fontSize: 15,
                  color: isCupertino ? labelColor(context) : null,
                ),
              ),
            ),
            TextAction(
              label: s.reconnect,
              onPressed: app.transitioning || app.busy ? null : app.reconnect,
            ),
          ],
        ),
      ),
    );
  }
}

/// A brief confirmation (Copied, Removed · Undo). Material uses a snackbar;
/// iOS shows a small capsule above the tab bar. Screen readers hear it too.
void showNotice(
  BuildContext context,
  String message, {
  String? action,
  VoidCallback? onAction,
}) {
  SemanticsService.sendAnnouncement(
    View.of(context),
    message,
    Directionality.of(context),
  );
  if (!isCupertino) {
    final messenger = ScaffoldMessenger.maybeOf(context);
    messenger?.hideCurrentSnackBar();
    messenger?.showSnackBar(SnackBar(
      content: Text(message),
      action: action == null || onAction == null ? null : SnackBarAction(label: action, onPressed: onAction),
    ));
    return;
  }
  _Notice.show(context, message, action: action, onAction: onAction);
}

class _Notice extends StatefulWidget {
  const _Notice({required this.message, this.action, this.onAction, required this.onDone});

  final String message;
  final String? action;
  final VoidCallback? onAction;
  final VoidCallback onDone;

  static OverlayEntry? _current;

  static void show(BuildContext context, String message, {String? action, VoidCallback? onAction}) {
    final overlay = Overlay.maybeOf(context, rootOverlay: true);
    if (overlay == null) return;
    _current?.remove();
    late final OverlayEntry entry;
    entry = OverlayEntry(
      builder: (_) => _Notice(
        message: message,
        action: action,
        onAction: onAction,
        onDone: () {
          if (identical(_current, entry)) _current = null;
          if (entry.mounted) entry.remove();
        },
      ),
    );
    _current = entry;
    overlay.insert(entry);
  }

  @override
  State<_Notice> createState() => _NoticeState();
}

class _NoticeState extends State<_Notice> {
  bool _visible = false;
  Timer? _timer;

  @override
  void initState() {
    super.initState();
    WidgetsBinding.instance.addPostFrameCallback((_) {
      if (mounted) setState(() => _visible = true);
    });
    _timer = Timer(Duration(seconds: widget.action == null ? 2 : 4), _hide);
  }

  void _hide() {
    if (!mounted) return;
    setState(() => _visible = false);
    final instant = MediaQuery.disableAnimationsOf(context);
    Timer(instant ? Duration.zero : const Duration(milliseconds: 200), widget.onDone);
  }

  @override
  void dispose() {
    _timer?.cancel();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    final instant = MediaQuery.disableAnimationsOf(context);
    final background = CupertinoColors.secondarySystemGroupedBackground.resolveFrom(context);
    return Positioned(
      left: 24,
      right: 24,
      bottom: MediaQuery.paddingOf(context).bottom + 76,
      child: IgnorePointer(
        ignoring: !_visible,
        child: AnimatedOpacity(
          opacity: _visible ? 1 : 0,
          duration: instant ? Duration.zero : const Duration(milliseconds: 180),
          curve: Curves.easeOutCubic,
          child: Center(
            child: Container(
              constraints: const BoxConstraints(minHeight: 44),
              padding: EdgeInsets.fromLTRB(18, 6, widget.action == null ? 18 : 6, 6),
              decoration: BoxDecoration(
                color: background,
                borderRadius: BorderRadius.circular(22),
                boxShadow: const [
                  BoxShadow(color: Color(0x29000000), blurRadius: 18, offset: Offset(0, 6)),
                ],
              ),
              child: Row(
                mainAxisSize: MainAxisSize.min,
                children: [
                  Flexible(
                    child: Text(
                      widget.message,
                      maxLines: 2,
                      overflow: TextOverflow.ellipsis,
                      style: TextStyle(
                        fontSize: 15,
                        decoration: TextDecoration.none,
                        color: labelColor(context),
                      ),
                    ),
                  ),
                  if (widget.action != null && widget.onAction != null)
                    TextAction(
                      label: widget.action!,
                      onPressed: () {
                        widget.onAction!();
                        _hide();
                      },
                    ),
                ],
              ),
            ),
          ),
        ),
      ),
    );
  }
}
