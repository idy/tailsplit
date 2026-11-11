import sing_box_flutter

/// TailSplit's Packet Tunnel entry point. sing-box, its TUN and the Tailscale
/// endpoint run here; Flutter stays in Runner.
final class PacketTunnelProvider: SingBoxPacketTunnelProvider {}
