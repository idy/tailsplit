//go:build !darwin || !cgo

package tailsplit

import (
	"github.com/sagernet/sing-box/dns"
	"github.com/sagernet/sing-box/dns/transport/local"
)

func registerLocalDNS(registry *dns.TransportRegistry) { local.RegisterTransport(registry) }
