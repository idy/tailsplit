import 'dart:async';
import 'dart:collection';
import 'dart:io';

import 'package:flutter/foundation.dart';
import 'package:flutter/widgets.dart' show AppLifecycleState, WidgetsBinding, WidgetsBindingObserver;
import 'package:url_launcher/url_launcher.dart';

import '../model/exit_profile.dart';
import '../model/settings.dart';
import '../services/config_builder.dart';
import '../services/errors.dart';
import '../services/rules.dart';
import '../services/storage.dart';
import '../services/vpn.dart';
import '../tailscale/tailscale.dart';
import 'preview.dart';

/// Build with `--dart-define=TAILSPLIT_PREVIEW=true` to review the design with
/// simulated exits. Preview builds never contact Tailscale or start a VPN;
/// review captions disclose simulated data without adding UI chrome.
const previewMode = bool.fromEnvironment('TAILSPLIT_PREVIEW');

enum SignInPhase { idle, starting, browser, finishing }

/// What a connection attempt is doing before the VPN reports progress.
enum ConnectStep { checking, rules, starting }

/// Problems the UI explains in place. Messages never contain keys or auth URLs.
enum AppProblem {
  signIn,
  refresh,
  rules,
  permission,
  connect,
  disconnect,
  tunnelStopped,
  needsSignIn,
  nodeOffline,
  nodeMissing,
  awaitingApproval,
  storage,
}

class LogLine {
  LogLine(this.level, String message, {this.source = 'app'})
      : time = DateTime.now(),
        message = sanitize(message);

  final DateTime time;
  final String level;
  final String message;
  final String source;

  static final _ansi = RegExp(r'\x1B\[[0-9;?]*[ -/]*[@-~]|\x1B[@-Z\\-_]');
  static final _control = RegExp(r'[\x00-\x08\x0B-\x1F\x7F]');
  static const maxLength = 4000;

  /// Terminal escapes and control characters are discarded; very long lines
  /// are shortened so one runaway entry cannot stall the log view.
  static String sanitize(String text) {
    final clean = text.replaceAll(_ansi, '').replaceAll(_control, '');
    return clean.length <= maxLength ? clean : '${clean.substring(0, maxLength)}…';
  }
}

/// Diagnostic lines, kept apart from app state so a busy core log only
/// rebuilds the Logs page, at most every [_interval].
class LogBuffer extends ChangeNotifier {
  static const maxLines = 2000;
  static const _interval = Duration(milliseconds: 120);

  final _lines = ListQueue<LogLine>();
  late final lines = UnmodifiableListView(_lines);
  Timer? _pending;

  void add(LogLine line) {
    _lines.addLast(line);
    if (_lines.length > maxLines) _lines.removeFirst();
    _pending ??= Timer(_interval, () {
      _pending = null;
      notifyListeners();
    });
  }

  void addAll(Iterable<LogLine> lines) {
    for (final line in lines) {
      add(line);
    }
  }

  void clear() {
    _lines.clear();
    _pending?.cancel();
    _pending = null;
    notifyListeners();
  }

  @override
  void dispose() {
    _pending?.cancel();
    super.dispose();
  }
}

/// Owns the single exit, the routing settings and the VPN lifecycle.
class AppController extends ChangeNotifier with WidgetsBindingObserver {
  AppController({
    required AppPaths paths,
    VpnService? vpn,
    TailscaleControlClient Function()? controlClient,
    Future<String> Function(Uri uri)? ruleDownload,
    Duration nativeCommandTimeout = const Duration(seconds: 15),
  })  : _nativeCommandTimeout = nativeCommandTimeout, // ignore: prefer_initializing_formals
        _paths = paths, // ignore: prefer_initializing_formals
        _vpn = vpn ?? VpnService(),
        _ruleDownload = ruleDownload, // ignore: prefer_initializing_formals
        _newClient = controlClient ?? (() => TailscaleControlClient(os: Platform.isIOS ? 'iOS' : 'android'));

  final AppPaths _paths;
  final Duration _nativeCommandTimeout;
  final VpnService _vpn;
  final TailscaleControlClient Function() _newClient;
  final Future<String> Function(Uri uri)? _ruleDownload;

  /// A saved snapshot younger than this is trusted for the connect preflight.
  static const freshSnapshot = Duration(minutes: 1);

  /// Reopening the app refreshes exit nodes older than this.
  static const staleSnapshot = Duration(minutes: 5);

  MobileSettings settings = const MobileSettings();
  ExitProfile? exit;
  VpnConnectionState vpnState = VpnConnectionState.disconnected;
  SignInPhase signIn = SignInPhase.idle;
  ConnectStep? connectStep;
  bool get preparing => connectStep != null;
  bool refreshing = false;
  bool removing = false;
  bool needsReconnect = false;
  String coreVersion = '';

  AppProblem? problem;
  FailureCause? problemCause;

  /// English diagnostic detail (also written to Logs).
  String? problemDetail;

  /// The node or item a problem is about, shown in its title.
  String? problemSubject;

  DateTime? rulesUpdatedAt;
  bool rulesUpdating = false;
  FailureCause? rulesUpdateError;

