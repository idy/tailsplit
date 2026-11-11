// ts2021 transport: HTTP/1.1 upgrade to the Tailscale control protocol
// (control/controlhttp), Noise (control/controlbase), the optional early
// payload (control/ts2021/conn.go) and HTTP/2 on top.

import 'dart:async';
import 'dart:convert';
import 'dart:io';
import 'dart:typed_data';

import 'package:http2/transport.dart';

import 'keys.dart';
import 'models.dart';
import 'noise.dart';
import 'tailcfg.dart' show currentCapabilityVersion;

const ts2021UpgradePath = '/ts2021';
const upgradeHeaderValue = 'tailscale-control-protocol';
const handshakeHeaderName = 'X-Tailscale-Handshake';

/// `EarlyPayloadMagic` in control/ts2021.
const earlyPayloadMagic = [0xff, 0xff, 0xff, 0x54, 0x53];
const _earlyPayloadHeaderLength = 9;
const _maxEarlyPayload = 10 << 20;

/// Removes URLs and key texts from strings that come from the network before
/// they go into exception messages, and caps their length.
String redact(String text, {int maxLength = 200}) {
  var out = text
      .replaceAll(RegExp(r'[a-zA-Z][a-zA-Z0-9+.-]*://\S+'), '<url>')
      .replaceAll(RegExp(r'\b(privkey|nodekey|mkey|discokey|nlpriv|nlpub|tlpub|chalpub):[0-9a-fA-F]+'), '<key>')
      .trim();
  if (out.length > maxLength) out = '${out.substring(0, maxLength)}…';
  return out;
}

/// Cooperative cancellation for in-flight dials and requests.
class CancelToken {
  bool _cancelled = false;
  final _callbacks = <void Function()>[];

  bool get isCancelled => _cancelled;

  void cancel() {
    if (_cancelled) return;
    _cancelled = true;
    for (final cb in List.of(_callbacks)) {
      try {
        cb();
      } catch (_) {}
    }
    _callbacks.clear();
  }

  /// Runs [callback] on cancel (immediately if already cancelled).
  /// Returns a function that unregisters it.
  void Function() onCancel(void Function() callback) {
    if (_cancelled) {
      callback();
      return () {};
    }
    _callbacks.add(callback);
    return () => _callbacks.remove(callback);
  }

  void throwIfCancelled() {
    if (_cancelled) throw const ControlCancelled();
  }
}

class ControlCancelled implements Exception {
  const ControlCancelled();
  @override
  String toString() => 'ControlCancelled';
}

/// A transient transport failure worth retrying (network, timeouts, 5xx).
class ControlTransientException extends TailscaleControlException {
  const ControlTransientException(super.message, {super.kind, this.retryAfter});
  final Duration? retryAfter;
}

/// Buffers a byte stream for the pre-Noise phase and then hands the rest of
/// it over as a stream.
class ChunkReader {
  ChunkReader(Stream<List<int>> source) {
    _sub = source.listen(_onData, onError: _onError, onDone: _onDone);
  }

  late final StreamSubscription<List<int>> _sub;
  final _buffer = BytesBuilder(copy: false);
  Uint8List _bytes = Uint8List(0);
  int _offset = 0;
  bool _done = false;
  Object? _error;
  Completer<void>? _waiter;
  StreamController<List<int>>? _forward;

  int get _available => _bytes.length - _offset + _buffer.length;

  void _onData(List<int> data) {
    final forward = _forward;
    if (forward != null) {
      forward.add(data);
      return;
    }
    _buffer.add(data);
    _wake();
  }

  void _onError(Object error) {
    final forward = _forward;
    if (forward != null) {
      forward.addError(error);
      return;
    }
    _error ??= error;
    _wake();
  }

  void _onDone() {
    _done = true;
    final forward = _forward;
    if (forward != null) {
      forward.close();
      return;
    }
    _wake();
  }

  void _wake() {
    final w = _waiter;
    _waiter = null;
    w?.complete();
  }

  void _compact() {
    if (_buffer.isEmpty) return;
    final rest = _bytes.sublist(_offset);
    _bytes = Uint8List.fromList([...rest, ..._buffer.takeBytes()]);
    _offset = 0;
  }

