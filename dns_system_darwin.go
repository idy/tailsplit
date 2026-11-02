//go:build darwin && cgo

package tailsplit

import (
	"context"
	"errors"
	"strings"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/dns"
	"github.com/sagernet/sing-box/dns/transport/local"
	"github.com/sagernet/sing-box/log"
	"github.com/sagernet/sing-box/option"
	"github.com/sagernet/sing-tun/dnsinfo"
	"github.com/sagernet/sing/service"
)

func registerLocalDNS(registry *dns.TransportRegistry) {
	dns.RegisterTransport[option.LocalDNSServerOptions](registry, "local",
		func(ctx context.Context, logger log.ContextLogger, tag string, options option.LocalDNSServerOptions) (adapter.DNSTransport, error) {
			transport, err := local.NewTransport(ctx, logger, tag, options)
			if err != nil {
				return nil, err
			}
			manager := service.FromContext[adapter.NetworkManager](ctx)
			return &guardedLocalDNS{DNSTransport: transport, check: func() error {
				monitor := manager.InterfaceMonitor()
				if monitor == nil {
					return errors.New("system DNS is waiting for a physical default interface")
				}
				iface := monitor.DefaultInterface()
				if iface == nil {
					return errors.New("system DNS is waiting for a physical default interface")
				}
				// Darwin auto-route captures traffic with more-specific
				// subranges and preserves the physical /0 used by this
				// monitor and the core's local resolver. A virtual default
				// must not make the recursion guard validate another NIC's
				// DNS while the wrapped resolver still selects the tunnel.
				if strings.HasPrefix(iface.Name, "utun") || strings.HasPrefix(iface.Name, "tun") ||
					strings.HasPrefix(iface.Name, "lo") {
					return errors.New("system DNS cannot use a tunnel as its physical upstream")
				}
				if options.BindInterface != "" && options.BindInterface != iface.Name {
					return errors.New("system DNS and interface refer to different networks; set dns.direct explicitly")
				}
				config := dnsinfo.Copy()
				if config == nil {
					return errors.New("could not read physical network DNS configuration")
				}
				// Match the core's resolver selection, rejecting a fallback
				// to our own supplemental TUN resolver.
				return validateSystemResolvers(config.Select(iface.Index).Servers)
			}}, nil
		})
}