  /// Set after a successful sign-in so the UI opens the node picker once.
  bool pickerRequested = false;

  /// Preview builds only: the state requested for simulator review.
  String previewScreen = '';

  final logBuffer = LogBuffer();
  List<LogLine> get logs => logBuffer.lines;

  Uri? _authUrl; // Sensitive: memory only, never logged or persisted.
  StreamSubscription<LoginProgress>? _login;
  TailscaleControlClient? _loginClient;
  StreamSubscription<VpnStatus>? _statusSub;
  StreamSubscription<SingBoxLog>? _logSub;
  int _connectAttempt = 0;
  int _signInAttempt = 0;
  bool _observing = false;
  bool _disposed = false;
  Future<void> _nativeOperations = Future<void>.value();
  Future<bool>? _stopRequest;
  bool _nativeStartPending = false;
  // Independent of displayed status and dismissible errors: only a confirmed
  // native stop, after every pending start settles, can release this latch.
  bool _shutdownUnconfirmed = false;
  int _nativeStopsPending = 0;
  bool _reconnecting = false;
  RoutingMode? _runningMode;
  bool _tunnelObservedActive = false;

  bool get isPreview => previewMode;
  bool get connected => vpnState == VpnConnectionState.connected;
  bool get active =>
      vpnState == VpnConnectionState.connected ||
      vpnState == VpnConnectionState.connecting;
  bool get transitioning =>
      preparing || _stopRequest != null ||
      vpnState == VpnConnectionState.connecting ||
      vpnState == VpnConnectionState.disconnecting;
  bool get busy => removing || signIn != SignInPhase.idle;
  bool get canConnect => exit?.hasNode == true && !busy &&
      !_shutdownUnconfirmed && !_nativeStartPending && _stopRequest == null;
  bool get canRetryDisconnect => !busy && _stopRequest == null;
  bool get hasAuthUrl => _authUrl != null || (isPreview && signIn == SignInPhase.browser);

  /// The switch reads on while preparing or connected.
  bool get switchOn => preparing || active;

  /// Turning the switch off also cancels an attempt still preparing.
  bool get canToggleConnection {
    if (busy || _stopRequest != null || vpnState == VpnConnectionState.disconnecting) return false;
    return switchOn || canConnect;
  }

  /// Automatic applies must keep Global protection active. A tunnel restored
  /// from the OS has an unknown runtime mode and takes the same safe path.
  bool get reconnectRequiresConsent => _runningMode != RoutingMode.split &&
      (vpnState != VpnConnectionState.disconnected || _nativeStartPending);

  String? get _ruleUrl => switch (settings.source) {
        RuleSource.builtin => RuleLists.defaultUrl,
        RuleSource.custom => settings.customSourceUrl.isEmpty ? null : settings.customSourceUrl,
        RuleSource.none => null,
      };

  @override
  void notifyListeners() {
    if (!_disposed) super.notifyListeners();
  }

  // ── Lifecycle ──────────────────────────────────────────────────────────

  Future<void> load() async {
    if (isPreview) return _loadPreview();
    try {
      final saved = await JsonStore.read(_paths.settingsFile);
      if (saved != null) settings = MobileSettings.fromJson(saved);
    } catch (error) {
      _log('warn', 'Saved settings could not be read; defaults are used (${describeFailure(error)}).');
    }
    try {
      final saved = await JsonStore.read(_paths.exitFile);
      if (saved != null) exit = ExitProfile.fromJson(saved);
    } catch (error) {
      _log('warn', 'The saved exit could not be read and was ignored (${describeFailure(error)}).');
    }
    await _loadRuleInfo();
    notifyListeners();
    try {
      await _vpn.initialize();
      _statusSub = _vpn.status.listen(_onStatus, onError: (Object error) {
        _log('warn', 'VPN status unavailable: ${describeFailure(error)}');
      });
      _logSub = _vpn.logs.listen(
        (entry) => _log(entry.level, entry.message, source: 'core'),
        onError: (Object _) {},
      );
      _onStatus(await _vpn.currentStatus());
      coreVersion = await _vpn.coreVersion();
    } catch (error) {
      _log('warn', 'VPN service unavailable: ${describeFailure(error)}');
    }
    WidgetsBinding.instance.addObserver(this);
    _observing = true;
    notifyListeners();
    // A saved netmap can be days old; update it quietly while idle.
    if (exit != null && !active) unawaited(refresh(silent: true));
  }