  Future<void> _waitForMore() async {
    if (_error != null) throw _error!;
    if (_done) throw const SocketException('connection closed by control server');
    _waiter ??= Completer<void>();
    await _waiter!.future;
    if (_error != null) throw _error!;
  }

  Future<Uint8List> readExactly(int n) async {
    while (_available < n) {
      await _waitForMore();
    }
    _compact();
    final out = Uint8List.fromList(_bytes.sublist(_offset, _offset + n));
    _offset += n;
    return out;
  }

  /// Reads through the blank line ending an HTTP/1.1 header block.
  Future<String> readHttpHeader({int maxBytes = 16 * 1024}) async {
    var searchFrom = 0;
    while (true) {
      _compact();
      final view = _bytes;
      for (var i = _offset + (searchFrom > 3 ? searchFrom - 3 : 0); i + 3 < view.length; i++) {
        if (view[i] == 13 && view[i + 1] == 10 && view[i + 2] == 13 && view[i + 3] == 10) {
          final header = latin1.decode(view.sublist(_offset, i));
          _offset = i + 4;
          return header;
        }
      }
      searchFrom = view.length - _offset;
      if (searchFrom > maxBytes) throw const SocketException('control server sent an oversized HTTP header');
      await _waitForMore();
    }
  }

  /// The unread bytes followed by the rest of the source.
  Stream<List<int>> detach() {
    _compact();
    // Not sync: events added before anyone listens are buffered in order.
    final controller = StreamController<List<int>>(
      onPause: _sub.pause,
      onResume: _sub.resume,
      onCancel: _sub.cancel,
    );
    final rest = _bytes.sublist(_offset);
    _bytes = Uint8List(0);
    _offset = 0;
    if (rest.isNotEmpty) controller.add(rest);
    if (_error != null) controller.addError(_error!);
    if (_done) {
      controller.close();
    } else {
      _forward = controller;
    }
    return controller.stream;
  }

  Future<void> cancel() => _sub.cancel();
}

/// An established Noise connection over a TCP or TLS socket.
class NoiseTransport {
  NoiseTransport._(this._socket, this.session, Stream<List<int>> raw, this.viaTls) {
    final decoder = NoiseFrameDecoder(session.rx);
    incoming = raw.transform(StreamTransformer<List<int>, List<int>>.fromHandlers(
      handleData: (data, sink) {
        try {
          for (final plaintext in decoder.add(data)) {
            sink.add(plaintext);
          }
        } on NoiseException catch (e) {
          sink.addError(e);
          destroy();
        }
      },
    ));
    final out = StreamController<List<int>>(sync: true);
    outgoing = out.sink;
    out.stream.listen(
      (data) {
        if (_closed) return;
        try {
          _socket.add(session.tx.encryptFrames(data));
        } catch (_) {
          destroy();
        }
      },
      onDone: () {
        if (!_closed) {
          _closed = true;
          _socket.close().catchError((_) => _socket);
        }
      },
    );
    _socket.done.then((_) {}, onError: (_) {});
  }

  final Socket _socket;
  final NoiseSession session;
  final bool viaTls;
  late final Stream<List<int>> incoming;
  late final StreamSink<List<int>> outgoing;
  bool _closed = false;

  void destroy() {
    if (_closed) return;
    _closed = true;
    _socket.destroy();
  }
}

/// Where to dial, following `ts2021.NewClient`'s port rules.
class ControlEndpoint {
  ControlEndpoint(this.controlUrl)
      : host = controlUrl.host,
        _ports = _portsFor(controlUrl);

  final Uri controlUrl;
  final String host;
  final (int?, int?) _ports;

  /// Plain HTTP port for the upgrade, or null when not to be tried.
  int? get httpPort => _ports.$1;

  /// HTTPS fallback port, or null when not to be tried.
  int? get httpsPort => _ports.$2;

  /// `:authority` for HTTP/2 requests (Go uses the bare host).
  String get authority => host;

