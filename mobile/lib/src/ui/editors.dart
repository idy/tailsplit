import 'dart:io';
import 'dart:typed_data';

import 'package:flutter/cupertino.dart';
import 'package:flutter/material.dart';

import '../app.dart';
import '../l10n/strings.dart';
import '../model/settings.dart';
import '../services/rules.dart';
import '../theme/tokens.dart';
import 'common.dart';
import 'native.dart';

/// Editable routing lists in Settings › Routing.
enum SettingsList {
  proxyDomains,
  directDomains,
  proxyIps,
  directIps;

  bool get isDomain => this == proxyDomains || this == directDomains;

  String title(S s) => switch (this) {
        proxyDomains => s.proxyDomains,
        directDomains => s.directDomains,
        proxyIps => s.proxyIps,
        directIps => s.directIps,
      };

  List<String> read(MobileSettings settings) => switch (this) {
        proxyDomains => settings.proxyDomains,
        directDomains => settings.directDomains,
        proxyIps => settings.proxyIps,
        directIps => settings.directIps,
      };

  MobileSettings write(MobileSettings settings, List<String> values) => switch (this) {
        proxyDomains => settings.copyWith(proxyDomains: values),
        directDomains => settings.copyWith(directDomains: values),
        proxyIps => settings.copyWith(proxyIps: values),
        directIps => settings.copyWith(directIps: values),
      };

  /// Returns the canonical entry, or null when invalid.
  String? normalize(String input) {
    final value = input.trim();
    if (isDomain) {
      final domain = RuleLists.canonicalDomain(value.startsWith('.') ? value.substring(1) : value);
      return domain != null && domain.contains('.') ? domain : null;
    }
    final slash = value.indexOf('/');
    final host = slash < 0 ? value : value.substring(0, slash);
    if (!host.contains(':') && !RegExp(r'^\d{1,3}(\.\d{1,3}){3}$').hasMatch(host)) return null;
    final address = InternetAddress.tryParse(host);
    if (address == null) return null;
    final max = address.type == InternetAddressType.IPv4 ? 32 : 128;
    final prefix = slash < 0 ? max : int.tryParse(value.substring(slash + 1));
    if (prefix == null || prefix < 0 || prefix > max) return null;
    // Store the network itself: 192.0.2.7/24 becomes 192.0.2.0/24.
    final bytes = Uint8List.fromList(address.rawAddress);
    for (var bit = prefix; bit < max; bit++) {
      bytes[bit ~/ 8] &= ~(0x80 >> (bit % 8));
    }
    final network = InternetAddress.fromRawAddress(bytes, type: address.type);
    return '${network.address}/$prefix';
  }
}

class ListEditorPage extends StatelessWidget {
  const ListEditorPage({super.key, required this.field});

  final SettingsList field;

  @override
  Widget build(BuildContext context) {
    final app = AppScope.of(context);
    final s = S.of(context);
    final values = field.read(app.settings);
    void save(List<String> next) => app.updateSettings(field.write(app.settings, next));

    void remove(String value) {
      final before = field.read(app.settings);
      final index = before.indexOf(value);
      if (index < 0) return;
      save([...before]..removeAt(index));
      showNotice(
        context,
        s.removed(value),
        action: s.undo,
        onAction: () {
          final current = field.read(app.settings);
          if (current.contains(value)) return;
          save([...current]..insert(index.clamp(0, current.length), value));
        },
      );
    }

    Future<void> add() async {
      final value = await promptText(
        context,
        title: field.title(s),
        initial: '',
        hint: field.isDomain ? s.domainHint : s.cidrHint,
        // The URL keyboard has '.', '/' and ':' for CIDRs and IPv6; the
        // numeric pad cannot type a prefix length.
        keyboard: TextInputType.url,
        validate: (v) => field.normalize(v) == null
            ? (field.isDomain ? s.invalidDomain : s.invalidCidr)
            : null,
      );
      if (value == null) return;
      final entry = field.normalize(value)!;
      if (!values.contains(entry)) save([...values, entry]);
    }

    return NativePage(
      title: field.title(s),
      largeTitle: false,
      bottom: app.needsReconnect ? const ReconnectBar() : null,
      actions: [
        PageAction(label: s.add, iosSymbol: 'plus', icon: Icons.add, onPressed: add),
      ],
      slivers: [
        SliverToBoxAdapter(
          child: NativeSection(
            footer: Text(field.isDomain ? s.listFooterDomains : s.listFooterIps),
            children: [
              if (values.isEmpty)
                NativeRow(
                  leading: const NativeRowIcon(ios: CupertinoIcons.list_bullet, android: Icons.list),
                  title: Text(s.listEmpty, style: TextStyle(color: secondaryLabelColor(context))),
                ),
              for (final value in values)
                Dismissible(
                  key: ValueKey(value),
                  direction: DismissDirection.endToStart,
                  background: Container(
                    alignment: Alignment.centerRight,
                    padding: const EdgeInsets.only(right: 20),
                    color: isCupertino
                        ? CupertinoColors.systemRed.resolveFrom(context)
                        : Theme.of(context).colorScheme.error,
                    child: Text(
                      s.delete,
                      style: const TextStyle(color: Colors.white, fontWeight: FontWeight.w600),
                    ),
                  ),
                  onDismissed: (_) => remove(value),
                  child: NativeRow(
                    leading: NativeRowIcon(
                      ios: field.isDomain ? CupertinoIcons.globe : CupertinoIcons.number,
                      android: field.isDomain ? Icons.language : Icons.numbers,
                    ),
                    title: Text(
                      value,
                      style: const TextStyle(fontFeatures: [FontFeature.tabularFigures()]),
                    ),
                    trailing: _DeleteButton(
                      label: '${s.delete} $value',
                      onPressed: () => remove(value),
                    ),
                  ),
                ),
              NativeRow(
                leading: Icon(
                  isCupertino ? CupertinoIcons.plus_circle_fill : Icons.add_circle_outline,
                  color: isCupertino ? TsColors.of(context, TsColors.good) : null,
                  size: 22,
                ),
                title: Text(s.add),
                onTap: add,
              ),
            ],
          ),
        ),
      ],
    );
  }
}