  Future<void> _loadPreview() async {
    // Preview-only launch options work with Flutter tooling on either OS,
    // with simulator environment variables as an optional fallback.
    const screenOption = String.fromEnvironment('TAILSPLIT_PREVIEW_SCREEN');
    const languageOption = String.fromEnvironment('TAILSPLIT_PREVIEW_LANG');
    final screen = screenOption.isNotEmpty
        ? screenOption
        : Platform.environment['TAILSPLIT_PREVIEW_SCREEN'] ?? '';
    final language = languageOption.isNotEmpty
        ? languageOption
        : Platform.environment['TAILSPLIT_PREVIEW_LANG'];
    final seeded = await PreviewData.seed(withNode: screen != 'nonode');
    settings = seeded.settings.copyWith(
      mode: screen == 'global' || screen == 'tunnel-stopped' ? RoutingMode.global : null,
      language: switch (language) {
        'zh' => AppLanguage.zh,
        'en' => AppLanguage.en,
        _ => null,
      },
    );
    exit = screen == 'empty' || screen == 'signin' ? null : seeded.exit;
    coreVersion = '1.14.4';
    rulesUpdatedAt = DateTime.now().subtract(const Duration(days: 2));
    logBuffer.addAll(PreviewData.logs());
    previewScreen = screen;
    switch (screen) {
      case 'connected' || 'global':
        vpnState = VpnConnectionState.connected;
        _runningMode = settings.mode;
      case 'reconnect':
        vpnState = VpnConnectionState.connected;
        _runningMode = settings.mode;
        needsReconnect = true;
      case 'signin':
        signIn = SignInPhase.browser;
      case 'connecting':
        connectStep = ConnectStep.rules;
      case 'tunnel-stopped':
        _setProblem(AppProblem.tunnelStopped);
      case 'error':
        _setProblem(AppProblem.connect, detail: 'The exit node did not respond within 45 seconds.');
      case 'offline':
        _setProblem(AppProblem.nodeOffline, subject: seeded.exit.nodeName);
      case 'rules':
        _setProblem(
          AppProblem.rules,
          error: const RuleListException('connection reset', cause: FailureCause.offline),
        );
        rulesUpdatedAt = null;
      case 'nonode':
        pickerRequested = true;
    }
    notifyListeners();
  }

  @override
  void dispose() {
    _disposed = true;
    if (_observing) WidgetsBinding.instance.removeObserver(this);
    _statusSub?.cancel();
    _logSub?.cancel();
    _login?.cancel();
    _loginClient?.close();
    logBuffer.dispose();
    super.dispose();
  }

  @override
  void didChangeAppLifecycleState(AppLifecycleState state) {
    if (state == AppLifecycleState.resumed) unawaited(_resume());
  }

  /// The plugin's status stream can miss changes while the UI is suspended,
  /// and the system can stop the VPN at any time; re-read both on return.
  Future<void> _resume() async {
    if (isPreview) return;
    try {
      _onStatus(await _vpn.currentStatus());
    } catch (_) {
      // The next status event corrects the display.
    }
    final current = exit;
    if (current == null || active || preparing || busy) return;
    final fetched = current.snapshot?.fetchedAt;
    if (fetched == null || DateTime.now().difference(fetched) > staleSnapshot) {
      unawaited(refresh(silent: true));
    }
  }

  void _onStatus(VpnStatus status) {
    final previous = vpnState;
    final stopped = status.state == VpnConnectionState.disconnected ||
        status.state == VpnConnectionState.error;
    final unexpectedStop = stopped && _tunnelObservedActive &&
        _stopRequest == null && !_shutdownUnconfirmed;
    if (status.state == VpnConnectionState.connected) _tunnelObservedActive = true;
    if (stopped) _tunnelObservedActive = false;
    vpnState = status.state;
    switch (status.state) {
      case VpnConnectionState.error:
        if (previous != VpnConnectionState.error) {
          _setProblem(AppProblem.connect, detail: status.message);
          _log('error', 'Connection failed${status.message == null ? '' : ': ${status.message}'}');
        }
      case VpnConnectionState.connected:
        if (problem == AppProblem.connect || problem == AppProblem.permission ||
            problem == AppProblem.tunnelStopped) {
          _clearProblem();
        }
        if (previous != VpnConnectionState.connected) _log('info', 'Connected.');
      case VpnConnectionState.disconnected:
        if (!_nativeStartPending) _runningMode = null;
        needsReconnect = false;
        if (previous == VpnConnectionState.connected || previous == VpnConnectionState.disconnecting) {
          _log('info', 'Disconnected.');
        }
      case VpnConnectionState.connecting || VpnConnectionState.disconnecting:
        break;
    }
    if (unexpectedStop) {
      _setProblem(AppProblem.tunnelStopped);
      _log('warn', 'The VPN stopped unexpectedly. Traffic may use normal networking.');
    }
    notifyListeners();
  }

  // ── Sign-in ────────────────────────────────────────────────────────────

  static String defaultHostname() => Platform.isIOS ? 'tailsplit-ios' : 'tailsplit-android';