  static (int?, int?) _portsFor(Uri u) {
    final private = _isPrivateHost(u.host);
    if (u.hasPort) {
      if (u.scheme == 'https') return (null, u.port);
      return (u.port, private ? null : 443);
    }
    if (u.scheme == 'http' && private) return (80, null);
    return (80, 443);
  }

  static bool _isPrivateHost(String host) {
    if (host == 'localhost') return true;
    final ip = InternetAddress.tryParse(host);
    if (ip == null) return false;
    if (ip.isLoopback) return true;
    final b = ip.rawAddress;
    if (ip.type == InternetAddressType.IPv4) {
      return b[0] == 10 || (b[0] == 172 && (b[1] & 0xf0) == 16) || (b[0] == 192 && b[1] == 168);
    }
    return (b[0] & 0xfe) == 0xfc;
  }
}

/// Dials control and completes the Noise handshake, racing plain HTTP
/// against an HTTPS fallback started after 500 ms (or as soon as HTTP fails),
/// like `controlhttp.Dialer.dialHost`.
Future<NoiseTransport> dialTs2021({
  required ControlEndpoint endpoint,
  required Uint8List machinePrivate,
  required Uint8List controlPublic,
  int protocolVersion = currentCapabilityVersion,
  Duration timeout = const Duration(seconds: 10),
  Duration httpsFallbackDelay = const Duration(milliseconds: 500),
  CancelToken? cancel,
}) {
  final completer = Completer<NoiseTransport>();
  final errors = <String>[];
  final sockets = <Socket>{};
  var inFlight = 0;
  Timer? fallback;
  Timer? deadline;
  void Function()? unregister;

  void finish({NoiseTransport? conn, Object? error}) {
    if (completer.isCompleted) {
      conn?.destroy();
      return;
    }
    fallback?.cancel();
    deadline?.cancel();
    unregister?.call();
    // Abort the losing attempts; keep the winner's socket.
    for (final s in sockets) {
      if (!identical(s, conn?._socket)) s.destroy();
    }
    sockets.clear();
    if (conn != null) {
      completer.complete(conn);
    } else {
      completer.completeError(error ?? const ControlTransientException('Cannot reach Tailscale control.', kind: ControlFailureKind.offline));
    }
  }

  void start(bool tls, int port) {
    if (completer.isCompleted) return;
    inFlight++;
    _upgrade(
      endpoint: endpoint,
      tls: tls,
      port: port,
      machinePrivate: machinePrivate,
      controlPublic: controlPublic,
      protocolVersion: protocolVersion,
      timeout: timeout,
      onSocket: sockets.add,
    ).then((conn) {
      inFlight--;
      finish(conn: conn);
    }, onError: (Object e) {
      inFlight--;
      errors.add('${tls ? 'HTTPS' : 'HTTP'}: ${_describe(e)}');
      if (e is TailscaleControlException && e is! ControlTransientException) {
        finish(error: e);
        return;
      }
      final https = endpoint.httpsPort;
      if (!tls && https != null && (fallback?.isActive ?? false)) {
        fallback!.cancel();
        start(true, https);
        return;
      }
      if (inFlight == 0 && !(fallback?.isActive ?? false)) {
        finish(
          error: ControlTransientException('Cannot reach Tailscale control (${errors.join('; ')}).', kind: ControlFailureKind.offline),
        );
      }
    });
  }

  if (cancel != null) {
    unregister = cancel.onCancel(() => finish(error: const ControlCancelled()));
  }
  if (!completer.isCompleted) {
    deadline = Timer(timeout, () {
      finish(error: const ControlTransientException('Timed out connecting to Tailscale control.', kind: ControlFailureKind.timeout));
    });
    final http = endpoint.httpPort;
    final https = endpoint.httpsPort;
    if (http != null) start(false, http);
    if (https != null) {
      if (http == null) {
        start(true, https);
      } else {
        fallback = Timer(httpsFallbackDelay, () => start(true, https));
      }
    }
    if (http == null && https == null) {
      finish(error: const TailscaleControlException('No port to reach Tailscale control on.'));
    }
  }
  return completer.future;
}

