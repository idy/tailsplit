import 'dart:async';
import 'dart:io';

import 'package:flutter/services.dart' show PlatformException;

import '../tailscale/tailscale.dart';
import 'rules.dart' show RuleListException;

/// Why an operation failed, so the UI can explain the problem and its
/// recovery in the user's language. Raw details stay in Logs.
enum FailureCause {
  offline,
  timeout,
  tls,
  rateLimited,
  server,
  rejected,
  invalid,
  noBrowser,
  storage,
  cancelled,
  unknown,
}

/// A failure raised by the app itself with a known cause.
class AppFailure implements Exception {
  const AppFailure(this.cause, this.message);
  final FailureCause cause;
  final String message;

  @override
  String toString() => message;
}

FailureCause classifyFailure(Object error) {
  switch (error) {
    case AppFailure(:final cause):
      return cause;
    case SocketException() || HttpException():
      return FailureCause.offline;
    case TimeoutException():
      return FailureCause.timeout;
    case HandshakeException() || TlsException():
      return FailureCause.tls;
    case FileSystemException():
      return FailureCause.storage;
    case RuleListException(:final cause):
      return cause;
    case TailscaleControlException(:final kind):
      return switch (kind) {
        ControlFailureKind.offline => FailureCause.offline,
        ControlFailureKind.timeout => FailureCause.timeout,
        ControlFailureKind.tls => FailureCause.tls,
        ControlFailureKind.rateLimited => FailureCause.rateLimited,
        ControlFailureKind.server => FailureCause.server,
        ControlFailureKind.rejected => FailureCause.rejected,
        ControlFailureKind.cancelled => FailureCause.cancelled,
      };
  }
  return FailureCause.unknown;
}

/// An English diagnostic for Logs. Never contains keys or auth URLs: the
/// control client and plugin already redact them.
String describeFailure(Object error) => switch (error) {
      AppFailure(:final message) => message,
      RuleListException(:final message) => message,
      TailscaleControlException(:final message) => message,
      PlatformException(:final message, :final code) => message ?? code,
      SocketException(:final osError, :final message) => osError?.message ?? message,
      FileSystemException(:final osError, :final message) => osError?.message ?? message,
      TimeoutException() => 'timed out',
      _ => error is Exception || error is Error
          ? error.toString().replaceFirst(RegExp(r'^\w*(Exception|Error): ?'), '')
          : 'unexpected error',
    };