  /// Signs in a new device, or the existing device after it was signed out.
  Future<void> signInExit({bool allowProtectionGap = false}) async {
    if (busy) return;
    if (reconnectRequiresConsent && !allowProtectionGap) {
      needsReconnect = true;
      notifyListeners();
      return;
    }
    final attempt = ++_signInAttempt;
    _clearProblem();
    signIn = SignInPhase.starting;
    notifyListeners();
    if ((switchOn || exit != null) && !await disconnect(force: true)) {
      signIn = SignInPhase.idle;
      notifyListeners();
      return;
    }
    if (attempt != _signInAttempt) return;
    _log('info', 'Starting Tailscale sign-in.');
    if (isPreview) {
      await Future<void>.delayed(const Duration(milliseconds: 700));
      if (attempt != _signInAttempt) return;
      signIn = SignInPhase.idle;
      exit = (await PreviewData.seed(withNode: false)).exit;
      pickerRequested = true;
      _log('info', 'Signed in (simulated).');
      notifyListeners();
      return;
    }
    try {
      final identity = exit?.identity ??
          await TailscaleIdentity.generate(hostname: defaultHostname());
      if (attempt != _signInAttempt) return;
      final client = _newClient();
      _loginClient = client;
      _login = client.login(identity).listen(
        (progress) => _onLogin(progress, attempt),
        onError: (Object error) {
          if (attempt == _signInAttempt) _finishSignIn(error: error);
        },
        onDone: () {
          // A completed stream is normal after authorization; committing
          // finishes the sign-in itself.
          if (attempt == _signInAttempt &&
              signIn != SignInPhase.idle &&
              signIn != SignInPhase.finishing) {
            _finishSignIn();
          }
        },
        cancelOnError: true,
      );
    } catch (error) {
      if (attempt == _signInAttempt) await _finishSignIn(error: error);
    }
  }

  Future<void> _onLogin(LoginProgress progress, int attempt) async {
    if (attempt != _signInAttempt) return;
    try {
      switch (progress) {
        case LoginNeedsBrowser(:final authUrl):
          final first = _authUrl == null;
          _authUrl = authUrl;
          signIn = SignInPhase.browser;
          if (first) _log('info', 'Waiting for sign-in in the browser.');
          notifyListeners();
          await openSignInPage();
        case LoginAuthorized(:final snapshot, :final identity):
          // Control may have replaced an expired node key; keep the accepted one.
          if (signIn == SignInPhase.finishing) return;
          signIn = SignInPhase.finishing;
          notifyListeners();
          await _commitSignIn(identity, snapshot);
          if (attempt == _signInAttempt) await _finishSignIn();
      }
    } catch (error) {
      // Without this, a failed state write would leave sign-in stuck.
      if (attempt == _signInAttempt) await _finishSignIn(error: error);
    }
  }

  Future<void> openSignInPage() async {
    final url = _authUrl;
    if (url == null) return;
    var opened = false;
    try {
      opened = await launchUrl(url, mode: LaunchMode.inAppBrowserView);
    } catch (_) {}
    if (!opened) {
      try {
        opened = await launchUrl(url, mode: LaunchMode.externalApplication);
      } catch (_) {}
    }
    if (!opened) {
      _setProblem(
        AppProblem.signIn,
        error: const AppFailure(FailureCause.noBrowser, 'No browser could open the sign-in page.'),
      );
      _log('warn', 'No browser could open the sign-in page.');
      notifyListeners();
    } else if (problem == AppProblem.signIn) {
      _clearProblem();
      notifyListeners();
    }
  }

  Future<void> _commitSignIn(TailscaleIdentity identity, TailnetSnapshot snapshot) async {
    try {
      await closeInAppWebView();
    } catch (_) {}
    final previous = exit;
    // A different Tailnet than before invalidates the old node choice.
    final sameNetwork = previous != null && previous.networkName == snapshot.networkName;
    final profile = (sameNetwork ? previous : ExitProfile(identity: identity))
        .copyWith(identity: identity, snapshot: snapshot);
    await TsnetState.write(
      stateDirectory: _paths.tailscaleState,
      identity: identity,
      snapshot: snapshot,
    );
    exit = profile;
    await _saveExit();
    _log('info', 'Signed in to ${snapshot.networkName}.');
    pickerRequested = !profile.hasNode;
  }

  Future<void> _finishSignIn({Object? error}) async {
    final login = _login;
    final client = _loginClient;
    _login = null;
    _loginClient = null;
    _authUrl = null;
    signIn = SignInPhase.idle;
    notifyListeners();
    await login?.cancel();
    await client?.close();
    if (error != null && classifyFailure(error) != FailureCause.cancelled) {
      _setProblem(AppProblem.signIn, error: error);
      _log('error', 'Sign-in failed: ${describeFailure(error)}');
      try {
        await closeInAppWebView();
      } catch (_) {}
    }
    notifyListeners();
  }

  /// Cancelling is unavailable while an authorized sign-in is being saved.
  bool get canCancelSignIn =>
      signIn == SignInPhase.starting || signIn == SignInPhase.browser;

  Future<void> cancelSignIn() async {
    if (!canCancelSignIn) return;
    _signInAttempt++;
    _log('info', 'Sign-in cancelled.');
    await _finishSignIn();
    try {
      await closeInAppWebView();
    } catch (_) {}
  }

  // ── Exit nodes ─────────────────────────────────────────────────────────

