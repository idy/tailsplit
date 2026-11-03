package tailsplit

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"time"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/adapter/outbound"
	"github.com/sagernet/sing-box/common/dialer"
	C "github.com/sagernet/sing-box/constant"
	"github.com/sagernet/sing-box/log"
	"github.com/sagernet/sing-box/option"
	"github.com/sagernet/sing-box/protocol/direct"
	"github.com/sagernet/sing/common/bufio"
	M "github.com/sagernet/sing/common/metadata"
	"github.com/sagernet/sing/service"
)

const directIPv4Type = "direct-ipv4"

func registerIPv4DirectOutbound(registry *outbound.Registry) {
	outbound.Register[option.DirectOutboundOptions](registry, directIPv4Type,
		func(ctx context.Context, router adapter.Router, logger log.ContextLogger, tag string, options option.DirectOutboundOptions) (adapter.Outbound, error) {
			if options.DomainResolver == nil || options.DomainResolver.Server == "" {
				return nil, errors.New("IPv4 direct outbound requires a domain resolver")
			}
			resolver := *options.DomainResolver
			resolver.Strategy = option.DomainStrategy(C.DomainStrategyIPv4Only)
			options.DomainResolver = &resolver
			base, err := direct.NewOutbound(ctx, router, logger, tag, options)
			if err != nil {
				return nil, err
			}
			dns := service.FromContext[adapter.DNSRouter](ctx)
			transports := service.FromContext[adapter.DNSTransportManager](ctx)
			return &ipv4DirectOutbound{
				Outbound: base.(*direct.Outbound), logger: logger,
				lookupIPv4: func(ctx context.Context, domain string) ([]netip.Addr, error) {
					transport, found := transports.Transport(resolver.Server)
					if !found {
						return nil, errors.New("direct DNS transport unavailable")
					}
					return dns.Lookup(ctx, domain, adapter.DNSQueryOptions{
						Transport: transport, Strategy: C.DomainStrategyIPv4Only,
					})
				},
			}, nil
		})
}

var _ dialer.PacketDialerWithDestination = (*ipv4DirectOutbound)(nil)

// Keep the direct outbound's lifecycle, interface binding and ICMP behavior.
// Override each TCP/UDP entry point used by sing-box's connection manager;
// embedding alone would let its promoted parallel methods bypass recovery.
type ipv4DirectOutbound struct {
	*direct.Outbound
	logger     log.ContextLogger
	lookupIPv4 func(context.Context, string) ([]netip.Addr, error)
}

func (o *ipv4DirectOutbound) recoverDestination(ctx context.Context, destination M.Socksaddr, addresses []netip.Addr) (M.Socksaddr, []netip.Addr, error) {
	var domain string
	if destination.IsDomain() && len(addresses) > 0 {
		addresses = ipv4Addresses(addresses)
		if len(addresses) > 0 {
			return destination, addresses, nil
		}
		domain = destination.Fqdn
	} else if address := destination.Addr; address.Is6() && !address.Is4In6() &&
		address.IsGlobalUnicast() && !address.IsPrivate() && address.Zone() == "" {
		if metadata := adapter.ContextFrom(ctx); metadata != nil {
			domain = metadata.Domain
		}
	} else {
		return destination, addresses, nil
	}
	if err := ctx.Err(); err != nil {
		return destination, nil, err
	}
	if !M.IsDomainName(domain) {
		return destination, nil, errors.New("direct IPv6 is disabled and no recoverable hostname is available")
	}
	resolved, err := o.lookupIPv4(ctx, domain)
	if err != nil {
		return destination, nil, fmt.Errorf("resolve direct IPv4 for %s: %w", domain, err)
	}
	addresses = ipv4Addresses(resolved)
	if len(addresses) == 0 {
		return destination, nil, fmt.Errorf("direct DNS returned no IPv4 address for %s", domain)
	}
	o.logger.DebugContext(ctx, "recover direct IPv6 destination ", destination, " via ", domain, ": ", fmt.Sprint(addresses))
	// The manager still owns the original destination for routing and UDP NAT.
	// Pass a new value to the direct dialer without mutating context metadata.
	return M.Socksaddr{Fqdn: domain, Port: destination.Port}, addresses, nil
}

func ipv4Addresses(addresses []netip.Addr) []netip.Addr {
	var result []netip.Addr
	for _, address := range addresses {
		if address = address.Unmap(); address.Is4() {
			result = append(result, address)
		}
	}
	return result
}

func (o *ipv4DirectOutbound) DialContext(ctx context.Context, network string, destination M.Socksaddr) (net.Conn, error) {
	recovered, addresses, err := o.recoverDestination(ctx, destination, nil)
	if err != nil {
		return nil, err
	}
	if len(addresses) > 0 {
		return o.Outbound.DialParallel(ctx, network, recovered, addresses)
	}
	return o.Outbound.DialContext(ctx, network, destination)
}

func (o *ipv4DirectOutbound) DialParallel(ctx context.Context, network string, destination M.Socksaddr, addresses []netip.Addr) (net.Conn, error) {
	destination, addresses, err := o.recoverDestination(ctx, destination, addresses)
	if err != nil {
		return nil, err
	}
	return o.Outbound.DialParallel(ctx, network, destination, addresses)
}

func (o *ipv4DirectOutbound) DialParallelNetwork(ctx context.Context, network string, destination M.Socksaddr, addresses []netip.Addr, strategy *C.NetworkStrategy, networkType, fallbackNetworkType []C.InterfaceType, fallbackDelay time.Duration) (net.Conn, error) {
	destination, addresses, err := o.recoverDestination(ctx, destination, addresses)
	if err != nil {
		return nil, err
	}
	return o.Outbound.DialParallelNetwork(ctx, network, destination, addresses, strategy, networkType, fallbackNetworkType, fallbackDelay)
}

func (o *ipv4DirectOutbound) ListenPacket(ctx context.Context, destination M.Socksaddr) (net.PacketConn, error) {
	conn, address, err := o.ListenPacketWithDestination(ctx, destination)
	if err != nil {
		return nil, err
	}
	if address.IsValid() {
		return bufio.NewNATPacketConn(bufio.NewPacketConn(conn), M.SocksaddrFrom(address, destination.Port), destination), nil
	}
	return conn, nil
}

// Report the actual IPv4 peer so the connection manager restores the original
// IPv6 address on UDP/QUIC replies. Connected UDP gets it from RemoteAddr.
func (o *ipv4DirectOutbound) ListenPacketWithDestination(ctx context.Context, destination M.Socksaddr) (net.PacketConn, netip.Addr, error) {
	recovered, addresses, err := o.recoverDestination(ctx, destination, nil)
	if err != nil {
		return nil, netip.Addr{}, err
	}
	if len(addresses) > 0 {
		return o.Outbound.ListenSerialNetworkPacket(ctx, recovered, addresses, nil, nil, nil, 0)
	}
	conn, err := o.Outbound.ListenPacket(ctx, destination)
	return conn, netip.Addr{}, err
}

func (o *ipv4DirectOutbound) ListenSerialNetworkPacket(ctx context.Context, destination M.Socksaddr, addresses []netip.Addr, strategy *C.NetworkStrategy, networkType, fallbackNetworkType []C.InterfaceType, fallbackDelay time.Duration) (net.PacketConn, netip.Addr, error) {
	destination, addresses, err := o.recoverDestination(ctx, destination, addresses)
	if err != nil {
		return nil, netip.Addr{}, err
	}
	return o.Outbound.ListenSerialNetworkPacket(ctx, destination, addresses, strategy, networkType, fallbackNetworkType, fallbackDelay)
}
