import 'dart:convert';
import 'dart:io';

import 'package:flutter/foundation.dart' show visibleForTesting;
import 'package:flutter/services.dart';
import 'package:path_provider/path_provider.dart';

/// Identifiers shared by the app, its Packet Tunnel extension and Android
/// service. They must match the signed entitlements.
abstract final class AppIdentifiers {
  static const appGroup = 'group.dev.idy.tailsplit';
  static const packetTunnel = 'dev.idy.tailsplit.PacketTunnel';
  static const vpnName = 'TailSplit';
}

/// File locations. Private app state stays in the app's own support directory;
/// the Tailscale state directory and sing-box cache live where the VPN process
/// can read them (the App Group container on iOS).
class AppPaths {
  AppPaths._(this.appDirectory, this.sharedDirectory);

  @visibleForTesting
  factory AppPaths.at(Directory app, Directory shared) => AppPaths._(app, shared);

  final Directory appDirectory;
  final Directory sharedDirectory;

  static const _channel = MethodChannel('dev.idy.tailsplit/container');

  static Future<AppPaths> resolve() async {
    final app = await getApplicationSupportDirectory();
    var shared = app;
    if (Platform.isIOS) {
      try {
        final path = await _channel.invokeMethod<String>(
          'appGroupPath',
          {'group': AppIdentifiers.appGroup},
        );
        if (path != null && path.isNotEmpty) shared = Directory(path);
      } on PlatformException {
        // Unsigned builds have no App Group; the UI still runs, the VPN can't.
      } on MissingPluginException {
        // Same as above for hosts without the container channel.
      }
    }
    await app.create(recursive: true);
    await shared.create(recursive: true);
    final paths = AppPaths._(app, shared);
    await paths.tailscaleState.create(recursive: true);
    if (Platform.isIOS) {
      for (final directory in [app, paths.tailscaleState]) {
        try {
          await _channel.invokeMethod<void>('excludeFromBackup', {'path': directory.path});
        } on PlatformException {
          // Best effort; Android excludes app data through its manifest.
        }
      }
    }
    return paths;
  }

  File get settingsFile => File('${appDirectory.path}/settings.json');
  File get exitFile => File('${appDirectory.path}/exit.json');
  Directory get tailscaleState => Directory('${sharedDirectory.path}/tailscale');
  String get cacheFile => '${sharedDirectory.path}/cache.db';
  Directory get rulesDirectory => Directory('${appDirectory.path}/rules');
}

/// Atomic JSON persistence.
abstract final class JsonStore {
  static Future<Map<String, Object?>?> read(File file) async {
    try {
      if (!await file.exists()) return null;
      final decoded = jsonDecode(await file.readAsString());
      return decoded is Map ? decoded.cast<String, Object?>() : null;
    } on FormatException {
      return null;
    }
  }

  static Future<void> write(File file, Map<String, Object?> value) async {
    await file.parent.create(recursive: true);
    final temp = File('${file.path}.tmp');
    await temp.writeAsString(jsonEncode(value), flush: true);
    await temp.rename(file.path);
  }

  static Future<void> delete(File file) async {
    if (await file.exists()) await file.delete();
  }
}