  /// Fetches the netmap. A [silent] refresh logs failures without showing a
  /// problem, except a signed-out device, which always needs attention.
  Future<bool> refresh({bool silent = false}) async {
    final current = exit;
    if (current == null || refreshing || busy) return false;
    refreshing = true;
    if (!silent && problem == AppProblem.refresh) _clearProblem();
    notifyListeners();
    try {
      if (isPreview) {
        await Future<void>.delayed(const Duration(milliseconds: 600));
        if (exit == null) return false;
        exit = exit!.copyWith(snapshot: PreviewData.snapshot());
        return true;
      }
      final snapshot = await _fetchSnapshot(current);
      if (exit == null) return false; // Removed meanwhile.
      exit = exit!.copyWith(snapshot: snapshot);
      await _saveExit();
      if (problem == AppProblem.needsSignIn ||
          problem == AppProblem.refresh ||
          (problem == AppProblem.awaitingApproval && snapshot.machineAuthorized)) {
        _clearProblem();
      }
      return true;
    } on TailscaleNeedsLogin {
      _setProblem(AppProblem.needsSignIn);
      _log('warn', 'This device is no longer signed in to its Tailnet.');
      return false;
    } catch (error) {
      _log('warn', 'Refreshing exit nodes failed: ${describeFailure(error)}');
      if (!silent) _setProblem(AppProblem.refresh, error: error);
      return false;
    } finally {
      refreshing = false;
      notifyListeners();
    }
  }

  Future<TailnetSnapshot> _fetchSnapshot(ExitProfile current, {Duration? timeout}) async {
    final identity = await _liveIdentity(current.identity);
    final client = _newClient();
    try {
      final request = client.fetchSnapshot(identity);
      return timeout == null ? await request : await request.timeout(timeout);
    } finally {
      await client.close();
    }
  }

  /// The address sing-box uses for [peer]'s exit route, IPv4 first.
  static String? exitAddress(TailnetPeer peer) {
    if (peer.addresses.isEmpty) return null;
    return peer.addresses.firstWhere((a) => !a.contains(':'), orElse: () => peer.addresses.first);
  }

  /// Whether [peer] can be newly chosen as this exit's node.
  static bool selectable(TailnetPeer peer) => peer.online && exitAddress(peer) != null;

  /// Saves the chosen node; false when it could not be saved, leaving the
  /// previous choice. Split reconnects in the background; Global waits for consent.
  Future<bool> confirmNode(TailnetPeer peer) async {
    final current = exit;
    final address = exitAddress(peer);
    if (current == null || address == null) return false;
    final next = current.copyWith(
      nodeId: peer.stableId,
      nodeName: peer.name,
      nodeAddress: address,
      label: current.autoName
          ? ExitProfile.automaticLabel(peer.name, current.networkName)
          : current.label,
    );
    if (!await _writeExit(next)) return false;
    exit = next;
    if (problem == AppProblem.nodeOffline || problem == AppProblem.nodeMissing) _clearProblem();
    _log('info', 'Exit node set to ${peer.name}.');
    notifyListeners();
    if (switchOn) unawaited(reconnect());
    return true;
  }

  Future<bool> removeExit() async {
    final current = exit;
    if (current == null || busy) return false;
    removing = true;
    notifyListeners();
    Object? storageError;
    try {
      // Never log out or remove state until native shutdown is confirmed.
      if (!await disconnect(force: true)) return false;
      if (!isPreview) {
        final client = _newClient();
        try {
          await client
              .logout(await _liveIdentity(current.identity))
              .timeout(const Duration(seconds: 15));
        } catch (error) {
          _log('warn', 'Tailscale sign-out did not complete: ${describeFailure(error)}');
        } finally {
          await client.close();
        }
        try {
          await TsnetState.clear(_paths.tailscaleState);
          await JsonStore.delete(_paths.exitFile);
        } catch (error) {
          storageError = error;
          _log('error', 'The exit could not be deleted from storage: ${describeFailure(error)}');
        }
      }
      exit = null;
      needsReconnect = false;
      _clearProblem();
      if (storageError != null) _setProblem(AppProblem.storage, error: storageError);
      _log('info', 'Exit removed.');
      return true;
    } finally {
      removing = false;
      notifyListeners();
    }
  }

  Future<TailscaleIdentity> _liveIdentity(TailscaleIdentity identity) async {
    // tsnet may rotate the node key while the VPN runs; keep using its key.
    String? live;
    try {
      live = await TsnetState.readNodePrivateKey(_paths.tailscaleState);
    } catch (_) {
      live = null;
    }
    if (live == null || live == identity.nodePrivateKey) return identity;
    final updated = identity.copyWith(nodePrivateKey: live);
    exit = exit?.copyWith(identity: updated);
    await _saveExit();
    return updated;
  }

  // ── Connection ─────────────────────────────────────────────────────────

  Future<void> setConnected(bool value) async {
    if (value) {
      await connect();
    } else {
      await disconnect();
    }
  }

