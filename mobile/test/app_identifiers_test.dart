import 'dart:convert';
import 'dart:io';

import 'package:flutter/services.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:tailsplit_mobile/src/services/storage.dart';
import 'package:tailsplit_mobile/src/services/vpn.dart';

void main() {
  final binding = TestWidgetsFlutterBinding.ensureInitialized();
  const native = MethodChannel('dev.idy.sing_box_flutter/methods');

  tearDown(() {
    binding.defaultBinaryMessenger.setMockMethodCallHandler(native, null);
  });

  test(
    'native VPN initialization and storage match the signing channel',
    () async {
      const channel = String.fromEnvironment(
        'TAILSPLIT_IOS_CHANNEL',
        defaultValue: 'app-store',
      );
      final manifest = jsonDecode(
        File('release-identity.json').readAsStringSync(),
      );
      final identity = manifest['channels'][channel] as Map<String, dynamic>;
      final calls = <MethodCall>[];
      binding.defaultBinaryMessenger.setMockMethodCallHandler(native, (
        call,
      ) async {
        calls.add(call);
        return null;
      });

      await VpnService().initialize();

      expect(calls, hasLength(1));
      expect(calls.single.method, 'initialize');
      expect(calls.single.arguments, {
        'appGroup': identity['app_group'],
        'providerBundleIdentifier': identity['packet_tunnel_bundle_id'],
        'notificationTitle': identity['name'],
      });
      expect(AppIdentifiers.appGroup, identity['app_group']);
    },
  );
}
