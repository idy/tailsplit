import 'dart:async';
import 'dart:convert';
import 'dart:io';
import 'dart:typed_data';

import 'control_conn.dart';
import 'identity.dart';
import 'keys.dart';
import 'models.dart';
import 'noise.dart';
import 'tailcfg.dart';

/// A minimal Tailscale control-plane client (register, read-only netmap,
/// logout) speaking ts2021 like `controlclient.Direct` of the tailscale fork
/// pinned by sing-box 1.14.4. It never needs a VPN/TUN.
class TailscaleControlClient {
  TailscaleControlClient({
    Uri? controlUrl,
    this.os = 'iOS',
    this.osVersion,
    this.deviceModel,
  }) : controlUrl = _normalize(controlUrl ?? defaultControlUrl);

  static final Uri defaultControlUrl = Uri.parse('https://controlplane.tailscale.com');

  final Uri controlUrl;

  /// `Hostinfo.OS`: `iOS` or `android` (version.OS values).
  final String os;
  final String? osVersion;
  final String? deviceModel;

  late final ControlEndpoint _endpoint = ControlEndpoint(controlUrl);
  final String _backendLogId = hexEncode(randomBytes(32));
  final Set<CancelToken> _active = {};
  HttpClient? _http;
  Future<Uint8List>? _controlKey;
  bool _closed = false;

  static const _requestTimeout = Duration(seconds: 30);
  static const _mapTimeout = Duration(seconds: 60);
  static const _longPollTimeout = Duration(minutes: 10);
  static const _maxBackoff = Duration(seconds: 30);

  /// Registers [identity]'s node key. Emits [LoginNeedsBrowser] while the
  /// user has to approve the device, then one [LoginAuthorized] and closes.
  /// Single-subscription; cancelling the subscription closes all sockets.
  Stream<LoginProgress> login(TailscaleIdentity identity) {
    final cancel = CancelToken();
    late final StreamController<LoginProgress> out;
    out = StreamController<LoginProgress>(
      onListen: () {
        if (_closed) {
          out
            ..addError(const TailscaleControlException('The Tailscale client is closed.'))
            ..close();
          return;
        }
        _active.add(cancel);
        _runLogin(identity, out, cancel).then(
          (_) {},
          onError: (Object e, StackTrace st) {
            if (!cancel.isCancelled && !out.isClosed) out.addError(_publicError(e), st);
          },
        ).whenComplete(() {
          _active.remove(cancel);
          if (!out.isClosed) out.close();
        });
      },
      onCancel: cancel.cancel,
    );
    return out.stream;
  }

  /// One read-only netmap fetch for an already signed-in node key.
  ///
  /// Uses a streaming MapRequest (`Stream: true`, capability version 142),
  /// which control treats as read-only, reads the first full MapResponse and
  /// closes the stream. Unlike a non-streaming ("lite"/update) request it
  /// does not replace the endpoints, home DERP, Hostinfo or disco key that a
  /// concurrently running sing-box session with the same node key reported.
  /// Throws [TailscaleNeedsLogin] if control does not accept the node key or
  /// the key has expired.
  Future<TailnetSnapshot> fetchSnapshot(TailscaleIdentity identity) => _guard((cancel) async {
        final key = await _getControlKey();
        final session = await _retrying(cancel, () => _open(identity, key, cancel));
        final unregister = cancel.onCancel(session.close);
        try {
          final parsed = await _fetchMap(session, identity);
          if (parsed.selfStatus.keyExpired) {
            throw const TailscaleNeedsLogin('This device’s Tailscale key has expired; sign in again.');
          }
          return parsed.snapshot;
        } finally {
          unregister();
          await session.close();
        }
      });

  /// Expires the node key at control (what `tailscale logout` does). Best
  /// effort: throws [TailscaleControlException] if control cannot be reached.
  Future<void> logout(TailscaleIdentity identity) => _guard((cancel) async {
        final key = await _getControlKey();
        final session = await _retrying(cancel, () => _open(identity, key, cancel));
        final unregister = cancel.onCancel(session.close);
        try {
          final response = await _register(session, identity,
              nodePrivateKey: identity.nodePrivateKey, expiry: goLogoutExpiry);
          if (response.error.isNotEmpty) {
            throw TailscaleControlException('Tailscale control refused to sign out: ${redact(response.error)}', kind: ControlFailureKind.rejected);
          }
        } finally {
          unregister();
          await session.close();
        }
      });

  /// Cancels everything in flight and releases sockets.
  Future<void> close() async {
    _closed = true;
    for (final token in List.of(_active)) {
      token.cancel();
    }
    _active.clear();
    _http?.close(force: true);
    _http = null;
  }