  Future<void> connect() async {
    final current = exit;
    if (current == null || !canConnect || preparing || active ||
        vpnState == VpnConnectionState.disconnecting) {
      return;
    }
    final connectionSettings = settings;
    final attempt = ++_connectAttempt;
    bool cancelled() => attempt != _connectAttempt || _disposed;
    connectStep = ConnectStep.checking;
    if (problem != AppProblem.storage) _clearProblem();
    needsReconnect = false;
    notifyListeners();
    _log('info', 'Connecting through ${current.displayName(current.nodeName)}.');
    try {
      if (isPreview) {
        await Future<void>.delayed(const Duration(milliseconds: 500));
        if (cancelled()) return;
        final node = current.node;
        if (node == null || !node.online) {
          _setProblem(AppProblem.nodeOffline, subject: current.nodeName);
          return;
        }
        connectStep = ConnectStep.starting;
        notifyListeners();
        await Future<void>.delayed(const Duration(milliseconds: 600));
        if (cancelled()) return;
        vpnState = VpnConnectionState.connected;
        _runningMode = connectionSettings.mode;
        _log('info', 'Connected (simulated).');
        return;
      }

      // A native logout must not be undone using the app's cached identity.
      // Keep the exit selection and let an explicit sign-in replace the state.
      final stateFile = File('${_paths.tailscaleState.path}/${TsnetState.stateFileName}');
      if (await stateFile.exists()) {
        final loggedOut = await TsnetState.readLoggedOut(_paths.tailscaleState);
        final nodeKey = await TsnetState.readNodePrivateKey(_paths.tailscaleState);
        if (cancelled()) return;
        if (loggedOut != false || nodeKey == null) {
          _setProblem(AppProblem.needsSignIn);
          _log('warn', 'The saved Tailscale session needs a new sign-in.');
          return;
        }
      }
      if (cancelled()) return;

      // 1. Check the node against Tailscale rather than a stale netmap; never
      // connect through a node that is known to be gone or offline.
      var profile = current;
      final fetched = profile.snapshot?.fetchedAt;
      bool expired(TailnetSnapshot? snapshot) =>
          snapshot?.keyExpiry != null && !snapshot!.keyExpiry!.isAfter(DateTime.now());
      var fresh = fetched != null && DateTime.now().difference(fetched) < freshSnapshot &&
          !expired(profile.snapshot);
      if (!fresh) {
        try {
          final snapshot = await _fetchSnapshot(profile, timeout: const Duration(seconds: 12));
          if (cancelled()) return;
          profile = (exit ?? profile).copyWith(snapshot: snapshot);
          exit = profile;
          await _saveExit();
          fresh = true;
        } on TailscaleNeedsLogin {
          if (cancelled()) return;
          _setProblem(AppProblem.needsSignIn);
          _log('warn', 'This device is no longer signed in to its Tailnet.');
          return;
        } catch (error) {
          if (cancelled()) return;
          _log('warn', 'Could not check the exit node (${describeFailure(error)}); using its saved status.');
        }
      }
      if (expired(profile.snapshot)) {
        _setProblem(AppProblem.needsSignIn);
        _log('warn', 'The saved Tailscale key has expired; sign in again.');
        return;
      }
      if (fresh) {
        final snapshot = profile.snapshot!;
        if (!snapshot.machineAuthorized) {
          _setProblem(AppProblem.awaitingApproval);
          _log('warn', 'This device is waiting for admin approval.');
          return;
        }
        final node = profile.node;
        if (node == null) {
          _setProblem(AppProblem.nodeMissing, subject: profile.nodeName);
          _log('warn', '${profile.nodeName} is no longer an exit node in this Tailnet.');
          return;
        }
        if (!node.online) {
          _setProblem(AppProblem.nodeOffline, subject: node.name);
          _log('warn', '${node.name} is offline.');
          return;
        }
        final address = exitAddress(node);
        if (address != null && address != profile.nodeAddress) {
          profile = profile.copyWith(nodeAddress: address);
          exit = profile;
          await _saveExit();
        }
      }
      if (profile.nodeAddress.isEmpty) {
        _setProblem(AppProblem.nodeMissing, subject: profile.nodeName);
        return;
      }
      final identity = await _liveIdentity(profile.identity);
      final snapshot = profile.snapshot;
      if (!await stateFile.exists() && snapshot != null) {
        await TsnetState.write(
          stateDirectory: _paths.tailscaleState,
          identity: identity,
          snapshot: snapshot,
        );
      }
      if (cancelled()) return;

      // 2. Rule list: cached after the first download.
      RuleProjection? rules;
      if (connectionSettings.mode == RoutingMode.split && connectionSettings.source != RuleSource.none) {
        connectStep = ConnectStep.rules;
        notifyListeners();
        final url = connectionSettings.source == RuleSource.builtin
            ? RuleLists.defaultUrl
            : connectionSettings.customSourceUrl.isEmpty ? null : connectionSettings.customSourceUrl;
        try {
          if (url == null) {
            throw const RuleListException('no custom rule list URL', cause: FailureCause.invalid);
          }
          final loaded = await RuleLists.load(url, _paths.rulesDirectory, download: _ruleDownload);
          rules = loaded.projection;
          rulesUpdatedAt = loaded.fetchedAt;
        } catch (error) {
          if (cancelled()) return;
          _setProblem(AppProblem.rules, error: error);
          _log('error', 'Rule list unavailable: ${describeFailure(error)}');
          return;
        }
        if (cancelled()) return;
      }

      // 3. Start the VPN.
      connectStep = ConnectStep.starting;
      notifyListeners();
      final config = ConfigBuilder.build(
        settings: connectionSettings,
        rules: rules,
        exit: ExitRuntime(
          hostname: identity.hostname,
          nodeAddress: profile.nodeAddress,
          stateDirectory: _paths.tailscaleState.path,
          cacheFile: _paths.cacheFile,
          magicDnsSuffix: profile.snapshot?.magicDnsSuffix ?? '',
        ),
      );
      await _vpn.check(config);
      if (cancelled()) return;
      if (!await _vpn.requestPermission()) {
        if (cancelled()) return;
        _setProblem(AppProblem.permission);
        _log('warn', 'VPN permission was not granted.');
        return;
      }
      if (cancelled()) return;
      await _serializeNative(() async {
        if (cancelled()) return;
        _runningMode = connectionSettings.mode;
        await _startVpnBounded(config);
        // A start acknowledgement can arrive after cancellation. Stop inside
        // this same serialized operation before any later start may run.
        if (attempt != _connectAttempt) {
          await _stopVpnConfirmed();
        } else {
          _runningMode = connectionSettings.mode;
          needsReconnect = needsReconnect || settings.affectsConnection(connectionSettings) ||
              exit?.nodeId != profile.nodeId;
        }
      });
    } catch (error) {
      if (_shutdownUnconfirmed) {
        // The bounded queue is released even if the native acknowledgement is
        // lost. Stop may cancel native work, but a pending start keeps the latch.
        final reportStartFailure = !cancelled();
        if (await disconnect(force: true) && reportStartFailure) {
          _setProblem(AppProblem.connect, error: error);
          _log('error', 'Could not start the VPN: ${describeFailure(error)}');
        }
      } else if (!cancelled()) {
        _setProblem(AppProblem.connect, error: error);
        _log('error', 'Could not start the VPN: ${describeFailure(error)}');
      }
    } finally {
      if (attempt == _connectAttempt) {
        connectStep = null;
      }
      notifyListeners();
    }
  }

