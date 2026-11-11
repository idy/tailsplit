import 'dart:math';

import '../tailscale/tailscale.dart';
import 'exit_profile.dart';

/// One independently authenticated embedded device. The local ID, never a
/// display name, determines its native state and cache locations.
class TailnetAccount {
  const TailnetAccount({
    required this.id,
    required this.identity,
    this.snapshot,
    this.usesLegacyState = false,
  });

  final String id;
  final TailscaleIdentity identity;
  final TailnetSnapshot? snapshot;

  /// A migrated account keeps the original directory in place because an
  /// OS-owned VPN may still reference it after the app has been upgraded.
  final bool usesLegacyState;

  String get networkName => snapshot?.networkName ?? '';
  String get networkKey => networkKeyOf(snapshot);

  /// Display names and login names are not unique tailnet identifiers. Without
  /// a stable DNS suffix we keep accounts separate rather than guess a match.
  static String networkKeyOf(TailnetSnapshot? snapshot) =>
      snapshot?.magicDnsSuffix.trim().toLowerCase().replaceAll(RegExp(r'\.+$'), '') ?? '';

  static String newId() {
    final random = Random.secure();
    return List.generate(16, (_) => random.nextInt(256).toRadixString(16).padLeft(2, '0')).join();
  }

  TailnetAccount copyWith({TailscaleIdentity? identity, TailnetSnapshot? snapshot}) => TailnetAccount(
        id: id,
        identity: identity ?? this.identity,
        snapshot: snapshot ?? this.snapshot,
        usesLegacyState: usesLegacyState,
      );

  Map<String, Object?> toJson() => {
        'id': id,
        'identity': identity.toJson(),
        if (snapshot != null) 'snapshot': snapshot!.toJson(),
        if (usesLegacyState) 'uses_legacy_state': true,
      };

  factory TailnetAccount.fromJson(Map<String, Object?> json) {
    final id = json['id'];
    if (id is! String || !RegExp(r'^[a-zA-Z0-9_-]{1,80}$').hasMatch(id)) {
      throw const FormatException('Invalid local tailnet ID');
    }
    return TailnetAccount(
      id: id,
      identity: TailscaleIdentity.fromJson((json['identity'] as Map).cast<String, Object?>()),
      snapshot: json['snapshot'] is Map
          ? TailnetSnapshot.fromJson((json['snapshot'] as Map).cast<String, Object?>())
          : null,
      usesLegacyState: json['uses_legacy_state'] == true,
    );
  }
}

/// The only persisted source of accounts and the sole global node selection.
/// An empty store is meaningful: legacy exit.json must never resurrect it.
class TailnetStore {
  TailnetStore({Iterable<TailnetAccount> accounts = const [], this.selection})
      : accounts = Map.unmodifiable({for (final account in accounts) account.id: account});

  final Map<String, TailnetAccount> accounts;
  final ExitSelection? selection;

  TailnetStore withAccount(TailnetAccount account) => TailnetStore(
        accounts: {...accounts, account.id: account}.values,
        selection: selection,
      );

  TailnetStore withoutAccount(String id) => TailnetStore(
        accounts: accounts.values.where((account) => account.id != id),
        selection: selection?.tailnetId == id ? null : selection,
      );

  TailnetStore select(ExitSelection? next) => TailnetStore(accounts: accounts.values, selection: next);

  factory TailnetStore.fromLegacy(ExitProfile profile, {bool allowUnselectedFixture = false}) {
    const id = 'legacy';
    return TailnetStore(
      accounts: [TailnetAccount(id: id, identity: profile.identity, snapshot: profile.snapshot, usesLegacyState: true)],
      selection: profile.hasNode || allowUnselectedFixture ? ExitSelection.fromProfile(id, profile) : null,
    );
  }

  Map<String, Object?> toJson() => {
        'version': 1,
        'accounts': [for (final account in accounts.values) account.toJson()],
        'selection': selection?.toJson(),
      };

  factory TailnetStore.fromJson(Map<String, Object?> json) {
    if (json['version'] != 1 || json['accounts'] is! List) {
      throw const FormatException('Unsupported tailnet store');
    }
    final accounts = <TailnetAccount>[];
    final ids = <String>{};
    var hasLegacy = false;
    for (final value in json['accounts'] as List) {
      final account = TailnetAccount.fromJson((value as Map).cast<String, Object?>());
      if (!ids.add(account.id) || (account.usesLegacyState && hasLegacy)) {
        throw const FormatException('Duplicate tailnet state owner');
      }
      hasLegacy = hasLegacy || account.usesLegacyState;
      accounts.add(account);
    }
    final selection = json['selection'] is Map
        ? ExitSelection.fromJson((json['selection'] as Map).cast<String, Object?>())
        : null;
    return TailnetStore(
      accounts: accounts,
      selection: selection != null && ids.contains(selection.tailnetId) && selection.nodeId.isNotEmpty
          ? selection : null,
    );
  }
}
