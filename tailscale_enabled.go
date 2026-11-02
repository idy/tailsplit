//go:build with_gvisor

package tailsplit

import (
	"os"

	"github.com/sagernet/sing-box/adapter/endpoint"
	"github.com/sagernet/sing-box/dns"
	"github.com/sagernet/sing-box/protocol/tailscale"
	"github.com/sagernet/tailscale/envknob"
)

const TailscaleEnabled = true

func init() {
	// The port-80 Noise fast path can finish its handshake and then stall
	// indefinitely behind a middlebox. Upstream provides this HTTPS override
	// for that exact failure (control/controlhttp/client.go). Set it before
	// any clients start, including the installed network service.
	if _, configured := os.LookupEnv("TS_FORCE_NOISE_443"); !configured {
		envknob.Setenv("TS_FORCE_NOISE_443", "true")
	}
}

func registerTailscale(endpoints *endpoint.Registry, transports *dns.TransportRegistry) {
	tailscale.RegisterEndpoint(endpoints)
	tailscale.RegistryTransport(transports)
}