  // ── Login ────────────────────────────────────────────────────────────

  Future<void> _runLogin(
    TailscaleIdentity identity,
    StreamController<LoginProgress> out,
    CancelToken cancel,
  ) async {
    final controlKey = await _getControlKey();
    cancel.throwIfCancelled();
    ControlSession? session;
    final unregister = cancel.onCancel(() => session?.close());
    Future<ControlSession> current(TailscaleIdentity id) async {
      final s = session;
      if (s != null && s.isOpen) return s;
      await s?.close();
      return session = await _open(id, controlKey, cancel);
    }

    Future<void> dropSession() async {
      final s = session;
      session = null;
      await s?.close();
    }

    try {
      var nodePrivate = identity.nodePrivateKey;
      String? oldNodePublic;
      var regenerated = false;
      RegisterResponse response;
      while (true) {
        final attempt = identity.copyWith(nodePrivateKey: nodePrivate);
        response = await _retrying(
          cancel,
          () async => _register(await current(attempt), attempt,
              nodePrivateKey: nodePrivate, oldNodePublicKey: oldNodePublic),
          onTransient: dropSession,
        );
        _checkRegister(response);
        if (!response.nodeKeyExpired) break;
        // Same as Direct.doLogin: an expired key must be replaced, telling
        // control the old one so the device keeps its identity.
        if (regenerated) {
          throw const TailscaleControlException('Tailscale control rejected the replacement node key.');
        }
        oldNodePublic = publicKeyText(nodePrivate, KeyPrefix.nodePublic);
        nodePrivate = encodeKey(KeyPrefix.privateKey, newCurve25519PrivateKey());
        regenerated = true;
      }

      final authorized = identity.copyWith(nodePrivateKey: nodePrivate);
      var url = response.authUrl;
      String? shown;
      var repeats = 0;
      while (url.isNotEmpty) {
        if (url != shown) {
          final uri = Uri.tryParse(url);
          if (uri == null || !(uri.isScheme('https') || uri.isScheme('http'))) {
            throw const TailscaleControlException('Tailscale control sent an invalid sign-in link.');
          }
          cancel.throwIfCancelled();
          out.add(LoginNeedsBrowser(uri));
          shown = url;
        }
        final followup = url;
        // Long-poll: control answers once the user finished in the browser.
        response = await _retrying(
          cancel,
          () async => _register(await current(authorized), authorized,
              nodePrivateKey: nodePrivate,
              oldNodePublicKey: oldNodePublic,
              followup: followup,
              timeout: _longPollTimeout),
          forever: true,
          onTransient: dropSession,
        );
        _checkRegister(response);
        if (response.nodeKeyExpired) {
          throw const TailscaleControlException('The sign-in expired; start again.');
        }
        if (response.authUrl == url) {
          repeats++;
          await _sleep(_backoff(repeats), cancel);
        } else {
          repeats = 0;
        }
        url = response.authUrl;
      }

      final parsed = await _retrying(
        cancel,
        () async => _fetchMap(await current(authorized), authorized, register: response),
        onTransient: dropSession,
      );
      cancel.throwIfCancelled();
      out.add(LoginAuthorized(parsed.snapshot, identity: authorized));
    } finally {
      unregister();
      await session?.close();
    }
  }

  void _checkRegister(RegisterResponse response) {
    if (response.error.isNotEmpty) {
      throw TailscaleControlException('Tailscale control refused the sign-in: ${redact(response.error)}', kind: ControlFailureKind.rejected);
    }
    if (response.hasNodeKeySignature) {
      throw const TailscaleControlException(
          'This tailnet uses Tailnet Lock key rotation, which this app does not support yet.');
    }
  }

  // ── Requests ─────────────────────────────────────────────────────────

  HostinfoFields _hostinfo(TailscaleIdentity identity) => HostinfoFields(
        hostname: identity.hostname,
        os: os,
        backendLogId: _backendLogId,
        osVersion: osVersion,
        deviceModel: deviceModel,
      );

  Future<ControlSession> _open(TailscaleIdentity identity, Uint8List controlKey, CancelToken cancel) {
    cancel.throwIfCancelled();
    return ControlSession.open(
      endpoint: _endpoint,
      machinePrivate: parsePrivateKey(identity.machinePrivateKey),
      controlPublic: controlKey,
      protocolVersion: currentCapabilityVersion,
      cancel: cancel,
    );
  }

