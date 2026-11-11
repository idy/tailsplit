import 'package:flutter/cupertino.dart' show CupertinoIcons;
import 'package:adaptive_platform_ui/adaptive_platform_ui.dart';
import 'package:flutter/material.dart';
import 'package:url_launcher/url_launcher.dart';

import '../app.dart';
import '../l10n/strings.dart';
import '../model/settings.dart';
import '../services/config_builder.dart' show ResolverAddress;
import '../state/app_controller.dart';
import '../theme/tokens.dart';
import 'common.dart';
import 'editors.dart';
import 'exit_page.dart' show ChoiceMenu;
import 'native.dart';

/// Settings save immediately. While connected, connection settings wait for
/// Reconnect; the routing mode reconnects at once, like the desktop shortcut.
class SettingsPage extends StatelessWidget {
  const SettingsPage({super.key});

  @override
  Widget build(BuildContext context) {
    final app = AppScope.of(context);
    final s = S.of(context);
    final settings = app.settings;
    final split = settings.mode == RoutingMode.split;
    final hasRuleList = settings.source != RuleSource.none &&
        (settings.source == RuleSource.builtin || settings.customSourceUrl.isNotEmpty);
    void save(MobileSettings next) => app.updateSettings(next);

    return NativePage(
      title: s.tabSettings,
      bottom: app.needsReconnect ? const ReconnectBar() : null,
      slivers: [
        const SliverToBoxAdapter(
          child: ProblemCard(only: {AppProblem.rules, AppProblem.storage, AppProblem.globalUnavailable}),
        ),
        SliverToBoxAdapter(
          child: Padding(
            padding: EdgeInsets.fromLTRB(isCupertino ? 20 : 16, 20, isCupertino ? 20 : 16, 12),
            child: Column(
              crossAxisAlignment: CrossAxisAlignment.start,
              children: [
                Padding(
                  padding: EdgeInsetsDirectional.only(start: isCupertino ? 0 : 20),
                  child: Text(
                    s.routing,
                    style: isCupertino
                        ? TextStyle(fontSize: 13, color: secondaryLabelColor(context))
                        : Theme.of(context).textTheme.labelLarge?.copyWith(
                            color: Theme.of(context).colorScheme.primary,
                          ),
                  ),
                ),
                const SizedBox(height: 8),
                IgnorePointer(
                  ignoring: !app.globalAvailable,
                  child: AdaptiveSegmentedControl(
                    labels: [s.ruleBased, s.globalShort],
                    selectedIndex: split ? 0 : 1,
                    enabled: app.globalAvailable && !app.transitioning && !app.busy,
                    onValueChanged: (index) {
                      final mode = index == 0 ? RoutingMode.split : RoutingMode.global;
                      if (mode != settings.mode) app.setRoutingMode(mode);
                    },
                  ),
                ),
                if (!app.globalAvailable) ...[
                  const SizedBox(height: 8),
                  Text(s.globalUnavailableBody,
                      style: TextStyle(fontSize: 13, color: secondaryLabelColor(context))),
                ],
              ],
            ),
          ),
        ),
        if (split)
          SliverToBoxAdapter(
            child: NativeSection(
              footer: hasRuleList && app.rulesUpdateError != null && !app.rulesUpdating
                  ? _RuleListError(app: app)
                  : null,
              children: [
                NativeRow(
                  leading: const NativeRowIcon(ios: CupertinoIcons.doc_text, android: Icons.description_outlined),
                  title: Text(s.ruleSource),
                  trailing: ChoiceMenu<RuleSource>(
                    label: _sourceLabel(s, settings.source),
                    options: [
                      for (final source in RuleSource.values) (source, _sourceLabel(s, source)),
                    ],
                    selected: settings.source,
                    onSelected: (source) async {
                      if (source == settings.source) return;
                      if (source == RuleSource.custom && settings.customSourceUrl.isEmpty) {
                        final url = await promptText(
                          context,
                          title: s.customRulesUrl,
                          initial: '',
                          hint: 'https://',
                          keyboard: TextInputType.url,
                          validate: (v) => _validUrl(v) ? null : s.invalidUrl,
                        );
                        if (url == null) return;
                        save(settings.copyWith(source: source, customSourceUrl: url));
                        return;
                      }
                      save(settings.copyWith(source: source));
                    },
                  ),
                ),
                if (settings.source == RuleSource.custom)
                  NativeRow(
                    leading: const NativeRowIcon(ios: CupertinoIcons.link, android: Icons.link),
                    title: Text(s.customRulesUrl),
                    value: settings.customSourceUrl,
                    chevron: true,
                    onTap: () async {
                      final url = await promptText(
                        context,
                        title: s.customRulesUrl,
                        initial: settings.customSourceUrl,
                        hint: 'https://',
                        keyboard: TextInputType.url,
                        validate: (v) => _validUrl(v) ? null : s.invalidUrl,
                      );
                      if (url != null) save(settings.copyWith(customSourceUrl: url));
                    },
                  ),
                NativeRow(
                  leading: const NativeRowIcon(ios: CupertinoIcons.arrow_turn_down_right, android: Icons.turn_right),
                  title: Text(s.unmatchedTraffic),
                  trailing: ChoiceMenu<UnmatchedRoute>(
                    label: settings.unmatched == UnmatchedRoute.direct ? s.direct : s.throughExit,
                    options: [
                      (UnmatchedRoute.direct, s.direct),
                      (UnmatchedRoute.exit, s.throughExit),
                    ],
                    selected: settings.unmatched,
                    onSelected: (route) => save(settings.copyWith(unmatched: route)),
                  ),
                ),
                if (settings.source != RuleSource.none &&
                    (settings.source == RuleSource.builtin || settings.customSourceUrl.isNotEmpty))
                  _RuleListRow(app: app),
                for (final field in SettingsList.values) _listRow(context, app, field),
              ],
            ),
          )
        else if (app.globalAvailable)
          SliverToBoxAdapter(
            child: Padding(
              padding: const EdgeInsets.fromLTRB(20, 0, 20, 12),
              child: Text(
                s.globalHidesSplit,
                style: TextStyle(fontSize: 13, color: secondaryLabelColor(context)),
              ),
            ),
          ),
        SliverToBoxAdapter(
          child: NativeSection(
            header: s.dns,
            footer: Text(s.fakeIpNote),
            children: [
              NativeRow(
                leading: const NativeRowIcon(ios: CupertinoIcons.antenna_radiowaves_left_right, android: Icons.dns_outlined),
                title: Text(s.proxyDns),
                value: settings.proxyDns,
                chevron: true,
                onTap: () async {
                  final value = await promptText(
                    context,
                    title: s.proxyDns,
                    initial: settings.proxyDns,
                    hint: '8.8.8.8',
                    keyboard: TextInputType.url,
                    validate: (v) {
                      final address = ResolverAddress.parse(v);
                      return address == null || address.isSystem ? s.invalidProxyDns : null;
                    },
                  );
                  if (value != null) {
                    save(settings.copyWith(proxyDns: ResolverAddress.parse(value)!.toString()));
                  }
                },
              ),
              NativeRow(
                leading: const NativeRowIcon(ios: CupertinoIcons.wifi, android: Icons.wifi),
                title: Text(s.directDns),
                value: settings.directDns.isEmpty ? s.system : settings.directDns,
                chevron: true,
                onTap: () async {
                  final value = await promptText(
                    context,
                    title: s.directDns,
                    initial: settings.directDns,
                    hint: s.system,
                    keyboard: TextInputType.url,
                    allowEmpty: true,
                    validate: (v) => ResolverAddress.parse(v) == null ? s.invalidDirectDns : null,
                  );
                  if (value != null) {
                    final address = ResolverAddress.parse(value)!;
                    save(settings.copyWith(directDns: address.isSystem ? '' : address.toString()));
                  }
                },
              ),
              NativeRow(
                leading: const NativeRowIcon(ios: CupertinoIcons.shuffle, android: Icons.shuffle),
                title: Text(s.fakeIp),
                trailing: NativeSwitch(
                  label: s.fakeIp,
                  value: settings.fakeIp,
                  onChanged: (v) => save(settings.copyWith(fakeIp: v)),
                ),
              ),
            ],
          ),
        ),
        SliverToBoxAdapter(
          child: NativeSection(
            header: s.network,
            footer: Text(s.directIpv6Note),
            children: [
              NativeRow(
                leading: const NativeRowIcon(ios: CupertinoIcons.globe, android: Icons.language),
                title: Text(s.directIpv6),
                trailing: NativeSwitch(
                  label: s.directIpv6,
                  value: settings.directIpv6,
                  onChanged: (v) => save(settings.copyWith(directIpv6: v)),
                ),
              ),
              NativeRow(
                leading: const NativeRowIcon(ios: CupertinoIcons.arrow_left_right, android: Icons.swap_horiz),
                title: Text(s.mtu),
                value: '${settings.mtu}',
                chevron: true,
                onTap: () async {
                  final value = await promptText(
                    context,
                    title: s.mtu,
                    initial: '${settings.mtu}',
                    hint: '1400',
                    keyboard: TextInputType.number,
                    validate: (v) {
                      final n = int.tryParse(v);
                      return n != null && n >= MobileSettings.minMtu && n <= MobileSettings.maxMtu
                          ? null
                          : s.invalidMtu;
                    },
                  );
                  if (value != null) save(settings.copyWith(mtu: int.parse(value)));
                },
              ),
            ],
          ),
        ),
        SliverToBoxAdapter(
          child: NativeSection(
            header: s.general,
            children: [
              NativeRow(
                leading: const NativeRowIcon(ios: CupertinoIcons.globe, android: Icons.translate),
                title: Text(s.language),
                trailing: ChoiceMenu<AppLanguage>(
                  label: _languageLabel(s, settings.language),
                  options: [
                    for (final language in AppLanguage.values) (language, _languageLabel(s, language)),
                  ],
                  selected: settings.language,
                  onSelected: (v) => save(settings.copyWith(language: v)),
                ),
              ),
              NativeRow(
                leading: const NativeRowIcon(ios: CupertinoIcons.slider_horizontal_3, android: Icons.tune),
                title: Text(s.logLevel),
                trailing: ChoiceMenu<LogLevel>(
                  label: settings.logLevel.name,
                  options: [for (final level in LogLevel.values) (level, level.name)],
                  selected: settings.logLevel,
                  onSelected: (v) => save(settings.copyWith(logLevel: v)),
                ),
              ),
            ],
          ),
        ),
        SliverToBoxAdapter(child: _AboutSection(app: app)),
      ],
    );
  }

