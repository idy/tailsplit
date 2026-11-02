package tailsplit

import (
	"net/netip"
)

// The DNS peer lives inside the TUN stack, not on a host socket. Use a
// TailSplit subnet rather than sing-box's common 172.19.0.0/30 default.
var (
	tunIPv4 = netip.MustParsePrefix("172.31.255.1/30")
	tunIPv6 = netip.MustParsePrefix("fd74:6169:6c73::1/126")
)

func tunAddresses() []string {
	return []string{tunIPv4.String(), tunIPv6.String()}
}

func tunDNSAddresses() []string {
	return []string{tunIPv4.Addr().Next().String(), tunIPv6.Addr().Next().String()}
}

func systemDNSListen() string {
	// macOS traditional DNS tools use port 53 regardless of ServerPort.
	return netip.AddrPortFrom(tunIPv4.Addr().Next(), 53).String()
}

func isTUNAddress(address netip.Addr) bool {
	address = address.Unmap()
	return tunIPv4.Contains(address) || tunIPv6.Contains(address)
}

func validSystemDNSListener(address netip.AddrPort) bool {
	return address.Port() == 53 && (address.Addr() == tunIPv4.Addr().Next() ||
		address.Addr() == tunIPv6.Addr().Next())
}
