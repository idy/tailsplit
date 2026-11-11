import 'dart:convert';
import 'dart:io';

import 'identity.dart';
import 'keys.dart';
import 'models.dart';

/// tsnet's on-disk state (`<Dir>/tailscaled.state`, an ipn/store FileStore:
/// a JSON object whose values are base64 []byte) as the tailscale fork
/// pinned by sing-box 1.14.4 reads it. sing-box's `tailscale` endpoint
/// passes `state_directory` as tsnet's `Dir`.
///
/// On start, tsnet's profile manager loads `_current-profile` → the
/// `_profiles` entry → `profile-<id>` prefs. `LocalBackend.Start` then keeps
/// only `Persist` from those prefs (sing-box's UpdatePrefs replace the rest,
/// with `LoggedOut: false`), reads `_machinekey`, and because a node key is
/// present calls a non-interactive login with that key. Control accepts the
/// already-authorized key, so no browser sign-in happens.
abstract final class TsnetState {
  static const stateFileName = 'tailscaled.state';

  static const _machineKey = '_machinekey';
  static const _currentProfile = '_current-profile';
  static const _knownProfiles = '_profiles';

  /// Write a FileStore that the pinned tsnet/sing-box endpoint loads as an
  /// already-authenticated profile. Replaces any existing state file.
  ///
  /// [controlUrl] must match the endpoint's `control_url`; leave it null when
  /// that option is unset (tsnet then stores an empty ControlURL).
  static Future<void> write({
    required Directory stateDirectory,
    required TailscaleIdentity identity,
    required TailnetSnapshot snapshot,
    Uri? controlUrl,
  }) async {
    final control = controlUrl == null ? '' : controlUrl.toString().replaceAll(RegExp(r'/+$'), '');
    final id = hexEncode(randomBytes(2));
    final profileKey = 'profile-$id';
    final userProfile = <String, Object?>{
      'ID': snapshot.userId,
      'LoginName': snapshot.userLoginName,
      'DisplayName': snapshot.userDisplayName.isEmpty ? snapshot.userLoginName : snapshot.userDisplayName,
      if (snapshot.userProfilePicUrl.isNotEmpty) 'ProfilePicURL': snapshot.userProfilePicUrl,
    };
    final loginProfile = <String, Object?>{
      'ID': id,
      'Name': snapshot.userLoginName,
      'NetworkProfile': {
        'MagicDNSName': snapshot.magicDnsSuffix,
        'DomainName': snapshot.domainName.isEmpty ? snapshot.networkName : snapshot.domainName,
        'DisplayName': snapshot.tailnetDisplayName,
      },
      'Key': profileKey,
      'UserProfile': userProfile,
      'NodeID': snapshot.stableNodeId,
      'LocalUserID': '',
      'ControlURL': control,
      'Created': DateTime.now().toUtc().toIso8601String(),
    };
    // Fields not written keep ipn.NewPrefs defaults when tsnet loads them.
    final prefs = <String, Object?>{
      'ControlURL': control,
      'WantRunning': true,
      'LoggedOut': false,
      'Hostname': identity.hostname,
      'Config': {
        'PrivateNodeKey': identity.nodePrivateKey,
        'UserProfile': userProfile,
        if (identity.networkLockPrivateKey != null) 'NetworkLockKey': identity.networkLockPrivateKey,
        'NodeID': snapshot.stableNodeId,
      },
    };
    String b64(String s) => base64.encode(utf8.encode(s));
    final state = <String, String>{
      _machineKey: b64(identity.machinePrivateKey),
      _currentProfile: b64(profileKey),
      _knownProfiles: b64(jsonEncode({id: loginProfile})),
      profileKey: b64(const JsonEncoder.withIndent('\t').convert(prefs)),
    };

    await stateDirectory.create(recursive: true);
    final file = _stateFile(stateDirectory);
    final temp = File('${file.path}.tmp-${hexEncode(randomBytes(4))}');
    try {
      await temp.writeAsString(const JsonEncoder.withIndent('  ').convert(state), flush: true);
      await temp.rename(file.path);
    } catch (_) {
      if (await temp.exists()) await temp.delete();
      rethrow;
    }
  }

  /// Read back the node private key tsnet may have rotated, so the app keeps
  /// using the live key. Null when there is no usable state.
  static Future<String?> readNodePrivateKey(Directory stateDirectory) async {
    final persist = (await _currentPrefs(stateDirectory))?['Config'];
    if (persist is! Map) return null;
    final key = persist['PrivateNodeKey'];
    if (key is! String) return null;
    try {
      if (isZeroKey(parsePrivateKey(key))) return null;
    } on FormatException {
      return null;
    }
    return key;
  }

  /// Whether the current tsnet profile is marked logged out (e.g. after
  /// sing-box's `Logout`). Null when there is no usable state.
  static Future<bool?> readLoggedOut(Directory stateDirectory) async {
    final prefs = await _currentPrefs(stateDirectory);
    if (prefs == null) return null;
    return prefs['LoggedOut'] == true;
  }

  /// Removes tsnet's state and the other files it keeps in the directory.
  static Future<void> clear(Directory stateDirectory) async {
    if (!await stateDirectory.exists()) return;
    const files = {'tailscaled.log.conf', 'tailscaled.log1.txt', 'tailscaled.log2.txt'};
    const dirs = {'tka-profiles', 'certs'};
    await for (final entry in stateDirectory.list(followLinks: false)) {
      final name = entry.uri.pathSegments.where((s) => s.isNotEmpty).last;
      final isState = name == stateFileName || name.startsWith('$stateFileName.');
      if (entry is File && (isState || files.contains(name))) {
        await entry.delete();
      } else if (entry is Directory && dirs.contains(name)) {
        await entry.delete(recursive: true);
      }
    }
  }

  static File _stateFile(Directory dir) => File('${dir.path}${Platform.pathSeparator}$stateFileName');

  static Future<Map<String, Object?>?> _currentPrefs(Directory dir) async {
    final file = _stateFile(dir);
    if (!await file.exists()) return null;
    try {
      final store = jsonDecode(await file.readAsString());
      if (store is! Map) return null;
      String? value(String key) {
        final v = store[key];
        return v is String ? utf8.decode(base64.decode(v)) : null;
      }

      final profileKey = value(_currentProfile);
      if (profileKey == null || profileKey.isEmpty) return null;
      final prefsText = value(profileKey);
      if (prefsText == null) return null;
      final prefs = jsonDecode(prefsText);
      return prefs is Map ? prefs.cast<String, Object?>() : null;
    } on FormatException {
      return null;
    }
  }
}
