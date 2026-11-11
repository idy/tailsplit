import 'package:adaptive_platform_ui/adaptive_platform_ui.dart';
import 'package:flutter/material.dart';
import 'package:flutter/services.dart';

import '../app.dart';
import '../l10n/strings.dart';
import '../state/app_controller.dart';
import '../theme/tokens.dart';
import 'common.dart';
import 'native.dart';

enum _Filter { all, info, warn, error }

/// Diagnostic output in the desktop's fixed deep-gray terminal, newest last.
/// Like the desktop, the view follows new lines while scrolled to the end and
/// holds still while the reader has scrolled up.
class LogsPage extends StatefulWidget {
  const LogsPage({super.key});

  @override
  State<LogsPage> createState() => _LogsPageState();
}

class _LogsPageState extends State<LogsPage> {
  var _filter = _Filter.all;
  final _scroll = ScrollController();
  bool _following = true;
  bool _active = false;
  LogBuffer? _buffer;

  static const _followSlack = 48.0;

  @override
  void initState() {
    super.initState();
    _scroll.addListener(() {
      if (!_scroll.hasClients) return;
      final position = _scroll.position;
      _following = position.maxScrollExtent - position.pixels <= _followSlack;
    });
  }

  @override
  void didChangeDependencies() {
    super.didChangeDependencies();
    final wasActive = _active;
    _active = TickerMode.valuesOf(context).enabled;
    final buffer = AppScope.read(context).logBuffer;
    final bufferChanged = !identical(buffer, _buffer);
    if (bufferChanged) {
      _buffer?.removeListener(_onLines);
      _buffer = buffer..addListener(_onLines);
    }
    if (_active && _following && (bufferChanged || !wasActive)) {
      _scrollToEnd();
    }
  }

  @override
  void dispose() {
    _buffer?.removeListener(_onLines);
    _scroll.dispose();
    super.dispose();
  }

  void _onLines() {
    if (!mounted || !_active) return;
    setState(() {});
    if (_following) _scrollToEnd();
  }

  void _scrollToEnd() {
    WidgetsBinding.instance.addPostFrameCallback((_) {
      if (!mounted || !_active || !_scroll.hasClients) return;
      _scroll.jumpTo(_scroll.position.maxScrollExtent);
    });
  }

  static bool _isError(String level) => level == 'error' || level == 'fatal' || level == 'panic';

  bool _include(LogLine line) => switch (_filter) {
        _Filter.all => true,
        _Filter.info => line.level != 'trace' && line.level != 'debug',
        _Filter.warn => line.level == 'warn' || line.level == 'warning' || _isError(line.level),
        _Filter.error => _isError(line.level),
      };

  String _filterLabel(_Filter filter) => switch (filter) {
        _Filter.all => 'all',
        _Filter.info => 'info+',
        _Filter.warn => 'warn+',
        _Filter.error => 'error',
      };

  @override
  Widget build(BuildContext context) {
    final app = AppScope.of(context);
    final s = S.of(context);
    final lines = app.logs.where(_include).toList();
    final mono = TextStyle(
      fontFamily: isCupertino ? 'Menlo' : 'monospace',
      fontSize: 12,
      height: 1.6,
      color: TsColors.logInk,
    );

    return NativePage(
      title: s.tabLogs,
      scrollController: _scroll,
      actions: [
        PageAction(
          label: s.logFilter,
          iosSymbol: 'line.3.horizontal.decrease',
          icon: Icons.filter_list,
          menu: [
            for (final filter in _Filter.values)
              AdaptivePopupMenuItem<_Filter>(
                label: _filterLabel(filter),
                value: filter,
                icon: filter == _filter ? 'checkmark' : null,
              ),
          ],
          onMenuSelected: (index) => setState(() {
            _filter = _Filter.values[index];
            _following = true;
            _scrollToEnd();
          }),
        ),
        PageAction(
          label: s.copy,
          iosSymbol: 'doc.on.doc',
          icon: Icons.copy_all_outlined,
          onPressed: lines.isEmpty
              ? null
              : () async {
                  await Clipboard.setData(ClipboardData(text: lines.map(_plain).join('\n')));
                  if (context.mounted) showNotice(context, s.copiedLines(lines.length));
                },
        ),
        PageAction(
          label: s.clear,
          iosSymbol: 'trash',
          icon: Icons.delete_outline,
          onPressed: app.logs.isEmpty ? null : app.clearLogs,
        ),
      ],
      slivers: [
        SliverPadding(
          padding: const EdgeInsets.symmetric(horizontal: 16),
          sliver: DecoratedSliver(
            decoration: BoxDecoration(
              color: TsColors.logBackground,
              borderRadius: BorderRadius.circular(TsSpace.panelRadius),
            ),
            sliver: SliverPadding(
              padding: const EdgeInsets.fromLTRB(14, 12, 14, 14),
              sliver: lines.isEmpty
                  ? SliverToBoxAdapter(child: _EmptyLogs(s: s, mono: mono))
                  : SliverList.builder(
                      itemCount: lines.length,
                      itemBuilder: (context, i) => _LogRow(line: lines[i], mono: mono),
                    ),
            ),
          ),
        ),
      ],
    );
  }

  static String _two(int n) => n.toString().padLeft(2, '0');
  static String _stamp(DateTime t) => '${_two(t.hour)}:${_two(t.minute)}:${_two(t.second)}';
  static String _plain(LogLine line) =>
      '${_stamp(line.time)} ${line.level.toUpperCase()} [${line.source}] ${line.message}';
}

class _LogRow extends StatelessWidget {
  const _LogRow({required this.line, required this.mono});
  final LogLine line;
  final TextStyle mono;

  @override
  Widget build(BuildContext context) {
    final levelColor = switch (line.level) {
      'error' || 'fatal' || 'panic' => TsColors.logError,
      'warn' || 'warning' => TsColors.logWarn,
      'debug' || 'trace' => TsColors.logMuted,
      _ => TsColors.logGood,
    };
    final sourceColor = line.source == 'core' ? TsColors.logCyan : TsColors.logMagenta;
    return SelectableText.rich(
      TextSpan(
        style: mono,
        children: [
          TextSpan(text: '${_LogsPageState._stamp(line.time)} ', style: const TextStyle(color: TsColors.logMuted)),
          TextSpan(text: line.level.toUpperCase().padRight(5), style: TextStyle(color: levelColor, fontWeight: FontWeight.w600)),
          TextSpan(text: ' ${line.source} ', style: TextStyle(color: sourceColor)),
          TextSpan(text: line.message),
        ],
      ),
    );
  }
}

class _EmptyLogs extends StatelessWidget {
  const _EmptyLogs({required this.s, required this.mono});
  final S s;
  final TextStyle mono;

  @override
  Widget build(BuildContext context) => Padding(
        padding: const EdgeInsets.symmetric(vertical: 20),
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            Text(s.logsEmpty, style: mono.copyWith(fontWeight: FontWeight.w600)),
            const SizedBox(height: 6),
            Text(s.logsEmptyBody, style: mono.copyWith(color: TsColors.logMuted)),
          ],
        ),
      );
}