class _DeleteButton extends StatelessWidget {
  const _DeleteButton({required this.label, required this.onPressed});
  final String label;
  final VoidCallback onPressed;

  @override
  Widget build(BuildContext context) => Semantics(
        button: true,
        label: label,
        child: GestureDetector(
          behavior: HitTestBehavior.opaque,
          onTap: onPressed,
          child: SizedBox(
            width: 44,
            height: 44,
            child: Icon(
              isCupertino ? CupertinoIcons.minus_circle : Icons.remove_circle_outline,
              size: 20,
              color: isCupertino
                  ? CupertinoColors.systemRed.resolveFrom(context)
                  : Theme.of(context).colorScheme.error,
            ),
          ),
        ),
      );
}

/// A native alert with one text field. Returns the trimmed value, or null when
/// cancelled. Invalid input keeps the alert open with its explanation.
Future<String?> promptText(
  BuildContext context, {
  required String title,
  required String initial,
  required String hint,
  required String? Function(String value) validate,
  TextInputType keyboard = TextInputType.text,
  bool allowEmpty = false,
}) {
  final s = S.of(context);
  final controller = TextEditingController(text: initial);
  String? error;

  void submit(BuildContext dialogContext, StateSetter setState) {
    final value = controller.text.trim();
    final message = value.isEmpty && !allowEmpty ? '' : validate(value);
    if (message != null) {
      setState(() => error = message.isEmpty ? null : message);
      return;
    }
    Navigator.pop(dialogContext, value);
  }

  return (isCupertino
          ? showCupertinoDialog<String>(
              context: context,
              builder: (context) => StatefulBuilder(
                builder: (context, setState) {
                  return CupertinoAlertDialog(
                    title: Text(title),
                    content: Column(
                      children: [
                        const SizedBox(height: 12),
                        CupertinoTextField(
                          controller: controller,
                          placeholder: hint,
                          autofocus: true,
                          autocorrect: false,
                          keyboardType: keyboard,
                          textCapitalization: TextCapitalization.none,
                          onSubmitted: (_) => submit(context, setState),
                        ),
                        if (error != null) ...[
                          const SizedBox(height: 8),
                          Text(
                            error!,
                            style: TextStyle(
                              fontSize: 13,
                              color: TsColors.of(context, TsColors.error),
                            ),
                          ),
                        ],
                      ],
                    ),
                    actions: [
                      CupertinoDialogAction(
                        onPressed: () => Navigator.pop(context),
                        child: Text(s.cancel),
                      ),
                      CupertinoDialogAction(
                        isDefaultAction: true,
                        onPressed: () => submit(context, setState),
                        child: Text(s.done),
                      ),
                    ],
                  );
                },
              ),
            )
          : showDialog<String>(
              context: context,
              builder: (context) => StatefulBuilder(
                builder: (context, setState) {
                  return AlertDialog(
                    title: Text(title),
                    content: TextField(
                      controller: controller,
                      autofocus: true,
                      autocorrect: false,
                      keyboardType: keyboard,
                      decoration: InputDecoration(hintText: hint, errorText: error),
                      onSubmitted: (_) => submit(context, setState),
                    ),
                    actions: [
                      TextButton(onPressed: () => Navigator.pop(context), child: Text(s.cancel)),
                      TextButton(onPressed: () => submit(context, setState), child: Text(s.done)),
                    ],
                  );
                },
              ),
            ))
      .whenComplete(controller.dispose);
}
