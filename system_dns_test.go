package tailsplit

import (
	"context"
	"encoding/json"
	"fmt"
	"net/netip"
	"runtime"
	"slices"
	"testing"

	"github.com/sagernet/sing-box/option"
	singtun "github.com/sagernet/sing-tun"
)

func TestDNSUsesTUNWithoutHostDNSListenersInBothModes(t *testing.T) {
	for _, capture := range []bool{false, true} {
		t.Run(fmt.Sprintf("capture=%t", capture), func(t *testing.T) {
			config, err := loadTestConfig(t, fmt.Sprintf(`
state_dir: ./state
dns:
  listen: 127.0.0.1:15353
tun:
  enabled: %t
`, capture))
			if err != nil {
				t.Fatal(err)
			}
			compiled, err := Compile(config)
			if err != nil {
				t.Fatal(err)
			}
			var native object
			if err := json.Unmarshal(compiled.JSON, &native); err != nil {
				t.Fatal(err)
			}
			inbounds := native["inbounds"].([]any)
			if len(inbounds) != 2 || inbounds[0].(map[string]any)["type"] != "mixed" ||
				inbounds[1].(map[string]any)["type"] != "tun" {
				t.Fatal("a host DNS listener was created:", inbounds)
			}
			// Exercise sing-tun's real route expansion, including macOS's
			// connected-subnet routes, without opening a kernel interface.
			options, err := parseNative(engineContext(context.Background()), compiled.JSON)
			if err != nil {
				t.Fatal(err)
			}
			tunConfig := options.Inbounds[1].Options.(*option.TunInboundOptions)
			nativeRoutes := singtun.Options{AutoRoute: tunConfig.AutoRoute, StrictRoute: tunConfig.StrictRoute}
			for _, prefix := range tunConfig.Address {
				if prefix.Addr().Is4() {
					nativeRoutes.Inet4Address = append(nativeRoutes.Inet4Address, prefix)
				} else {
					nativeRoutes.Inet6Address = append(nativeRoutes.Inet6Address, prefix)
				}
			}
			for _, prefix := range tunConfig.RouteAddress {
				if prefix.Addr().Is4() {
					nativeRoutes.Inet4RouteAddress = append(nativeRoutes.Inet4RouteAddress, prefix)
				} else {
					nativeRoutes.Inet6RouteAddress = append(nativeRoutes.Inet6RouteAddress, prefix)
				}
			}
			ranges, err := nativeRoutes.BuildAutoRouteRanges(false)
			if err != nil {
				t.Fatal(err)
			}
			if runtime.GOOS == "darwin" {
				// The interface monitor and local DNS transport select the
				// physical /0. Capture mode must leave that route intact,
				// including when strict_route is enabled.
				for _, prefix := range ranges {
					if prefix.Bits() == 0 {
						t.Fatal("TUN would replace the physical default used by system DNS:", ranges)
					}
				}
			}
			capturesInternet := slices.ContainsFunc(ranges, func(prefix netip.Prefix) bool {
				return prefix.Contains(netip.MustParseAddr("8.8.8.8"))
			})
			if capturesInternet != capture {
				t.Fatal("Internet route coverage does not match the selected mode:", ranges)
			}
			for _, address := range tunDNSAddresses() {
				if !slices.ContainsFunc(ranges, func(prefix netip.Prefix) bool {
					return prefix.Contains(netip.MustParseAddr(address))
				}) {
					t.Fatal("the DNS peer is not routed into TUN:", address, ranges)
				}
			}
			if tunConfig.DNSMode != map[bool]string{true: "hijack", false: "disabled"}[capture] {
				t.Fatal("proxy-only mode can publish a system DNS override")
			}
			rules := native["route"].(map[string]any)["rules"].([]any)
			dnsRule := rules[0].(map[string]any)
			if dnsRule["action"] != "hijack-dns" || dnsRule["port"] != float64(53) {
				t.Fatal("TUN DNS requests bypass the DNS policy")
			}
			if !capture && rules[1].(map[string]any)["action"] != "reject" {
				t.Fatal("DNS-only TUN can forward other client traffic")
			}
			engine, err := Open(context.Background(), config, compiled)
			if err != nil {
				t.Fatal(err)
			}
			defer engine.Close()
			if !engine.hasTUN || (engine.dnsListen != "") != capture {
				t.Fatal("interface creation and system DNS publishing are coupled")
			}
			// No Start call: these tests never change host routes or DNS.
		})
	}
}

func TestSystemDNSOnlyPublishesTheVirtualPeer(t *testing.T) {
	for _, address := range []string{
		systemDNSListen(), netip.AddrPortFrom(tunIPv6.Addr().Next(), 53).String(),
	} {
		if !validSystemDNSListener(netip.MustParseAddrPort(address)) {
			t.Fatal("valid virtual DNS peer rejected:", address)
		}
	}
	for _, address := range []string{
		"127.0.0.1:53", "[::1]:53", "0.0.0.0:53", "8.8.8.8:53",
		netip.AddrPortFrom(tunIPv4.Addr(), 53).String(),
		netip.AddrPortFrom(tunIPv4.Addr().Next(), 15353).String(),
	} {
		if validSystemDNSListener(netip.MustParseAddrPort(address)) {
			t.Fatal("system DNS can point outside the TUN's port-53 peer:", address)
		}
	}
}
