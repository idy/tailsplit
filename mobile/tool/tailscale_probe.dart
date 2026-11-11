// Live probe against real Tailscale control (no account needed):
//
//   dart run tool/tailscale_probe.dart [--control-url URL]
//
// Generates a throwaway identity, runs login() until control hands out a
// sign-in link, cancels, then shows how control answers a netmap request for
// that unapproved key. Prints only the link's host, never the link or keys.

import 'dart:async';
import 'dart:io';

import 'package:tailsplit_mobile/src/tailscale/tailscale.dart';

Future<void> main(List<String> args) async {
  final i = args.indexOf('--control-url');
  final controlUrl = i >= 0 && i + 1 < args.length ? Uri.parse(args[i + 1]) : null;
  final identity = await TailscaleIdentity.generate(hostname: 'tailsplit-probe');
  final client = TailscaleControlClient(controlUrl: controlUrl, os: 'iOS');
  stdout.writeln('control: ${client.controlUrl}');

  final watch = Stopwatch()..start();
  final first = Completer<LoginProgress>();
  final sub = client.login(identity).listen(
    (p) {
      if (!first.isCompleted) first.complete(p);
    },
    onError: (Object e) {
      if (!first.isCompleted) first.completeError(e);
    },
  );
  var ok = false;
  try {
    final progress = await first.future.timeout(const Duration(seconds: 60));
    switch (progress) {
      case LoginNeedsBrowser(:final authUrl):
        ok = true;
        stdout.writeln('login(): auth URL received after ${watch.elapsedMilliseconds} ms '
            '(scheme ${authUrl.scheme}, host ${authUrl.host})');
      case LoginAuthorized():
        stdout.writeln('login(): unexpectedly authorized without a browser step');
    }
  } catch (e) {
    stdout.writeln('login() failed: $e');
  } finally {
    await sub.cancel();
    stdout.writeln('login(): subscription cancelled');
  }

  // How control answers a read-only map request for a key nobody approved.
  try {
    final snapshot = await client.fetchSnapshot(identity);
    stdout.writeln('fetchSnapshot(): unexpected snapshot $snapshot');
  } on TailscaleNeedsLogin catch (e) {
    stdout.writeln('fetchSnapshot() for the unapproved key: TailscaleNeedsLogin (${e.message})');
  } catch (e) {
    stdout.writeln('fetchSnapshot() for the unapproved key: $e');
  }
  await client.close();
  exit(ok ? 0 : 1);
}
