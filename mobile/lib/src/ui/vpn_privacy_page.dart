import 'package:flutter/cupertino.dart' show CupertinoIcons;
import 'package:flutter/material.dart';
import 'package:url_launcher/url_launcher.dart';

import '../app.dart';
import '../l10n/strings.dart';
import '../state/app_controller.dart' show AppProblem;
import 'native.dart';

/// Matches the privacy URL in the production App Store Connect metadata.
final privacyPolicyUrl = Uri.parse('https://gist.github.com/idy/5167415ea27b82a536851e6fe43ea192');

/// The disclosure is bundled so it is readable before any network access.
/// Settings keeps this same screen available after first-use acceptance.
class VpnPrivacyPage extends StatefulWidget {
  const VpnPrivacyPage({super.key, this.requestConsent = false});

  final bool requestConsent;

  @override
  State<VpnPrivacyPage> createState() => _VpnPrivacyPageState();
}

class _VpnPrivacyPageState extends State<VpnPrivacyPage> {
  bool _saving = false;
  bool _saveFailed = false;

  Future<void> _accept() async {
    if (_saving) return;
    setState(() {
      _saving = true;
      _saveFailed = false;
    });
    final saved = await AppScope.read(context).acceptVpnDisclosure();
    if (!mounted) return;
    setState(() {
      _saving = false;
      _saveFailed = !saved;
    });
  }

  @override
  Widget build(BuildContext context) {
    final s = S.of(context);
    final app = AppScope.of(context);
    return NativePage(
      title: s.vpnPrivacy,
      slivers: [
        SliverToBoxAdapter(
          child: Padding(
            padding: const EdgeInsets.fromLTRB(20, 16, 20, 8),
            child: Text(s.vpnPrivacyIntro),
          ),
        ),
        for (final (title, body) in [
          (s.privacyLocalTitle, s.privacyLocalBody),
          (s.privacyServicesTitle, s.privacyServicesBody),
          (s.privacyTrafficTitle, s.privacyTrafficBody),
          (s.privacyControlsTitle, s.privacyControlsBody),
        ])
          SliverToBoxAdapter(
            child: Padding(
              padding: const EdgeInsets.fromLTRB(20, 16, 20, 0),
              child: Column(
                crossAxisAlignment: CrossAxisAlignment.start,
                children: [
                  Text(title, style: const TextStyle(fontWeight: FontWeight.w600)),
                  const SizedBox(height: 6),
                  Text(body),
                ],
              ),
            ),
          ),
        SliverToBoxAdapter(
          child: NativeSection(
            children: [
              NativeRow(
                leading: const NativeRowIcon(ios: CupertinoIcons.hand_raised, android: Icons.privacy_tip_outlined),
                title: Text(s.privacyPolicy),
                chevron: true,
                onTap: () => launchUrl(privacyPolicyUrl, mode: LaunchMode.inAppBrowserView),
              ),
              NativeRow(
                leading: const NativeRowIcon(ios: CupertinoIcons.link, android: Icons.link),
                title: Text(s.tailscalePrivacyPolicy),
                chevron: true,
                onTap: () => launchUrl(Uri.parse('https://tailscale.com/privacy-policy'), mode: LaunchMode.inAppBrowserView),
              ),
            ],
          ),
        ),
        if (widget.requestConsent)
          SliverToBoxAdapter(
            child: Padding(
              padding: const EdgeInsets.fromLTRB(20, 8, 20, 24),
              child: Column(
                crossAxisAlignment: CrossAxisAlignment.start,
                children: [
                  Text(s.privacyAgreement),
                  if (app.switchOn || app.problem == AppProblem.disconnect) ...[
                    const SizedBox(height: 12),
                    Text(s.existingVpnNotice),
                    TextAction(
                      label: s.disconnectVpn,
                      onPressed: app.canRetryDisconnect ? () => app.disconnect(force: true) : null,
                    ),
                  ],
                  if (_saveFailed) ...[
                    const SizedBox(height: 12),
                    Text(s.privacySaveFailed),
                  ],
                  const SizedBox(height: 16),
                  PrimaryButton(label: s.agreeAndContinue, onPressed: _accept, busy: _saving),
                ],
              ),
            ),
          ),
      ],
    );
  }
}