String _describe(Object e) {
  if (e is SocketException) return e.osError?.message ?? e.message;
  if (e is HandshakeException) return 'TLS handshake failed';
  if (e is TailscaleControlException) return e.message;
  if (e is NoiseException) return e.message;
  if (e is TimeoutException) return 'timed out';
  if (e is TransportException) return redact(e.message);
  return e.runtimeType.toString();
}

Future<NoiseTransport> _upgrade({
  required ControlEndpoint endpoint,
  required bool tls,
  required int port,
  required Uint8List machinePrivate,
  required Uint8List controlPublic,
  required int protocolVersion,
  required Duration timeout,
  required void Function(Socket) onSocket,
}) async {
  final Socket socket = tls
      ? await SecureSocket.connect(
          endpoint.host,
          port,
          timeout: timeout,
          supportedProtocols: const ['http/1.1'],
          // Like controlhttp, certificate problems on this connection are
          // not fatal: the Noise handshake authenticates the server with the
          // key fetched (with full TLS verification) from /key.
          onBadCertificate: (_) => true,
        )
      : await Socket.connect(endpoint.host, port, timeout: timeout);
  onSocket(socket);
  try {
    socket.setOption(SocketOption.tcpNoDelay, true);
    final handshake = NoiseClientHandshake.start(
      machinePrivate: machinePrivate,
      controlPublic: controlPublic,
      protocolVersion: protocolVersion,
    );
    final reader = ChunkReader(socket);
    final defaultPort = tls ? 443 : 80;
    final hostHeader = port == defaultPort ? endpoint.host : '${endpoint.host}:$port';
    socket.add(latin1.encode('POST $ts2021UpgradePath HTTP/1.1\r\n'
        'Host: $hostHeader\r\n'
        'User-Agent: Go-http-client/1.1\r\n'
        'Content-Length: 0\r\n'
        'Connection: upgrade\r\n'
        'Upgrade: $upgradeHeaderValue\r\n'
        '$handshakeHeaderName: ${base64.encode(handshake.initiation)}\r\n'
        '\r\n'));

    final header = await reader.readHttpHeader();
    final lines = header.split('\r\n');
    final status = lines.first.split(' ');
    if (status.length < 2 || !status[0].startsWith('HTTP/1.')) {
      throw const ControlTransientException('unexpected reply to the protocol upgrade');
    }
    if (status[1] != '101') {
      throw ControlTransientException('unexpected HTTP response: ${status[1]}');
    }
    String? upgrade;
    for (final line in lines.skip(1)) {
      final colon = line.indexOf(':');
      if (colon > 0 && line.substring(0, colon).trim().toLowerCase() == 'upgrade') {
        upgrade = line.substring(colon + 1).trim();
      }
    }
    if (upgrade != upgradeHeaderValue) {
      throw const ControlTransientException('server switched to an unexpected protocol');
    }

    final head = await reader.readExactly(noiseHeaderLength);
    if (head[0] == msgTypeError) {
      final message = await reader.readExactly((head[1] << 8) | head[2]);
      throw TailscaleControlException(
          'Tailscale control refused the connection: ${redact(utf8.decode(message, allowMalformed: true))}');
    }
    if (head[0] != msgTypeResponse) {
      throw NoiseException('unexpected response message type ${head[0]}');
    }
    final rest = await reader.readExactly(responseMessageLength - noiseHeaderLength);
    final session = handshake.finish(Uint8List.fromList([...head, ...rest]));
    return NoiseTransport._(socket, session, reader.detach(), tls);
  } catch (_) {
    socket.destroy();
    rethrow;
  }
}

