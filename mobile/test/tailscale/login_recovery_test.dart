import 'dart:convert';
import 'dart:io';
import 'dart:typed_data';

import 'package:flutter_test/flutter_test.dart';
import 'package:tailsplit_mobile/src/tailscale/control_client.dart';
import 'package:tailsplit_mobile/src/tailscale/control_conn.dart';
import 'package:tailsplit_mobile/src/tailscale/identity.dart';
import 'package:tailsplit_mobile/src/tailscale/models.dart';

const _authUrl = 'https://login.example.test/a/synthetic-approval';
const _newAuthUrl = 'https://login.example.test/a/synthetic-renewal';

class _Response extends Fake implements ControlResponse {
  _Response(this.status, String text)
    : bytes = Uint8List.fromList(utf8.encode(text));
  _Response.json(Object json) : this(200, jsonEncode(json));
  _Response.map() : this.maps([
    jsonDecode(File('test/tailscale/fixtures/map_response.json').readAsStringSync()),
  ]);
  _Response.maps(List<Object?> maps) : status = 200 {
    final frames = BytesBuilder();
    for (final map in maps) {
      final json = utf8.encode(jsonEncode(map));
      frames.add((ByteData(4)..setUint32(0, json.length, Endian.little)).buffer.asUint8List());
      frames.add(json);
    }
    bytes = frames.takeBytes();
  }

  @override
  final int status;
  late final Uint8List bytes;

  @override
  Stream<List<int>> get body => Stream.value(bytes);

  @override
  Future<Uint8List> readAll({int maxBytes = 1 << 20}) async => bytes;

  @override
  void cancel() {}
}

class _Session extends Fake implements ControlSession {
  _Session(this.responses);
  final List<_Response> responses;
  final requests =
      <({String path, Map<String, Object?> body, List<String> lbKeys})>[];
  bool closed = false;

  @override
  bool get isOpen => !closed;

  @override
  Future<ControlResponse> post(
    String path,
    Object? body, {
    List<String> lbKeys = const [],
    Duration headerTimeout = const Duration(seconds: 30),
  }) async {
    requests.add((
      path: path,
      body: body! as Map<String, Object?>,
      lbKeys: List.of(lbKeys),
    ));
    if (responses.isEmpty) throw StateError('Unexpected extra control request');
    return responses.removeAt(0);
  }

  @override
  Future<void> close() async => closed = true;
}

class _Client extends TailscaleControlClient {
  _Client(this.session);
  final _Session session;
  final openedIdentities = <TailscaleIdentity>[];

  @override
  Future<ControlSession> openLoginSession(
    TailscaleIdentity identity,
    CancelToken cancel,
  ) async {
    cancel.throwIfCancelled();
    openedIdentities.add(identity);
    return session;
  }
}

TailscaleIdentity _identity() => TailscaleIdentity(
  machinePrivateKey: 'privkey:${'01' * 32}',
  nodePrivateKey: 'privkey:${'02' * 32}',
  hostname: 'login-recovery-test',
);

_Response _pending([String url = _authUrl]) => _Response.json({'AuthURL': url});
_Response _gone() => _Response(410, 'auth path not found');
_Response _approved() =>
    _Response.json({'AuthURL': '', 'MachineAuthorized': true});

Future<(List<LoginProgress>, Object?)> _login(
  _Client client,
  TailscaleIdentity identity,
) async {
  final events = <LoginProgress>[];
  Object? error;
  try {
    await for (final event
        in client.login(identity).timeout(const Duration(seconds: 3))) {
      events.add(event);
    }
  } catch (e) {
    error = e;
  } finally {
    await client.close();
  }
  return (events, error);
}

void _expectSameRegistrationKey(
  _Session session,
  TailscaleIdentity identity,
  List<String> followups,
) {
  final registrations = session.requests
      .where((r) => r.path == '/machine/register')
      .toList();
  expect(registrations.map((r) => r.body['Followup']), followups);
  for (final request in registrations) {
    expect(request.body['NodeKey'], identity.nodePublicKey);
    expect(request.body['OldNodeKey'], 'nodekey:${'00' * 32}');
    expect(request.lbKeys, [identity.nodePublicKey]);
  }
}