  Widget _listRow(BuildContext context, AppController app, SettingsList field) {
    final s = S.of(context);
    final values = field.read(app.settings);
    return NativeRow(
      leading: NativeRowIcon(
        ios: field.isDomain ? CupertinoIcons.globe : CupertinoIcons.number,
        android: field.isDomain ? Icons.language : Icons.numbers,
      ),
      title: Text(field.title(s)),
      value: values.isEmpty ? s.none : s.entries(values.length),
      chevron: true,
      onTap: () => pushPage<void>(
        context,
        (_) => AppScope(controller: app, child: ListEditorPage(field: field)),
      ),
    );
  }

  static String _sourceLabel(S s, RuleSource source) => switch (source) {
        RuleSource.builtin => s.builtinRules,
        RuleSource.custom => s.customRules,
        RuleSource.none => s.noRuleList,
      };

  static String _languageLabel(S s, AppLanguage language) => switch (language) {
        AppLanguage.system => s.followSystem,
        AppLanguage.en => 'English',
        AppLanguage.zh => '简体中文',
      };

  static bool _validUrl(String value) {
    final text = value.trim();
    if (text.contains(RegExp(r'\s'))) return false;
    final uri = Uri.tryParse(text);
    return uri != null && uri.scheme == 'https' && uri.host.isNotEmpty;
  }
}