/// Removes the optional early payload (`\xff\xff\xffTS` + BE uint32 length +
/// JSON `tailcfg.EarlyNoise`) the server may send before HTTP/2 starts.
Stream<List<int>> stripEarlyPayload(Stream<List<int>> input) {
  var state = 0; // 0: reading header, 1: reading payload, 2: pass-through
  var need = _earlyPayloadHeaderLength;
  final pending = BytesBuilder(copy: true);
  return input.transform(StreamTransformer<List<int>, List<int>>.fromHandlers(
    handleData: (data, sink) {
      if (state == 2) {
        sink.add(data);
        return;
      }
      pending.add(data);
      if (state == 0) {
        if (pending.length < _earlyPayloadHeaderLength) return;
        final bytes = pending.toBytes();
        var magic = true;
        for (var i = 0; i < earlyPayloadMagic.length; i++) {
          if (bytes[i] != earlyPayloadMagic[i]) magic = false;
        }
        if (!magic) {
          state = 2;
          sink.add(pending.takeBytes());
          return;
        }
        final length = ByteData.sublistView(bytes, 5, 9).getUint32(0, Endian.big);
        if (length > _maxEarlyPayload) {
          sink.addError(const NoiseException('invalid early payload length'));
          return;
        }
        need = _earlyPayloadHeaderLength + length;
        state = 1;
      }
      if (state == 1 && pending.length >= need) {
        final bytes = pending.takeBytes();
        try {
          final decoded = jsonDecode(utf8.decode(bytes.sublist(_earlyPayloadHeaderLength, need)));
          if (decoded != null && decoded is! Map) throw const FormatException();
        } on FormatException {
          sink.addError(const NoiseException('invalid early payload'));
          return;
        }
        state = 2;
        if (bytes.length > need) sink.add(bytes.sublist(need));
      }
    },
  ));
}

/// The response to a request on the control HTTP/2 connection.
class ControlResponse {
  ControlResponse._(this.status, this.body, this._cancel);

  final int status;

  /// Single-subscription body stream.
  final Stream<List<int>> body;
  final void Function() _cancel;

  /// Aborts the stream (RST_STREAM) if still open.
  void cancel() => _cancel();

  Future<Uint8List> readAll({int maxBytes = 1 << 20}) async {
    final out = BytesBuilder(copy: false);
    await for (final chunk in body) {
      out.add(chunk);
      if (out.length > maxBytes) {
        cancel();
        throw const TailscaleControlException('Tailscale control sent an oversized response.');
      }
    }
    return out.takeBytes();
  }
}

/// HTTP/2 over Noise to control (what `ts2021.Client` does with net/http).
class ControlSession {
  ControlSession._(this._transport, this._connection, this.endpoint);

  static Future<ControlSession> open({
    required ControlEndpoint endpoint,
    required Uint8List machinePrivate,
    required Uint8List controlPublic,
    int protocolVersion = currentCapabilityVersion,
    CancelToken? cancel,
  }) async {
    final transport = await dialTs2021(
      endpoint: endpoint,
      machinePrivate: machinePrivate,
      controlPublic: controlPublic,
      protocolVersion: protocolVersion,
      cancel: cancel,
    );
    final connection = ClientTransportConnection.viaStreams(
      stripEarlyPayload(transport.incoming),
      transport.outgoing,
    );
    return ControlSession._(transport, connection, endpoint);
  }

  final NoiseTransport _transport;
  final ClientTransportConnection _connection;
  final ControlEndpoint endpoint;
  bool _closed = false;

  bool get isOpen => !_closed && _connection.isOpen;