  Future<RegisterResponse> _register(
    ControlSession session,
    TailscaleIdentity identity, {
    required String nodePrivateKey,
    String? oldNodePublicKey,
    String followup = '',
    String expiry = goZeroTime,
    Duration timeout = _requestTimeout,
  }) async {
    final nodePublic = publicKeyText(nodePrivateKey, KeyPrefix.nodePublic);
    final response = await session.post(
      '/machine/register',
      registerRequestJson(
        nodeKey: nodePublic,
        oldNodeKey: oldNodePublicKey,
        nlKey: identity.networkLockPublicKey,
        hostinfo: _hostinfo(identity),
        followup: followup,
        expiry: expiry,
      ),
      lbKeys: [?oldNodePublicKey, nodePublic],
      headerTimeout: timeout,
    );
    final body = await _readBody(response, _requestTimeout);
    _checkStatus(response.status, body, what: 'sign-in request');
    final Object? json;
    try {
      json = jsonDecode(utf8.decode(body));
    } on FormatException {
      throw const TailscaleControlException('Tailscale control sent an unreadable sign-in response.');
    }
    if (json is! Map) throw const TailscaleControlException('Tailscale control sent an unexpected sign-in response.');
    return RegisterResponse.fromJson(json.cast<String, Object?>());
  }

  Future<ParsedMapResponse> _fetchMap(
    ControlSession session,
    TailscaleIdentity identity, {
    RegisterResponse? register,
  }) async {
    final nodePublic = identity.nodePublicKey;
    final response = await session.post(
      '/machine/map',
      mapRequestJson(nodeKey: nodePublic, hostinfo: _hostinfo(identity)),
      lbKeys: [nodePublic],
      headerTimeout: _mapTimeout,
    );
    if (response.status != 200) {
      final body = await _readBody(response, const Duration(seconds: 5), maxBytes: 4096)
          .catchError((Object _) => Uint8List(0));
      if (response.status == 401 || response.status == 403 || response.status == 404) {
        throw TailscaleNeedsLogin(
            'Tailscale control does not accept this device’s key (HTTP ${response.status}); sign in again.');
      }
      _checkStatus(response.status, body, what: 'netmap request');
    }
    Future<ParsedMapResponse> read() async {
      await for (final message in decodeMapFrames(response.body)) {
        if (isKeepAlive(message)) continue;
        try {
          return parseMapResponse(
            message,
            fallbackLoginName: register?.loginName ?? '',
            fallbackDisplayName: (register?.userDisplayName.isNotEmpty ?? false)
                ? register!.userDisplayName
                : (register?.loginDisplayName ?? ''),
            fallbackUserId: register?.userId ?? 0,
          );
        } on MapResponseFormatException catch (e) {
          throw TailscaleControlException('Tailscale control sent an unexpected netmap (${e.message}).');
        }
      }
      throw const ControlTransientException('Tailscale control closed the netmap stream early.');
    }

    try {
      return await read().timeout(_mapTimeout);
    } on TimeoutException {
      throw const ControlTransientException('Timed out waiting for the netmap.', kind: ControlFailureKind.timeout);
    } finally {
      // We only need the first full map; end the long poll.
      response.cancel();
    }
  }

  Future<Uint8List> _readBody(ControlResponse response, Duration timeout, {int maxBytes = 1 << 20}) async {
    try {
      return await response.readAll(maxBytes: maxBytes).timeout(timeout);
    } on TimeoutException {
      response.cancel();
      throw const ControlTransientException('Timed out reading the control response.', kind: ControlFailureKind.timeout);
    }
  }

  void _checkStatus(int status, Uint8List body, {required String what}) {
    if (status == 200) return;
    final text = redact(utf8.decode(body, allowMalformed: true));
    final detail = text.isEmpty ? 'HTTP $status' : 'HTTP $status: $text';
    if (status == 429) {
      throw ControlTransientException('Tailscale control is rate limiting this device ($detail).',
          kind: ControlFailureKind.rateLimited, retryAfter: const Duration(seconds: 10));
    }
    if (status >= 500) throw ControlTransientException('Tailscale control failed the $what ($detail).');
    throw TailscaleControlException('Tailscale control refused the $what ($detail).', kind: ControlFailureKind.rejected);
  }

  Future<Uint8List> _getControlKey() {
    final cached = _controlKey;
    if (cached != null) return cached;
    final future = _loadControlKey();
    _controlKey = future;
    future.then((_) {}, onError: (Object _) {
      if (identical(_controlKey, future)) _controlKey = null;
    });
    return future;
  }

