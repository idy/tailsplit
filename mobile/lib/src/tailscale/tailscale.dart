/// Pure-Dart Tailscale control-plane client for TailSplit: sign-in, netmap
/// snapshots and the tsnet state handoff to sing-box's `tailscale` endpoint.
library;

export 'control_client.dart' show TailscaleControlClient;
export 'identity.dart' show TailscaleIdentity;
export 'models.dart'
    show
        ControlFailureKind,
        LoginAuthorized,
        LoginNeedsBrowser,
        LoginProgress,
        TailnetPeer,
        TailnetSnapshot,
        TailscaleControlException,
        TailscaleNeedsLogin;
export 'tsnet_state.dart' show TsnetState;