/// When the rule list was downloaded, with an explicit Update action. The
/// list is otherwise reused from its saved copy, as on the desktop.
class _RuleListRow extends StatelessWidget {
  const _RuleListRow({required this.app});
  final AppController app;

  @override
  Widget build(BuildContext context) {
    final s = S.of(context);
    final date = app.rulesUpdatedAt;
    final status = app.rulesUpdating
        ? s.updating
        : date == null
            ? s.rulesNotDownloaded
            : s.updated(relativeTime(s, date));
    return NativeRow(
      leading: const NativeRowIcon(ios: CupertinoIcons.arrow_clockwise, android: Icons.sync),
      title: Text(s.ruleList),
      trailing: Row(
        mainAxisSize: MainAxisSize.min,
        children: [
          Flexible(
            child: Tooltip(
              message: status,
              child: Text(
                status,
                maxLines: 1,
                softWrap: false,
                overflow: TextOverflow.ellipsis,
                style: TextStyle(fontSize: 13, color: secondaryLabelColor(context)),
              ),
            ),
          ),
          const SizedBox(width: 4),
          TextAction(
            label: s.update,
            onPressed: app.rulesUpdating
                ? null
                : () async {
                    await app.updateRules();
                    if (!context.mounted || app.rulesUpdateError != null) return;
                    showNotice(context, app.needsReconnect ? s.rulesUpdatedReconnect : s.rulesUpdated);
                  },
          ),
        ],
      ),
    );
  }
}