  /// GET /key?v=142 over verified TLS (`loadServerPubKeys`).
  Future<Uint8List> _loadControlKey() async {
    final http = _http ??= HttpClient()
      ..connectionTimeout = const Duration(seconds: 15)
      ..userAgent = 'tailsplit';
    final path = controlUrl.path.endsWith('/') ? controlUrl.path : '${controlUrl.path}/';
    final uri = controlUrl.replace(path: '${path}key', query: 'v=$currentCapabilityVersion');
    try {
      final request = await http.getUrl(uri).timeout(_requestTimeout);
      final response = await request.close().timeout(_requestTimeout);
      final body = BytesBuilder(copy: false);
      await for (final chunk in response.timeout(_requestTimeout)) {
        body.add(chunk);
        if (body.length > 64 << 10) throw const TailscaleControlException('Tailscale control sent an oversized key.');
      }
      if (response.statusCode != 200) {
        throw ControlTransientException('Fetching the Tailscale control key failed (HTTP ${response.statusCode}).');
      }
      final json = jsonDecode(utf8.decode(body.takeBytes()));
      final public = json is Map ? json['publicKey'] : null;
      if (public is! String) {
        throw const TailscaleControlException('Tailscale control is too old: it has no Noise key.');
      }
      final raw = parseMachinePublic(public);
      if (isZeroKey(raw)) throw const TailscaleControlException('Tailscale control is too old: it has no Noise key.');
      return raw;
    } on SocketException catch (e) {
      throw ControlTransientException('Cannot reach Tailscale control: ${e.osError?.message ?? e.message}.', kind: ControlFailureKind.offline);
    } on HandshakeException {
      throw const ControlTransientException('Cannot verify Tailscale control’s TLS certificate.', kind: ControlFailureKind.tls);
    } on HttpException {
      throw const ControlTransientException('Tailscale control closed the connection.');
    } on TimeoutException {
      throw const ControlTransientException('Timed out reaching Tailscale control.', kind: ControlFailureKind.timeout);
    } on FormatException {
      throw const TailscaleControlException('Tailscale control returned an unreadable key.');
    }
  }

  // ── Plumbing ─────────────────────────────────────────────────────────

  Future<T> _guard<T>(Future<T> Function(CancelToken cancel) body) async {
    if (_closed) throw const TailscaleControlException('The Tailscale client is closed.');
    final cancel = CancelToken();
    _active.add(cancel);
    try {
      return await body(cancel);
    } catch (e) {
      throw _publicError(e);
    } finally {
      _active.remove(cancel);
    }
  }

  Future<T> _retrying<T>(
    CancelToken cancel,
    Future<T> Function() op, {
    bool forever = false,
    Future<void> Function()? onTransient,
  }) async {
    var attempt = 0;
    while (true) {
      cancel.throwIfCancelled();
      try {
        return await op();
      } on ControlTransientException catch (e) {
        if (cancel.isCancelled) throw const ControlCancelled();
        attempt++;
        if (!forever && attempt >= 3) rethrow;
        await onTransient?.call();
        await _sleep(e.retryAfter ?? _backoff(attempt), cancel);
      }
    }
  }

  static Duration _backoff(int attempt) {
    final seconds = 1 << (attempt - 1).clamp(0, 5);
    final d = Duration(seconds: seconds);
    return d > _maxBackoff ? _maxBackoff : d;
  }

  static Future<void> _sleep(Duration d, CancelToken cancel) {
    final done = Completer<void>();
    late final void Function() unregister;
    final timer = Timer(d, () {
      unregister();
      if (!done.isCompleted) done.complete();
    });
    unregister = cancel.onCancel(() {
      timer.cancel();
      if (!done.isCompleted) done.completeError(const ControlCancelled());
    });
    return done.future;
  }

  Object _publicError(Object e) {
    if (e is TailscaleControlException) return e;
    if (e is ControlCancelled) return const TailscaleControlException('Cancelled.', kind: ControlFailureKind.cancelled);
    if (e is NoiseException) {
      return TailscaleControlException('The secure connection to Tailscale control failed (${e.message}).', kind: ControlFailureKind.tls);
    }
    if (e is SocketException) {
      return TailscaleControlException('Cannot reach Tailscale control: ${e.osError?.message ?? e.message}.', kind: ControlFailureKind.offline);
    }
    if (e is FormatException) return const TailscaleControlException('Tailscale control sent an unexpected response.');
    return TailscaleControlException('Tailscale control request failed (${e.runtimeType}).');
  }

  static Uri _normalize(Uri url) => Uri(
        scheme: url.scheme,
        host: url.host,
        port: url.hasPort ? url.port : null,
        path: url.path.replaceAll(RegExp(r'/+$'), ''),
      );
}