void main() {
  test('login discovers changed peers in the first map after keep-alives', () async {
    final map = jsonDecode(File('test/tailscale/fixtures/map_response.json').readAsStringSync()) as Map<String, Object?>;
    map['PeersChanged'] = map.remove('Peers');
    final session = _Session([
      _approved(),
      _Response.maps([
        {'KeepAlive': true, 'ControlTime': '2026-11-11T00:00:00Z'},
        map,
      ]),
    ]);
    final (events, error) = await _login(_Client(session), _identity());

    expect(error, isNull);
    final snapshot = (events.single as LoginAuthorized).snapshot;
    expect(snapshot.exitNodes, hasLength(5));
    expect(snapshot.discoveryDiagnostics!.peerCount, 7);
    expect(snapshot.discoveryDiagnostics!.changedPeerCount, 7);
    expect(snapshot.discoveryDiagnostics!.peerListPresent, isFalse);
    expect(session.requests.map((r) => r.path), ['/machine/register', '/machine/map']);
    expect(session.requests.last.body['Stream'], isTrue);
    expect(session.closed, isTrue);
  });

  test('netmap rejection preserves redacted server detail without authorizing', () async {
    final key = 'privkey:${'aa' * 32}';
    final session = _Session([
      _pending(),
      _approved(),
      _Response(403, 'device approval required at $_authUrl $key'),
    ]);
    final (events, error) = await _login(_Client(session), _identity());
    expect(events.whereType<LoginAuthorized>(), isEmpty);
    expect(error, isA<TailscaleNeedsLogin>());
    expect(error.toString(), contains('netmap request (HTTP 403: device approval required'));
    expect(error.toString(), contains('<url>'));
    expect(error.toString(), contains('<key>'));
    expect(error.toString(), isNot(contains(_authUrl)));
    expect(error.toString(), isNot(contains(key)));
  });

  test('browser approval followed by 410 re-registers the same device and fetches its map', () async {
    final identity = _identity();
    final session = _Session([
      _pending(),
      _gone(),
      _approved(),
      _Response.map(),
    ]);
    final client = _Client(session);

    final (events, error) = await _login(client, identity);

    expect(error, isNull);
    expect(events, hasLength(2));
    expect(events.first, isA<LoginNeedsBrowser>());
    final authorized = events.last as LoginAuthorized;
    expect(authorized.identity.machinePrivateKey, identity.machinePrivateKey);
    expect(authorized.identity.nodePrivateKey, identity.nodePrivateKey);
    expect(authorized.snapshot.stableNodeId, 'nSELF1001CNTRL');
    expect(
      client.openedIdentities.single.machinePrivateKey,
      identity.machinePrivateKey,
    );
    _expectSameRegistrationKey(session, identity, ['', _authUrl, '']);
    expect(session.requests.last.path, '/machine/map');
    expect(session.requests.last.body['NodeKey'], identity.nodePublicKey);
    expect(session.responses, isEmpty);
    expect(session.closed, isTrue);
  });

  test('an expired link requires approval at the replacement link', () async {
    final identity = _identity();
    final session = _Session([
      _pending(),
      _gone(),
      _pending(_newAuthUrl),
      _approved(),
      _Response.map(),
    ]);
    final (events, error) = await _login(_Client(session), identity);

    expect(error, isNull);
    expect(
      events.whereType<LoginNeedsBrowser>().map((e) => e.authUrl.toString()),
      [_authUrl, _newAuthUrl],
    );
    expect(events.last, isA<LoginAuthorized>());
    _expectSameRegistrationKey(session, identity, [
      '',
      _authUrl,
      '',
      _newAuthUrl,
    ]);
  });

  test(
    'another gone followup stops instead of repeatedly opening sign-in links',
    () async {
      final identity = _identity();
      final session = _Session([
        _pending(),
        _gone(),
        _pending(_newAuthUrl),
        _gone(),
      ]);
      final (events, error) = await _login(_Client(session), identity);

      expect(error, isA<TailscaleControlException>());
      expect(events.whereType<LoginAuthorized>(), isEmpty);
      _expectSameRegistrationKey(session, identity, [
        '',
        _authUrl,
        '',
        _newAuthUrl,
      ]);
      expect(session.requests, hasLength(4));
    },
  );

  test('fresh registration returning the same gone link stops without polling it again', () async {
    final identity = _identity();
    final session = _Session([_pending(), _gone(), _pending()]);
    final (events, error) = await _login(_Client(session), identity);

    expect(error, isA<TailscaleControlException>());
    expect(events.whereType<LoginAuthorized>(), isEmpty);
    _expectSameRegistrationKey(session, identity, ['', _authUrl, '']);
  });

  for (final rejection in <String, _Response Function()>{
    'HTTP rejection': () =>
        _Response(403, 'denied $_authUrl nodekey:${'03' * 32}'),
    'registration Error': () =>
        _Response.json({'Error': 'denied $_authUrl nodekey:${'03' * 32}'}),
    'expired node key': () => _Response.json({'NodeKeyExpired': true}),
    'another HTTP 410': _gone,
  }.entries) {
    test(
      'fresh registration ${rejection.key} never authorizes the device',
      () async {
        final identity = _identity();
        final session = _Session([_pending(), _gone(), rejection.value()]);
        final (events, error) = await _login(_Client(session), identity);

        expect(error, isA<TailscaleControlException>());
        expect(events.whereType<LoginAuthorized>(), isEmpty);
        _expectSameRegistrationKey(session, identity, ['', _authUrl, '']);
        expect(session.requests, hasLength(3));
        expect(error.toString(), isNot(contains(_authUrl)));
        expect(error.toString(), isNot(contains('03' * 32)));
      },
    );
  }

  test(
    'HTTP 410 on the initial registration does not trigger followup recovery',
    () async {
      final session = _Session([_gone()]);
      final (events, error) = await _login(_Client(session), _identity());

      expect(error, isA<TailscaleControlException>());
      expect(events, isEmpty);
      expect(session.requests, hasLength(1));
      expect(error.toString(), contains('HTTP 410'));
    },
  );

  test('other followup rejections do not trigger fresh registration', () async {
    final session = _Session([_pending(), _Response(403, 'sign-in denied')]);
    final (events, error) = await _login(_Client(session), _identity());

    expect(error, isA<TailscaleControlException>());
    expect(events.whereType<LoginAuthorized>(), isEmpty);
    expect(session.requests, hasLength(2));
    expect(error.toString(), contains('HTTP 403'));
  });
}
