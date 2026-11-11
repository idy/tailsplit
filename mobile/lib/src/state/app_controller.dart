import 'dart:async';
import 'dart:collection';
import 'dart:io';

import 'package:flutter/foundation.dart';
import 'package:flutter/widgets.dart' show AppLifecycleState, WidgetsBinding, WidgetsBindingObserver;
import 'package:url_launcher/url_launcher.dart';

import '../model/exit_profile.dart';
import '../model/settings.dart';
import '../model/tailnet_account.dart';
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
  TailnetStore _accounts = TailnetStore();
  Iterable<TailnetAccount> get tailnets => _accounts.accounts.values;
  TailnetAccount? tailnetById(String id) => _accounts.accounts[id];
  String? get selectedTailnetId => _accounts.selection?.tailnetId;

  ExitProfile? get exit {
    final selection = _accounts.selection;
    final account = selection == null ? null : tailnetById(selection.tailnetId);
    return account == null ? null : selection!.compose(account.identity, account.snapshot);
  }

  /// Legacy single-account test fixtures only. Production code mutates the
  /// authoritative account store through sign-in, selection and removal.
  @visibleForTesting
  set exit(ExitProfile? profile) {
    _accounts = profile == null ? TailnetStore()
        : TailnetStore.fromLegacy(profile, allowUnselectedFixture: true);
  }

  final _accountEpochs = <String, int>{};
  final _refreshingAccounts = <String>{};
  Future<void> _accountOperations = Future<void>.value();
  bool _accountStoreUnreadable = false;
  String? _runningTailnetId;
  bool get refreshing => _refreshingAccounts.isNotEmpty;
  bool isRefreshingTailnet(String id) => _refreshingAccounts.contains(id);
  int _accountEpoch(String id) => _accountEpochs[id] ?? 0;
  void _invalidateAccount(String id) => _accountEpochs[id] = _accountEpoch(id) + 1;
  bool _accountCurrent(String id, int epoch) =>
      !_disposed && _accountEpoch(id) == epoch && tailnetById(id) != null;

  Future<T> _serializeAccounts<T>(Future<T> Function() operation) {
    final next = _accountOperations.then((_) => operation());
    _accountOperations = next.then<void>((_) {}, onError: (Object _, StackTrace _) {});
    return next;
  }
  VpnConnectionState vpnState = VpnConnectionState.disconnected;
  SignInPhase signIn = SignInPhase.idle;
  ConnectStep? connectStep;
  bool get preparing => connectStep != null;
  bool removing = false;
  bool needsReconnect = false;
  String coreVersion = '';

  AppProblem? problem;
  FailureCause? problemCause;

  /// English diagnostic detail (also written to Logs).
  String? problemDetail;

  /// The node or item a problem is about, shown in its title.
  String? problemSubject;

  /// Account-specific errors must reauthenticate the same account even after
  /// the user selects an exit in another tailnet.
  String? problemTailnetId;

  DateTime? rulesUpdatedAt;
  bool rulesUpdating = false;
  FailureCause? rulesUpdateError;

  /// The account whose nodes the UI should open after sign-in. Adding a new
  /// account does not change the global selection just to drive the picker.
  String? pickerRequestedTailnetId;
  bool get pickerRequested => pickerRequestedTailnetId != null;
  set pickerRequested(bool value) {
    pickerRequestedTailnetId = value
        ? pickerRequestedTailnetId ?? selectedTailnetId ?? tailnets.firstOrNull?.id : null;
  }

  /// Preview builds only: the state requested for simulator review.
  String previewScreen = '';

  final logBuffer = LogBuffer();
  List<LogLine> get logs => logBuffer.lines;

  Uri? _authUrl; // Sensitive: memory only, never logged or persisted.
  StreamSubscription<LoginProgress>? _login;
  TailscaleControlClient? _loginClient;
  String? _signInTailnetId;
  StreamSubscription<VpnStatus>? _statusSub;
  StreamSubscription<SingBoxLog>? _logSub;
  int _connectAttempt = 0;
  int _signInAttempt = 0;
  bool _observing = false;
  bool _disposed = false;
  Future<void> _nativeOperations = Future<void>.value();
  Future<bool>? _stopRequest;
  Object? _pendingNativeStart;
  bool get _nativeStartPending => _pendingNativeStart != null;
  // Independent of displayed status and dismissible errors: only a confirmed
  // native stop barrier can release this latch, including lost start replies.
  bool _shutdownUnconfirmed = false;
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
      !_accountStoreUnreadable && !_shutdownUnconfirmed && !_nativeStartPending && _stopRequest == null;
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
      // Existence is authoritative, including an empty or damaged store. Never
      // fall back to stale legacy data after accounts have been removed.
      if (await FileSystemEntity.type(_paths.accountsFile.path, followLinks: false) != FileSystemEntityType.notFound) {
        final saved = await JsonStore.read(_paths.accountsFile);
        if (saved == null) throw const FormatException('Unreadable tailnet store');
        _accounts = TailnetStore.fromJson(saved);
      } else {
        final legacy = await JsonStore.read(_paths.exitFile);
        if (legacy != null) {
          _accounts = TailnetStore.fromLegacy(ExitProfile.fromJson(legacy));
          await _writeAccounts(_accounts);
        }
      }
    } catch (error) {
      _accountStoreUnreadable = true;
      _setProblem(AppProblem.storage, error: error);
      _log('warn', 'Saved tailnets could not be read (${describeFailure(error)}).');
    }
    await _loadRuleInfo();
    notifyListeners();
    try {
      // Keep observing even if initialization is cancelled and a later connect
      // retries it; native status streams can be subscribed before setup.
      _statusSub = _vpn.status.listen(_onStatus, onError: (Object error) {
        _log('warn', 'VPN status unavailable: ${describeFailure(error)}');
      });
      _logSub = _vpn.logs.listen(
        (entry) => _log(entry.level, entry.message, source: 'core'),
        onError: (Object _) {},
      );
      await _vpn.initialize();
      _onStatus(await _vpn.currentStatus());
      coreVersion = await _vpn.coreVersion();
    } catch (error) {
      _log('warn', 'VPN service unavailable: ${describeFailure(error)}');
    }
    WidgetsBinding.instance.addObserver(this);
    _observing = true;
    notifyListeners();
    // A saved netmap can be days old; update it quietly while idle.
    if (tailnets.isNotEmpty && !active) unawaited(refreshAll(silent: true));
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
    _accounts = screen == 'empty' || screen == 'signin' ? TailnetStore() : TailnetStore(
      accounts: seeded.tailnets,
      selection: seeded.exit.hasNode ? ExitSelection.fromProfile(seeded.tailnets.first.id, seeded.exit) : null,
    );
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
    if (active) _runningTailnetId = selectedTailnetId;
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
    if (active || preparing || busy) return;
    for (final account in tailnets.toList()) {
      final fetched = account.snapshot?.fetchedAt;
      if (fetched == null || DateTime.now().difference(fetched) > staleSnapshot) {
        unawaited(refresh(silent: true, tailnetId: account.id));
      }
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

  /// Adding an independent account has no protection gap. Reauthentication
  /// needs consent only if its native state may belong to the live Global (or
  /// restored unknown-mode) tunnel, including an old pending exit selection.
  bool signInRequiresConsent({String? tailnetId}) => tailnetId != null &&
      tailnetById(tailnetId) != null && _mayOwnNative(tailnetId) && reconnectRequiresConsent;

  /// A null target adds an independent account; an ID reauthenticates exactly
  /// that account. Adding never stops or changes an already selected exit.
  Future<void> signInExit({String? tailnetId, bool allowProtectionGap = false}) async {
    if (busy || _accountStoreUnreadable) return;
    final previous = tailnetId == null ? null : tailnetById(tailnetId);
    if (tailnetId != null && previous == null) return;
    final mustStop = previous != null && _mayOwnNative(previous.id);
    if (signInRequiresConsent(tailnetId: tailnetId) && !allowProtectionGap) {
      needsReconnect = true;
      notifyListeners();
      return;
    }
    final id = previous?.id ?? TailnetAccount.newId();
    _invalidateAccount(id);
    final epoch = _accountEpoch(id);
    final attempt = ++_signInAttempt;
    _signInTailnetId = previous?.id;
    bool cancelled() => _disposed || attempt != _signInAttempt ||
        _accountEpoch(id) != epoch || (previous != null && tailnetById(id) == null);
    _clearProblem();
    signIn = SignInPhase.starting;
    notifyListeners();
    if (mustStop && !await disconnect(force: true)) {
      if (!cancelled()) await _finishSignIn(attempt: attempt);
      return;
    }
    if (cancelled()) return;
    _log('info', 'Starting Tailscale sign-in.');
    try {
      if (isPreview) {
        await Future<void>.delayed(const Duration(milliseconds: 700));
        if (cancelled()) return;
        final seeded = await PreviewData.seed(withNode: false);
        if (cancelled()) return;
        signIn = SignInPhase.finishing;
        await _commitSignIn(previous?.identity ?? seeded.exit.identity,
            previous?.snapshot ?? PreviewData.snapshot(secondary: tailnets.isNotEmpty),
            id: id, existing: previous != null, cancelled: cancelled);
        await _finishSignIn(attempt: attempt);
        return;
      }
      final identity = previous == null
          ? await TailscaleIdentity.generate(hostname: defaultHostname())
          : await _liveIdentity(previous, cancelled: cancelled);
      if (cancelled()) return;
      final client = _newClient();
      _loginClient = client;
      _login = client.login(identity).listen(
        (progress) => _onLogin(progress, attempt, id, previous != null, cancelled),
        onError: (Object error) {
          if (!cancelled()) _finishSignIn(attempt: attempt, error: error);
        },
        onDone: () {
          if (!cancelled() && signIn != SignInPhase.idle && signIn != SignInPhase.finishing) {
            _finishSignIn(attempt: attempt);
          }
        },
        cancelOnError: true,
      );
    } catch (error) {
      if (!cancelled()) await _finishSignIn(attempt: attempt, error: error);
    }
  }

  Future<void> _onLogin(LoginProgress progress, int attempt, String id,
      bool existing, bool Function() cancelled) async {
    if (cancelled()) return;
    try {
      switch (progress) {
        case LoginNeedsBrowser(:final authUrl):
          if (signIn == SignInPhase.finishing) return;
          final first = _authUrl == null;
          _authUrl = authUrl;
          signIn = SignInPhase.browser;
          if (first) _log('info', 'Waiting for sign-in in the browser.');
          notifyListeners();
          await openSignInPage();
        case LoginAuthorized(:final snapshot, :final identity):
          if (signIn == SignInPhase.finishing) return;
          signIn = SignInPhase.finishing;
          notifyListeners();
          await _commitSignIn(identity, snapshot, id: id, existing: existing, cancelled: cancelled);
          if (!cancelled()) await _finishSignIn(attempt: attempt);
      }
    } catch (error) {
      if (!cancelled()) await _finishSignIn(attempt: attempt, error: error);
    }
  }

  Future<void> openSignInPage() async {
    final url = _authUrl;
    final attempt = _signInAttempt;
    if (url == null) return;
    bool cancelled() => _disposed || attempt != _signInAttempt || _authUrl != url;
    var opened = false;
    try {
      opened = await launchUrl(url, mode: LaunchMode.inAppBrowserView);
    } catch (_) {}
    if (cancelled()) return;
    if (!opened) {
      try {
        opened = await launchUrl(url, mode: LaunchMode.externalApplication);
      } catch (_) {}
    }
    if (cancelled()) return;
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

  Future<void> _commitSignIn(TailscaleIdentity identity, TailnetSnapshot snapshot, {
    required String id, required bool existing, required bool Function() cancelled,
  }) async {
    try {
      await closeInAppWebView();
    } catch (_) {}
    await _serializeAccounts(() async {
      if (cancelled()) return;
      final previous = tailnetById(id);
      final key = TailnetAccount.networkKeyOf(snapshot);
      if (existing && previous != null && previous.networkKey.isNotEmpty &&
          key.isNotEmpty && previous.networkKey != key) {
        throw const AppFailure(FailureCause.rejected,
            'This sign-in belongs to a different Tailnet. Add it as a new Tailnet.');
      }
      final duplicate = key.isEmpty ? null : tailnets.where(
          (account) => account.id != id && account.networkKey == key).firstOrNull;
      if (duplicate != null) {
        // Keep the working account and its native state intact. The newly
        // registered device is not a replacement for an existing identity.
        if (!isPreview && identity.machinePrivateKey != duplicate.identity.machinePrivateKey) {
          final client = _newClient();
          try {
            await client.logout(identity).timeout(const Duration(seconds: 15));
          } catch (error) {
            _log('warn', 'Duplicate device sign-out did not complete: ${describeFailure(error)}');
          } finally {
            await client.close();
          }
        }
        if (cancelled()) return;
        pickerRequestedTailnetId = duplicate.id;
        _log('info', 'This Tailnet is already signed in; the existing account was kept.');
        return;
      }
      final account = previous?.copyWith(identity: identity, snapshot: snapshot) ??
          TailnetAccount(id: id, identity: identity, snapshot: snapshot);
      if (!isPreview) {
        await TsnetState.write(stateDirectory: _paths.tailscaleStateFor(account),
            identity: identity, snapshot: snapshot);
      }
      if (cancelled()) return;
      if (!await _commitAccounts(_accounts.withAccount(account))) {
        throw const FileSystemException('The signed-in Tailnet could not be saved.');
      }
      _log('info', 'Signed in to ${snapshot.networkName}.');
      _logDiscovery(id, snapshot);
      if (!existing || selectedTailnetId != id || exit?.hasNode != true) {
        pickerRequestedTailnetId = id;
      }
    });
  }

  Future<void> _finishSignIn({int? attempt, Object? error}) async {
    final currentAttempt = attempt ?? _signInAttempt;
    if (_disposed || currentAttempt != _signInAttempt) return;
    final login = _login;
    final client = _loginClient;
    _login = null;
    _loginClient = null;
    _authUrl = null;
    await login?.cancel();
    await client?.close();
    if (_disposed || currentAttempt != _signInAttempt) return;
    if (error != null && classifyFailure(error) != FailureCause.cancelled) {
      _setProblem(AppProblem.signIn, error: error);
      _log('error', 'Sign-in failed: ${describeFailure(error)}');
      try {
        await closeInAppWebView();
      } catch (_) {}
    }
    if (_disposed || currentAttempt != _signInAttempt) return;
    signIn = SignInPhase.idle;
    _signInTailnetId = null;
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
  Future<bool> refresh({bool silent = false, String? tailnetId}) async {
    final id = tailnetId ?? selectedTailnetId;
    final current = id == null ? null : tailnetById(id);
    if (current == null || isRefreshingTailnet(current.id) || busy) return false;
    final epoch = _accountEpoch(current.id);
    bool cancelled() => !_accountCurrent(current.id, epoch);
    _refreshingAccounts.add(current.id);
    if (!silent && problem == AppProblem.refresh) _clearProblem();
    notifyListeners();
    try {
      final snapshot = isPreview
          ? await Future<TailnetSnapshot>.delayed(const Duration(milliseconds: 600),
              () => current.snapshot!.copyWith(fetchedAt: DateTime.now()))
          : await _fetchSnapshot(current, cancelled: cancelled);
      if (cancelled()) return false;
      final saved = await _serializeAccounts(() async {
        if (cancelled()) return false;
        return _commitAccounts(_accounts.withAccount(tailnetById(current.id)!.copyWith(snapshot: snapshot)));
      });
      if (!saved || cancelled()) return false;
      _logDiscovery(current.id, snapshot);
      if (problemTailnetId == current.id && (problem == AppProblem.needsSignIn ||
          problem == AppProblem.refresh ||
          (problem == AppProblem.awaitingApproval && snapshot.machineAuthorized))) {
        _clearProblem();
      }
      return true;
    } on TailscaleNeedsLogin {
      if (cancelled()) return false;
      if (selectedTailnetId == current.id || !silent) {
        _setProblem(AppProblem.needsSignIn, subject: current.networkName, tailnetId: current.id);
      }
      _log('warn', '${current.networkName}: this device is no longer signed in.');
      return false;
    } catch (error) {
      if (cancelled()) return false;
      _log('warn', 'Refreshing ${current.networkName} failed: ${describeFailure(error)}');
      if (!silent) _setProblem(AppProblem.refresh, error: error, tailnetId: current.id);
      return false;
    } finally {
      _refreshingAccounts.remove(current.id);
      notifyListeners();
    }
  }

  Future<void> refreshAll({bool silent = false}) async {
    await Future.wait([for (final account in tailnets.toList()) refresh(silent: silent, tailnetId: account.id)]);
  }

  Future<TailnetSnapshot> _fetchSnapshot(TailnetAccount current, {
    Duration? timeout, required bool Function() cancelled,
  }) async {
    final identity = await _liveIdentity(current, cancelled: cancelled);
    if (cancelled()) throw const AppFailure(FailureCause.cancelled, 'Account request cancelled.');
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
  Future<bool> confirmNode(TailnetPeer peer, {String? tailnetId}) async {
    final id = tailnetId ?? selectedTailnetId;
    final account = id == null ? null : tailnetById(id);
    if (account == null || busy || !selectable(peer)) return false;
    final epoch = _accountEpoch(account.id);
    final saved = await _serializeAccounts(() async {
      if (!_accountCurrent(account.id, epoch) || busy) return false;
      final currentAccount = tailnetById(account.id)!;
      // Use the captured account's netmap, not an arbitrary peer from another
      // tailnet that happens to have an identical stable node ID.
      final node = currentAccount.snapshot?.exitNodes.where((node) => node.stableId == peer.stableId).firstOrNull;
      if (node == null || !selectable(node)) return false;
      final previous = selectedTailnetId == account.id ? exit : null;
      final selection = ExitSelection(
        tailnetId: account.id, nodeId: node.stableId, nodeName: node.name,
        nodeAddress: exitAddress(node)!,
        autoName: previous?.autoName ?? true,
        label: previous?.autoName == false ? previous!.label
            : ExitProfile.automaticLabel(node.name, currentAccount.networkName),
      );
      return _commitAccounts(_accounts.select(selection));
    });
    if (!saved) return false;
    if (problem == AppProblem.nodeOffline || problem == AppProblem.nodeMissing) _clearProblem();
    _log('info', 'Exit node set to ${peer.name}.');
    final wasOn = switchOn;
    // Cancel preflight for the previous node. An already submitted Global
    // start must keep running until reconnect consent, just like a connected
    // Global tunnel. Split reconnect will cancel it through disconnect.
    if (connectStep != ConnectStep.starting) {
      _connectAttempt++;
      connectStep = null;
    }
    notifyListeners();
    if (wasOn) unawaited(reconnect());
    return true;
  }

  Future<bool> removeExit() async {
    final id = selectedTailnetId;
    return id != null && await removeTailnet(id);
  }

  /// True if this account may own native state, including a restored tunnel
  /// whose account is unknown after relaunch or an unconfirmed stop.
  bool _mayOwnNative(String id) => id == _runningTailnetId ||
      (_runningTailnetId == null && (id == selectedTailnetId ||
          vpnState != VpnConnectionState.disconnected || _nativeStartPending || _shutdownUnconfirmed));

  Future<bool> removeTailnet(String id) async {
    final current = tailnetById(id);
    if (current == null || busy || _accountStoreUnreadable) return false;
    removing = true;
    _invalidateAccount(id);
    notifyListeners();
    try {
      // Let an already submitted selection save publish before deciding which
      // account may be in use. New selection requests are blocked by removing.
      await _serializeAccounts(() async {});
      if (_disposed) return false;
      // An unrelated signed-in account does not own the selected VPN. The old
      // running account can differ from the saved selection during Global's
      // explicit-consent wait, so ownership is tracked independently.
      if ((id == selectedTailnetId || _mayOwnNative(id)) && !await disconnect(force: true)) return false;
      final saved = await _serializeAccounts(() async {
        if (_disposed || tailnetById(id) == null) return false;
        // Persist removal first, even for the last account. If persistence
        // fails, keep every account and selection and do not log anything out.
        return _commitAccounts(_accounts.withoutAccount(id));
      });
      if (!saved) return false;
      if (pickerRequestedTailnetId == id) pickerRequestedTailnetId = null;
      _clearProblem();
      if (!isPreview) {
        final client = _newClient();
        try {
          final identity = await _readLiveIdentity(current);
          await client.logout(identity).timeout(const Duration(seconds: 15));
        } catch (error) {
          _log('warn', 'Tailscale sign-out did not complete: ${describeFailure(error)}');
        } finally {
          await client.close();
        }
        try {
          await TsnetState.clear(_paths.tailscaleStateFor(current));
          await JsonStore.delete(File(_paths.cacheFileFor(current)));
          // Once the authoritative store is safely saved, discard credentials
          // from the old migration file too. Native state was never moved.
          await JsonStore.delete(_paths.exitFile);
        } catch (error) {
          _setProblem(AppProblem.storage, error: error);
          _log('error', 'Tailnet state cleanup failed: ${describeFailure(error)}');
        }
      }
      _log('info', 'Tailnet removed.');
      return true;
    } finally {
      removing = false;
      notifyListeners();
    }
  }

  Future<TailscaleIdentity> _readLiveIdentity(TailnetAccount account) async {
    String? live;
    try {
      live = await TsnetState.readNodePrivateKey(_paths.tailscaleStateFor(account));
    } catch (_) {}
    return live == null || live == account.identity.nodePrivateKey
        ? account.identity : account.identity.copyWith(nodePrivateKey: live);
  }

  Future<TailscaleIdentity> _liveIdentity(TailnetAccount account, {
    required bool Function() cancelled,
  }) async {
    final identity = await _readLiveIdentity(account);
    if (cancelled()) throw const AppFailure(FailureCause.cancelled, 'Account request cancelled.');
    if (identity.nodePrivateKey != account.identity.nodePrivateKey) {
      await _serializeAccounts(() async {
        if (cancelled()) return;
        final current = tailnetById(account.id);
        if (current != null) await _commitAccounts(_accounts.withAccount(current.copyWith(identity: identity)));
      });
    }
    return identity;
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
    final accountId = selectedTailnetId;
    final account = accountId == null ? null : tailnetById(accountId);
    if (current == null || account == null || !canConnect || preparing || active ||
        vpnState == VpnConnectionState.disconnecting) {
      return;
    }
    final connectionSettings = settings;
    final attempt = ++_connectAttempt;
    final epoch = _accountEpoch(account.id);
    bool cancelled() => attempt != _connectAttempt || !_accountCurrent(account.id, epoch) ||
        selectedTailnetId != account.id || exit?.nodeId != current.nodeId;
    final stateDirectory = _paths.tailscaleStateFor(account);
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
        _runningTailnetId = account.id;
        _log('info', 'Connected (simulated).');
        return;
      }

      // A native logout must not be undone using the app's cached identity.
      // Keep the exit selection and let an explicit sign-in replace the state.
      final stateFile = File('${stateDirectory.path}/${TsnetState.stateFileName}');
      if (await stateFile.exists()) {
        final loggedOut = await TsnetState.readLoggedOut(stateDirectory);
        final nodeKey = await TsnetState.readNodePrivateKey(stateDirectory);
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
          final snapshot = await _fetchSnapshot(account,
              timeout: const Duration(seconds: 12), cancelled: cancelled);
          if (cancelled()) return;
          await _serializeAccounts(() async {
            if (cancelled()) return;
            await _commitAccounts(_accounts.withAccount(tailnetById(account.id)!.copyWith(snapshot: snapshot)));
          });
          if (cancelled()) return;
          profile = profile.copyWith(snapshot: snapshot);
          _logDiscovery(account.id, snapshot);
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
          await _serializeAccounts(() async {
            if (cancelled()) return;
            await _commitAccounts(_accounts.select(ExitSelection.fromProfile(account.id, profile)));
          });
          if (cancelled()) return;
        }
      }
      if (profile.nodeAddress.isEmpty) {
        _setProblem(AppProblem.nodeMissing, subject: profile.nodeName);
        return;
      }
      final identity = await _liveIdentity(tailnetById(account.id)!, cancelled: cancelled);
      final snapshot = profile.snapshot;
      await _serializeAccounts(() async {
        if (cancelled()) return;
        if (!await stateFile.exists() && snapshot != null && !cancelled()) {
          await TsnetState.write(stateDirectory: stateDirectory, identity: identity, snapshot: snapshot);
        }
      });
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
          stateDirectory: stateDirectory.path,
          cacheFile: _paths.cacheFileFor(account),
          magicDnsSuffix: profile.snapshot?.magicDnsSuffix ?? '',
        ),
      );
      // A priority stop may retire an unfinished native initialization. The
      // adapter is idempotent, so a later connection can initialize afresh.
      await _vpn.initialize();
      if (cancelled()) return;
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
        _runningTailnetId = account.id;
        await _startVpnBounded(config);
        // A start acknowledgement can arrive after cancellation. Stop inside
        // this same serialized operation before any later start may run.
        if (attempt != _connectAttempt) {
          await _stopVpnConfirmed();
        } else {
          _runningMode = connectionSettings.mode;
          needsReconnect = needsReconnect || settings.affectsConnection(connectionSettings) ||
              selectedTailnetId != account.id || exit?.nodeId != profile.nodeId;
        }
      });
    } catch (error) {
      if (_shutdownUnconfirmed) {
        // The bounded queue is released even if the native acknowledgement is
        // lost. Only a successful native stop barrier can release the latch.
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
    final request = Object();
    _pendingNativeStart = request;
    var timedOut = false;
    try {
      await Future<void>.sync(() => _vpn.start(config)).whenComplete(() {
        // A timeout does not cancel native work. A later confirmed stop can
        // retire this request even if its Flutter reply never arrives.
        if (timedOut && identical(_pendingNativeStart, request)) {
          unawaited(_recoverLateStart(request));
        }
      }).timeout(_nativeCommandTimeout, onTimeout: () {
        timedOut = true;
        throw TimeoutException('The native VPN start did not acknowledge completion.');
      });
    } catch (_) {
      _shutdownUnconfirmed = true;
      rethrow;
    } finally {
      if (!timedOut && identical(_pendingNativeStart, request)) _pendingNativeStart = null;
    }
  }

  Future<void> _recoverLateStart(Object request) async {
    try {
      await _serializeNative(() async {
        // A successful stop may already have retired the old command. Its late
        // acknowledgement must not stop a newer connection or clear its errors.
        if (!identical(_pendingNativeStart, request)) return;
        await _stopVpnConfirmed();
        if (problem == AppProblem.disconnect) _clearProblem();
      });
    } catch (error) {
      _setProblem(AppProblem.disconnect, error: error);
    } finally {
      notifyListeners();
    }
  }

  Future<void> _stopVpnConfirmed() async {
    _shutdownUnconfirmed = true;
    // The pinned native backends order commands and cancel earlier pending
    // starts before acknowledging stop. See VpnService.stop's contract.
    await _vpn.stop().timeout(_nativeCommandTimeout);
    await _vpn.waitFor(VpnConnectionState.disconnected, timeout: _nativeCommandTimeout)
        .timeout(_nativeCommandTimeout);
    final current = await _vpn.currentStatus().timeout(_nativeCommandTimeout);
    _onStatus(current);
    if (current.state != VpnConnectionState.disconnected) {
      throw const AppFailure(FailureCause.unknown,
          'The VPN has not confirmed native shutdown.');
    }
    _pendingNativeStart = null;
    _runningMode = null;
    _runningTailnetId = null;
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
      _runningTailnetId = null;
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
    final saved = await _writeSettings(settings) &&
        await _serializeAccounts(() => _writeAccounts(_accounts));
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

  void _setProblem(AppProblem kind, {Object? error, String? detail, String? subject, String? tailnetId}) {
    problem = kind;
    problemCause = error == null ? null : classifyFailure(error);
    problemDetail = detail ?? (error == null ? null : describeFailure(error));
    problemSubject = subject;
    problemTailnetId = tailnetId ?? (kind == AppProblem.signIn ? _signInTailnetId : selectedTailnetId);
  }

  void _clearProblem() {
    problem = null;
    problemCause = null;
    problemDetail = null;
    problemSubject = null;
    problemTailnetId = null;
  }

  void _logDiscovery(String id, TailnetSnapshot snapshot) {
    // A local account ID and counts only: no keys, peer names or addresses.
    final detail = snapshot.discoveryDiagnostics?.summary ?? 'exit-nodes=${snapshot.exitNodes.length}';
    _log('info', 'Tailnet $id: machine-authorized=${snapshot.machineAuthorized}; $detail');
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

  Future<bool> _writeAccounts(TailnetStore store) async {
    if (isPreview) return true;
    // A corrupt authoritative store must not be overwritten by partial or empty
    // recovery data; in particular retrySave cannot erase unread identities.
    if (_accountStoreUnreadable) return false;
    try {
      await JsonStore.write(_paths.accountsFile, store.toJson());
      return true;
    } catch (error) {
      _setProblem(AppProblem.storage, error: error);
      _log('error', 'Tailnets could not be saved: ${describeFailure(error)}');
      notifyListeners();
      return false;
    }
  }

  /// Called inside the account queue so concurrent refreshes cannot overwrite
  /// a selection, removal, or another account's successful refresh.
  Future<bool> _commitAccounts(TailnetStore store) async {
    if (!await _writeAccounts(store)) return false;
    _accounts = store;
    return true;
  }
}
