package tailsplit

import (
	"context"
	"encoding/json"
	"errors"
	"net/netip"
	"testing"

	"github.com/miekg/dns"
	"github.com/sagernet/sing-box/adapter"
)

func TestSystemDNSAndProxyDNSRemainSeparate(t *testing.T) {
	config, err := loadTestConfig(t, "tailscale:\n  exit_node: 100.64.0.10\nrules:\n  proxy_domains: [blocked.example]\n")
	if err != nil {
		t.Fatal(err)
	}
	if config.DNS.Direct != "system" {
		t.Fatal("default DNS overrides the physical network")
	}
	compiled, err := Compile(config)
	if err != nil {
		t.Fatal(err)
	}
	var native object
	if err := json.Unmarshal(compiled.JSON, &native); err != nil {
		t.Fatal(err)
	}
	servers := native["dns"].(map[string]any)["servers"].([]any)
	direct, proxy := servers[0].(map[string]any), servers[1].(map[string]any)
	if direct["type"] != "local" || direct["detour"] != nil {
		t.Fatal("direct/system DNS is routed through the remote exit")
	}
	if proxy["server"] != "8.8.8.8" || proxy["detour"] != "tailnet" {
		t.Fatal("proxy-side real resolution was replaced by local system DNS")
	}
	if TailscaleEnabled {
		if err := Check(compiled); err != nil {
			t.Fatal("system DNS is not a valid embedded-core configuration:", err)
		}
	}
}

func TestSystemDNSCannotRecurseIntoTheAppsOwnResolver(t *testing.T) {
	for _, value := range []string{
		"100.100.100.100:53", "198.18.0.1:53", "[2001:2::1]:53",
		"172.31.255.2:53", "[fd74:6169:6c73::2]:53",
	} {
		if validateSystemResolvers([]netip.AddrPort{netip.MustParseAddrPort(value)}) == nil {
			t.Fatalf("recursive/tunnel upstream accepted: %s", value)
		}
	}
	if validateSystemResolvers(nil) == nil {
		t.Fatal("missing physical DNS silently selected another provider")
	}
	for _, address := range []string{"192.168.1.1:53", "127.0.0.1:53", "[::1]:53"} {
		if err := validateSystemResolvers([]netip.AddrPort{netip.MustParseAddrPort(address)}); err != nil {
			t.Fatal("an independent LAN/host recursive resolver was rejected:", address, err)
		}
	}
	guard := &guardedLocalDNS{
		DNSTransport: &unusedDNS{},
		check:        func() error { return errors.New("fixture self-recursion detected") },
	}
	if _, err := guard.Exchange(context.Background(), new(dns.Msg)); err == nil {
		t.Fatal("synchronous resolver bypassed loop prevention")
	}
	called := false
	guard.ExchangeAsync(context.Background(), new(dns.Msg), func(_ *dns.Msg, err error) {
		called = err != nil
	})
	if !called {
		t.Fatal("asynchronous resolver bypassed loop prevention")
	}
}

type unusedDNS struct{ adapter.DNSTransport }

func (*unusedDNS) Exchange(context.Context, *dns.Msg) (*dns.Msg, error) {
	panic("recursive DNS query reached its upstream")
}
func (*unusedDNS) ExchangeAsync(context.Context, *dns.Msg, func(*dns.Msg, error)) {
	panic("recursive DNS query reached its upstream")
}