  /// A native start and every subsequent stop share one ordered queue. Failed
  /// commands release the queue but do not grant permission to remove state.
  Future<T> _serializeNative<T>(Future<T> Function() operation) {
    final next = _nativeOperations.then((_) => operation());
    _nativeOperations = next.then<void>((_) {}, onError: (Object _, StackTrace _) {});
    return next;
  }

  Future<void> _startVpnBounded(String config) async {
    _nativeStartPending = true;
    var timedOut = false;
    try {
      await Future<void>.sync(() => _vpn.start(config)).whenComplete(() {
        // A timeout only releases the queue; it cannot cancel a platform call.
        // Keep starts/removal blocked and compensate when that call settles.
        if (timedOut) unawaited(_recoverLateStart());
      }).timeout(_nativeCommandTimeout, onTimeout: () {
        timedOut = true;
        throw TimeoutException('The native VPN start did not acknowledge completion.');
      });
    } catch (_) {
      _shutdownUnconfirmed = true;
      rethrow;
    } finally {
      if (!timedOut) _nativeStartPending = false;
    }
  }

  Future<void> _recoverLateStart() async {
    try {
      await _serializeNative(() async {
        _nativeStartPending = false;
        await _stopVpnConfirmed();
      });
      if (problem == AppProblem.disconnect) _clearProblem();
    } catch (error) {
      _setProblem(AppProblem.disconnect, error: error);
    } finally {
      notifyListeners();
    }
  }

  Future<void> _stopVpnConfirmed() async {
    _shutdownUnconfirmed = true;
    _nativeStopsPending++;
    await Future<void>.sync(_vpn.stop).whenComplete(() {
      _nativeStopsPending--;
    }).timeout(_nativeCommandTimeout);
    await _vpn.waitFor(VpnConnectionState.disconnected, timeout: _nativeCommandTimeout)
        .timeout(_nativeCommandTimeout);
    final current = await _vpn.currentStatus().timeout(_nativeCommandTimeout);
    _onStatus(current);
    if (current.state != VpnConnectionState.disconnected ||
        _nativeStartPending || _nativeStopsPending != 0) {
      throw const AppFailure(FailureCause.unknown,
          'The VPN has not confirmed shutdown of all pending operations.');
    }
    _runningMode = null;
    _shutdownUnconfirmed = false;
  }

  /// Stops the VPN, or cancels an attempt that has not reached it yet. False
  /// means the service could still own its identity and no replacement may start.
  Future<bool> disconnect({bool force = false}) {
    final step = connectStep;
    _connectAttempt++;
    if (preparing) {
      connectStep = null;
      _log('info', 'Connection cancelled.');
    }
    final pending = _stopRequest;
    if (pending != null) return pending;
    final reachedService = force || _shutdownUnconfirmed || _nativeStartPending ||
        vpnState != VpnConnectionState.disconnected || step == ConnectStep.starting;
    late final Future<bool> request;
    request = _disconnect(reachedService).whenComplete(() {
      if (identical(_stopRequest, request)) _stopRequest = null;
      notifyListeners();
    });
    _stopRequest = request;
    notifyListeners();
    return request;
  }

