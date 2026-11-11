import 'package:adaptive_platform_ui/adaptive_platform_ui.dart';
import 'package:flutter/cupertino.dart';
import 'package:flutter/material.dart';

import '../l10n/strings.dart';
import '../theme/tokens.dart';
import '../app.dart';
import 'editors.dart';
import 'exit_page.dart';
import 'native.dart';
import 'node_picker.dart';
import 'logs_page.dart';
import 'settings_page.dart';

/// Three top-level sections in a native tab bar (Liquid Glass UITabBar on
/// iOS 26+, Material 3 navigation bar on Android). Each tab keeps its own
/// navigation stack so the bar stays visible on detail pages.
class RootShell extends StatefulWidget {
  const RootShell({super.key});

  @override
  State<RootShell> createState() => _RootShellState();
}

class _RootShellState extends State<RootShell> {
  int _index = 0;
  final _navigators = List.generate(3, (_) => GlobalKey<NavigatorState>());
  bool _previewRouted = false;

  void _applyPreviewScreen(String screen) {
    if (_previewRouted || screen.isEmpty) return;
    _previewRouted = true;
    final tab = switch (screen) {
      'logs' => 1,
      'settings' || 'global' || 'reconnect' || 'list' || 'rules' => 2,
      _ => 0,
    };
    WidgetsBinding.instance.addPostFrameCallback((_) {
      if (!mounted) return;
      setState(() => _index = tab);
      if (screen == 'list') {
        WidgetsBinding.instance.addPostFrameCallback((_) {
          final navigator = _navigators[2].currentContext;
          if (navigator != null) {
            pushPage<void>(
              navigator,
              (_) => AppScope(
                controller: AppScope.read(context),
                child: const ListEditorPage(field: SettingsList.proxyDomains),
              ),
            );
          }
        });
      }
      if (screen == 'picker') {
        WidgetsBinding.instance.addPostFrameCallback((_) {
          final page = _navigators[0].currentContext;
          if (page != null) showNodePicker(page);
        });
      }
    });
  }

  static const _pages = <Widget>[ExitPage(), LogsPage(), SettingsPage()];

  void _select(int index) {
    if (index == _index) {
      _navigators[index].currentState?.popUntil((route) => route.isFirst);
      return;
    }
    setState(() => _index = index);
  }

  @override
  Widget build(BuildContext context) {
    final app = AppScope.of(context);
    if (app.isPreview) _applyPreviewScreen(app.previewScreen);
    final s = S.of(context);
    dynamic icon(String symbol, IconData cupertino, IconData material) =>
        PlatformInfo.isIOS26OrHigher() ? symbol : (isCupertino ? cupertino : material);
    return PopScope(
      canPop: false,
      onPopInvokedWithResult: (didPop, _) {
        final navigator = _navigators[_index].currentState;
        if (navigator != null && navigator.canPop()) {
          navigator.pop();
        } else if (_index != 0) {
          setState(() => _index = 0);
        }
      },
      child: AdaptiveScaffold(
        bottomNavigationBar: AdaptiveBottomNavigationBar(
          useNativeBottomBar: true,
          selectedIndex: _index,
          onTap: _select,
          selectedItemColor: isCupertino ? TsColors.of(context, TsColors.tint) : null,
          items: [
            AdaptiveNavigationDestination(
              icon: icon('point.3.connected.trianglepath.dotted', CupertinoIcons.globe, Icons.hub_outlined),
              selectedIcon: isCupertino ? null : Icons.hub,
              label: s.tabExit,
            ),
            AdaptiveNavigationDestination(
              icon: icon('text.alignleft', CupertinoIcons.text_alignleft, Icons.receipt_long_outlined),
              selectedIcon: isCupertino ? null : Icons.receipt_long,
              label: s.tabLogs,
            ),
            AdaptiveNavigationDestination(
              icon: icon('gearshape', CupertinoIcons.gear, Icons.settings_outlined),
              selectedIcon: isCupertino ? null : Icons.settings,
              label: s.tabSettings,
            ),
          ],
        ),
        body: IndexedStack(
          index: _index,
          children: [
            for (final (i, page) in _pages.indexed)
              TickerMode(
                enabled: i == _index,
                child: Navigator(
                  key: _navigators[i],
                  onGenerateRoute: (_) => isCupertino
                      ? CupertinoPageRoute<void>(builder: (_) => page)
                      : MaterialPageRoute<void>(builder: (_) => page),
                ),
              ),
          ],
        ),
      ),
    );
  }
}