/// Only rule-list errors need an explanation below the group. The download
/// time belongs to the Rule list row, not to the user's extra routing entries.
class _RuleListError extends StatelessWidget {
  const _RuleListError({required this.app});
  final AppController app;

  @override
  Widget build(BuildContext context) {
    final s = S.of(context);
    return Text(
      '${s.rulesUpdateFailed} ${causeText(s, app.rulesUpdateError) ?? ''}'.trim(),
      style: TextStyle(color: TsColors.of(context, TsColors.error)),
    );
  }
}

class _AboutSection extends StatelessWidget {
  const _AboutSection({required this.app});
  final AppController app;

  @override
  Widget build(BuildContext context) {
    final s = S.of(context);
    return NativeSection(
      header: s.about,
      footer: Text('TailSplit · GPL-3.0-or-later · idy.dev'),
      children: [
        NativeRow(
          leading: const NativeRowIcon(ios: CupertinoIcons.info_circle, android: Icons.info_outline),
          title: Text(s.version),
          value: '0.1.0',
        ),
        NativeRow(
          leading: const NativeRowIcon(ios: CupertinoIcons.gear_alt, android: Icons.settings_outlined),
          title: Text(s.coreVersion),
          value: app.coreVersion.isEmpty ? s.unavailable : app.coreVersion,
        ),
        NativeRow(
          leading: const NativeRowIcon(ios: CupertinoIcons.link, android: Icons.code),
          title: Text(s.sourceCode),
          value: 'GitHub',
          chevron: true,
          onTap: () => launchUrl(
            Uri.parse('https://github.com/idy/tailsplit'),
            mode: LaunchMode.inAppBrowserView,
          ),
        ),
        NativeRow(
          leading: const NativeRowIcon(ios: CupertinoIcons.doc_text, android: Icons.article_outlined),
          title: Text(s.licenses),
          chevron: true,
          onTap: () => showLicensePage(
            context: context,
            applicationName: 'TailSplit',
            applicationVersion: '0.1.0',
          ),
        ),
      ],
    );
  }
}
