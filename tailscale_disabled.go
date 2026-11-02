//go:build !with_gvisor

package tailsplit

import (
	"context"
	"errors"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/adapter/endpoint"
	"github.com/sagernet/sing-box/dns"
	"github.com/sagernet/sing-box/log"
	"github.com/sagernet/sing-box/option"
)

const TailscaleEnabled = false

func registerTailscale(endpoints *endpoint.Registry, transports *dns.TransportRegistry) {
	message := errors.New("Tailscale requires the with_gvisor build tag; build with make build")
	endpoint.Register[option.TailscaleEndpointOptions](endpoints, "tailscale",
		func(context.Context, adapter.Router, log.ContextLogger, string, option.TailscaleEndpointOptions) (adapter.Endpoint, error) {
			return nil, message
		})
	dns.RegisterTransport[option.TailscaleDNSServerOptions](transports, "tailscale",
		func(context.Context, log.ContextLogger, string, option.TailscaleDNSServerOptions) (adapter.DNSTransport, error) {
			return nil, message
		})
}
