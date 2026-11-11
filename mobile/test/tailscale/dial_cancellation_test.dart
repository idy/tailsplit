import 'dart:async';
import 'dart:io';
import 'dart:typed_data';

import 'package:flutter_test/flutter_test.dart';
import 'package:tailsplit_mobile/src/tailscale/control_conn.dart';

class _LateSocket extends Fake implements Socket {
  bool destroyed = false;
  bool configured = false;
  @override
  void destroy() => destroyed = true;
  @override
  bool setOption(SocketOption option, bool enabled) {
    configured = true;
    return true;
  }
}

void main() {
  for (final cancelled in [true, false]) {
    test('a socket arriving after ${cancelled ? "cancel" : "timeout"} is destroyed before upgrade', () async {
      final pending = Completer<Socket>();
      final token = CancelToken();
      final socket = _LateSocket();
      await IOOverrides.runZoned(() async {
        final dial = dialTs2021(
          endpoint: ControlEndpoint(Uri.parse('http://127.0.0.1:1234')),
          machinePrivate: Uint8List(32),
          controlPublic: Uint8List(32),
          cancel: token,
          timeout: const Duration(milliseconds: 10),
        );
        final rejected = expectLater(dial, throwsA(cancelled ? isA<ControlCancelled>() : isA<ControlTransientException>()));
        if (cancelled) token.cancel();
        await rejected;
        pending.complete(socket);
        await Future<void>.delayed(Duration.zero);
        expect(socket.destroyed, isTrue);
        expect(socket.configured, isFalse, reason: 'Late sockets must not enter HTTP/Noise upgrade.');
      }, socketConnect: (host, port, {sourceAddress, int sourcePort = 0, Duration? timeout}) => pending.future);
    });
  }
}
