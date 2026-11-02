package tailsplit

import (
	"context"
	"errors"
	"net/netip"

	"github.com/miekg/dns"
	"github.com/sagernet/sing-box/adapter"
)

// The core's local resolver selects macOS's physical-interface scoped DNS
// servers. Reject this app's virtual TUN resolver; independent host resolvers
// on loopback remain usable now that TailSplit owns no host DNS socket.
type guardedLocalDNS struct {
	adapter.DNSTransport
	check func() error
}

func (t *guardedLocalDNS) Exchange(ctx context.Context, message *dns.Msg) (*dns.Msg, error) {
	if err := t.check(); err != nil {
		return nil, err
	}
	return t.DNSTransport.Exchange(ctx, message)
}

func (t *guardedLocalDNS) ExchangeAsync(ctx context.Context, message *dns.Msg, callback func(*dns.Msg, error)) {
	if err := t.check(); err != nil {
		callback(nil, err)
		return
	}
	t.DNSTransport.ExchangeAsync(ctx, message, callback)
}

func (t *guardedLocalDNS) Environment() []string {
	return t.DNSTransport.(adapter.DNSTransportWithEnvironment).Environment()
}

func (t *guardedLocalDNS) PreferredDomain(domain string) bool {
	return t.DNSTransport.(adapter.DNSTransportWithPreferredDomain).PreferredDomain(domain)
}

func validateSystemResolvers(servers []netip.AddrPort) error {
	if len(servers) == 0 {
		return errors.New("the physical network has no scoped DNS servers; set dns.direct explicitly")
	}
	for _, server := range servers {
		address := server.Addr().Unmap()
		if !server.IsValid() || server.Port() == 0 ||
			address.IsUnspecified() || address.IsMulticast() || isTUNAddress(address) ||
			address.Unmap() == netip.MustParseAddr("100.100.100.100") ||
			netip.MustParsePrefix("198.18.0.0/15").Contains(address) ||
			netip.MustParsePrefix("2001:2::/48").Contains(address) {
			return errors.New("system DNS points to a reserved or TailSplit TUN address; set dns.direct to a real upstream")
		}
	}
	return nil
}