  Future<bool> _disconnect(bool reachedService) async {
    if (isPreview) {
      if (vpnState != VpnConnectionState.disconnected) {
        vpnState = VpnConnectionState.disconnecting;
        notifyListeners();
        await Future<void>.delayed(const Duration(milliseconds: 400));
        _onStatus(const VpnStatus(state: VpnConnectionState.disconnected));
      }
      _runningMode = null;
      return true;
    }
    // Cancelling preflight does not touch a service it never started. Identity
    // removal and sign-in use force=true even if the UI's state says stopped.
    if (!reachedService) return true;
    try {
      await _serializeNative(_stopVpnConfirmed);
      if (problem == AppProblem.disconnect) _clearProblem();
      return true;
    } catch (error) {
      _setProblem(AppProblem.disconnect, error: error);
      _log('warn', 'Disconnect failed: ${describeFailure(error)}');
      return false;
    }
  }

  Future<void> reconnect({bool allowProtectionGap = false}) async {
    if (_reconnecting) return;
    if (reconnectRequiresConsent && !allowProtectionGap) {
      // Keep the current Global tunnel alive until the user confirms a gap.
      needsReconnect = true;
      notifyListeners();
      return;
    }
    _reconnecting = true;
    try {
      if (!await disconnect(force: true)) {
        needsReconnect = true;
        return;
      }
      await connect();
    } finally {
      _reconnecting = false;
      notifyListeners();
    }
  }

  // ── Settings ───────────────────────────────────────────────────────────

  /// Saves immediately. Apply-now changes reconnect Split after shutdown;
  /// Global and other connection settings wait for an explicit Reconnect.
  Future<void> updateSettings(MobileSettings next, {bool applyNow = false}) async {
    final previous = settings;
    settings = next;
    final changed = previous.affectsConnection(next);
    if (switchOn && changed) needsReconnect = true;
    notifyListeners();
    if (previous.source != next.source || previous.customSourceUrl != next.customSourceUrl) {
      rulesUpdateError = null;
      await _loadRuleInfo();
      notifyListeners();
    }
    if (!isPreview) await _writeSettings(next);
    if (applyNow && switchOn && changed) await reconnect();
  }

  Future<void> setRoutingMode(RoutingMode mode) =>
      updateSettings(settings.copyWith(mode: mode), applyNow: true);

  /// Writes settings and the exit again after a storage failure.
  Future<void> retrySave() async {
    if (isPreview) return;
    final saved = await _writeSettings(settings) && (exit == null || await _writeExit(exit!));
    if (saved && problem == AppProblem.storage) _clearProblem();
    notifyListeners();
  }

  /// Downloads the current rule list again. A failure keeps the saved copy.
  Future<void> updateRules() async {
    final url = _ruleUrl;
    if (url == null || rulesUpdating) return;
    rulesUpdating = true;
    rulesUpdateError = null;
    notifyListeners();
    try {
      if (isPreview) {
        await Future<void>.delayed(const Duration(milliseconds: 700));
        rulesUpdatedAt = DateTime.now();
        return;
      }
      final loaded = await RuleLists.load(url, _paths.rulesDirectory, refresh: true, download: _ruleDownload);
      final failure = loaded.refreshError;
      if (failure != null) throw failure;
      rulesUpdatedAt = loaded.fetchedAt;
      if (switchOn && settings.mode == RoutingMode.split) needsReconnect = true;
      if (problem == AppProblem.rules) _clearProblem();
      _log('info', 'Rule list updated (${loaded.projection.proxyCount} proxy rules).');
    } catch (error) {
      rulesUpdateError = classifyFailure(error);
      _log('warn', 'Updating the rule list failed: ${describeFailure(error)}');
    } finally {
      rulesUpdating = false;
      notifyListeners();
    }
  }

  Future<void> _loadRuleInfo() async {
    final url = _ruleUrl;
    rulesUpdatedAt = url == null || isPreview ? rulesUpdatedAt : await RuleLists.cachedAt(_paths.rulesDirectory, url);
  }

  // ── Problems and logs ──────────────────────────────────────────────────

  void clearLogs() => logBuffer.clear();

  void dismissProblem() {
    _clearProblem();
    notifyListeners();
  }

  void _setProblem(AppProblem kind, {Object? error, String? detail, String? subject}) {
    problem = kind;
    problemCause = error == null ? null : classifyFailure(error);
    problemDetail = detail ?? (error == null ? null : describeFailure(error));
    problemSubject = subject;
  }

  void _clearProblem() {
    problem = null;
    problemCause = null;
    problemDetail = null;
    problemSubject = null;
  }

  void _log(String level, String message, {String source = 'app'}) {
    if (!_disposed) logBuffer.add(LogLine(level, message, source: source));
  }

  Future<bool> _writeSettings(MobileSettings value) async {
    try {
      await JsonStore.write(_paths.settingsFile, value.toJson());
      return true;
    } catch (error) {
      _setProblem(AppProblem.storage, error: error);
      _log('error', 'Settings could not be saved: ${describeFailure(error)}');
      notifyListeners();
      return false;
    }
  }

  Future<bool> _writeExit(ExitProfile profile) async {
    if (isPreview) return true;
    try {
      await JsonStore.write(_paths.exitFile, profile.toJson());
      return true;
    } catch (error) {
      _setProblem(AppProblem.storage, error: error);
      _log('error', 'The exit could not be saved: ${describeFailure(error)}');
      notifyListeners();
      return false;
    }
  }

  Future<void> _saveExit() async {
    final current = exit;
    if (current != null) await _writeExit(current);
  }
}