  /// POSTs JSON [body] to [path]. [lbKeys] become `Ts-Lb` headers.
  Future<ControlResponse> post(
    String path,
    Object? body, {
    List<String> lbKeys = const [],
    Duration headerTimeout = const Duration(seconds: 30),
  }) async {
    if (!isOpen) throw const ControlTransientException('The control connection is closed.');
    final bytes = utf8.encode(jsonEncode(body));
    final ClientTransportStream stream;
    try {
      stream = _connection.makeRequest([
        Header.ascii(':method', 'POST'),
        Header.ascii(':scheme', 'https'),
        Header.ascii(':authority', endpoint.authority),
        Header.ascii(':path', path),
        Header.ascii('content-type', 'application/json'),
        Header.ascii('content-length', '${bytes.length}'),
        Header.ascii('accept-encoding', 'identity'),
        for (final key in lbKeys) Header.ascii('ts-lb', key),
      ]);
    } on TransportException {
      throw const ControlTransientException('The control connection is closed.');
    }
    stream.sendData(bytes, endStream: true);

    final status = Completer<int>();
    final bodyController = StreamController<List<int>>();
    var finished = false;
    late final StreamSubscription<StreamMessage> sub;
    void abort() {
      if (finished) return;
      finished = true;
      sub.cancel();
      try {
        stream.terminate();
      } catch (_) {}
      if (!bodyController.isClosed) bodyController.close();
    }

    sub = stream.incomingMessages.listen(
      (message) {
        if (message is HeadersStreamMessage) {
          if (!status.isCompleted) {
            for (final h in message.headers) {
              if (ascii.decode(h.name, allowInvalid: true) == ':status') {
                status.complete(int.tryParse(ascii.decode(h.value, allowInvalid: true)) ?? 0);
              }
            }
            if (!status.isCompleted) status.complete(0);
          }
        } else if (message is DataStreamMessage) {
          if (!bodyController.isClosed && message.bytes.isNotEmpty) bodyController.add(message.bytes);
        }
      },
      onError: (Object e) {
        final err = ControlTransientException('The control connection failed: ${_describe(e)}.');
        if (!status.isCompleted) {
          status.completeError(err);
        } else if (!bodyController.isClosed) {
          bodyController.addError(err);
        }
        finished = true;
        if (!bodyController.isClosed) bodyController.close();
      },
      onDone: () {
        if (!status.isCompleted) {
          status.completeError(const ControlTransientException('Control closed the request without a response.'));
        }
        finished = true;
        if (!bodyController.isClosed) bodyController.close();
      },
    );
    bodyController
      ..onPause = sub.pause
      ..onResume = sub.resume
      ..onCancel = abort;

    try {
      final code = await status.future.timeout(headerTimeout);
      return ControlResponse._(code, bodyController.stream, abort);
    } on TimeoutException {
      abort();
      throw const ControlTransientException('Timed out waiting for Tailscale control.', kind: ControlFailureKind.timeout);
    } catch (_) {
      abort();
      rethrow;
    }
  }

  /// Same as [post] for a long-poll whose headers may take [timeout].
  Future<ControlResponse> longPoll(String path, Object? body, {List<String> lbKeys = const [], required Duration timeout}) =>
      post(path, body, lbKeys: lbKeys, headerTimeout: timeout);

  Future<void> close() async {
    if (_closed) return;
    _closed = true;
    try {
      await _connection.terminate().timeout(const Duration(seconds: 1));
    } catch (_) {}
    _transport.destroy();
  }
}

/// Splits a /machine/map body into messages: little-endian uint32 length,
/// then that many bytes of (uncompressed) JSON.
Stream<Map<String, Object?>> decodeMapFrames(Stream<List<int>> body, {int maxMessage = 64 << 20}) async* {
  // Growable buffer: [start, end) holds unread bytes.
  var buffer = Uint8List(64 << 10);
  var start = 0;
  var end = 0;
  await for (final chunk in body) {
    if (end + chunk.length > buffer.length) {
      final live = end - start;
      var capacity = buffer.length;
      while (capacity < live + chunk.length) {
        capacity *= 2;
      }
      final grown = capacity == buffer.length ? buffer : Uint8List(capacity);
      grown.setRange(0, live, buffer, start);
      buffer = grown;
      start = 0;
      end = live;
    }
    buffer.setRange(end, end + chunk.length, chunk);
    end += chunk.length;
    while (end - start >= 4) {
      final size = ByteData.sublistView(buffer, start, start + 4).getUint32(0, Endian.little);
      if (size > maxMessage) throw const TailscaleControlException('Tailscale control sent an oversized netmap.');
      if (end - start - 4 < size) break;
      final message = Uint8List.sublistView(buffer, start + 4, start + 4 + size);
      start += 4 + size;
      final Object? decoded;
      try {
        decoded = jsonDecode(utf8.decode(message));
      } on FormatException {
        throw const TailscaleControlException(
            'Tailscale control sent a netmap this app cannot read (compressed or malformed).');
      }
      if (decoded is! Map) throw const TailscaleControlException('Tailscale control sent a malformed netmap.');
      yield decoded.cast<String, Object?>();
    }
  }
}

/// `key` field helper: raw 32 bytes of a `mkey:` text.
Uint8List parseMachinePublic(String text) => decodeKey(text, KeyPrefix.machinePublic, 32);
