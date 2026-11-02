package tailsplit

import (
	"bytes"
	"context"
	"crypto/tls"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	box "github.com/sagernet/sing-box"
	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/adapter/outbound"
	boxsocks "github.com/sagernet/sing-box/protocol/socks"
	M "github.com/sagernet/sing/common/metadata"
	N "github.com/sagernet/sing/common/network"
	"github.com/sagernet/sing/protocol/socks"
	"github.com/sagernet/sing/service"
)

// The deterministic Initial is from sing-box v1.14.4's GPL-3.0
// common/sniff.TestSniffUQUICChrome115. Its SNI is www.google.com; the fixture
// resolves it only through a loopback DNS server and never contacts Google.
//
//go:embed testdata/quic-chrome115.hex
var ipv4RecoveryQUICInitial string

func TestDirectIPv4RecoveryPreservesRoutingPriority(t *testing.T) {
	peer := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = fmt.Fprint(w, "fixture-only")
	}))
	defer peer.Close()
	proxyAddress, _, proxied := startDirectIPv4Fixture(t, peer.Listener.Addr().String(), nil)
	proxyURL, _ := url.Parse("http://" + proxyAddress)
	_, port, _ := net.SplitHostPort(peer.Listener.Addr().String())
	for _, test := range []struct {
		name, address, domain string
		proxy                 bool
	}{
		{"cached IPv6", "2001:db8::10", "voice.test", false},
		{"proxy domain", "2001:db8::10", "proxy.test", true},
		{"proxy CIDR", "2001:db8:1::10", "voice.test", true},
		{"direct exception", "2001:db8:1::10", "safe.proxy.test", false},
		{"ordinary IPv4", "127.0.0.1", "voice.test", false},
	} {
		t.Run(test.name, func(t *testing.T) {
			transport := &http.Transport{
				Proxy: http.ProxyURL(proxyURL), DisableKeepAlives: true,
				TLSClientConfig: &tls.Config{InsecureSkipVerify: true, ServerName: test.domain},
			}
			defer transport.CloseIdleConnections()
			client := &http.Client{Transport: transport, Timeout: 3 * time.Second}
			getFixture(t, client, "https://"+net.JoinHostPort(test.address, port)+"/")
			if test.proxy {
				select {
				case target := <-proxied:
					if target != test.address {
						t.Fatalf("proxy lost the original CIDR destination: %s", target)
					}
				case <-time.After(time.Second):
					t.Fatal("proxy-selected traffic was sent directly")
				}
			} else {
				select {
				case target := <-proxied:
					t.Fatalf("direct recovery used the proxy: %s", target)
				default:
				}
			}
		})
	}
}

func TestDirectIPv4RecoveryThroughApplicationProxy(t *testing.T) {
	peer := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = fmt.Fprint(w, "fixture-only")
	}))
	defer peer.Close()
	proxyAddress, _, proxied := startDirectIPv4InboundFixture(t, peer.Listener.Addr().String(), "proxy-in", nil)
	proxyURL, _ := url.Parse("http://" + proxyAddress)
	_, port, _ := net.SplitHostPort(peer.Listener.Addr().String())
	for _, scheme := range []string{"http", "socks5"} {
		t.Run(scheme, func(t *testing.T) {
			proxyURL.Scheme = scheme
			transport := &http.Transport{
				Proxy: http.ProxyURL(proxyURL), DisableKeepAlives: true,
				TLSClientConfig: &tls.Config{InsecureSkipVerify: true, ServerName: "voice.test"},
			}
			defer transport.CloseIdleConnections()
			getFixture(t, &http.Client{Transport: transport, Timeout: 3 * time.Second},
				"https://"+net.JoinHostPort("2001:db8::10", port)+"/")
			select {
			case target := <-proxied:
				t.Fatalf("application proxy recovery used the exit: %s", target)
			default:
			}
		})
	}
}

func TestDirectIPv4QUICRecoveryPreservesReplyAddress(t *testing.T) {
	for _, test := range []struct {
		inbound   string
		connected bool
	}{{"tun-in", false}, {"tun-in", true}, {"proxy-in", false}, {"proxy-in", true}} {
		t.Run(fmt.Sprintf("%s/connected=%v", test.inbound, test.connected), func(t *testing.T) {
			peer := startIPv4EchoPackets(t)
			proxyAddress, _, proxied := startDirectIPv4InboundFixture(t, peer.LocalAddr().String(), test.inbound, func(native object) {
				if test.connected {
					route := native["route"].(map[string]any)
					route["rules"] = append([]any{object{
						"inbound": []string{test.inbound}, "network": "udp",
						"action": "route-options", "udp_connect": true,
					}}, route["rules"].([]any)...)
				}
			})
			_, portText, _ := net.SplitHostPort(peer.LocalAddr().String())
			port, _ := strconv.Atoi(portText)
			target := M.SocksaddrFrom(netip.MustParseAddr("2001:db8::20"), uint16(port))
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			client := socks.NewClient(N.SystemDialer, M.ParseSocksaddr(proxyAddress), socks.Version5, "", "")
			packets, err := client.ListenPacket(ctx, target)
			if err != nil {
				t.Fatal(err)
			}
			defer packets.Close()
			_ = packets.SetDeadline(time.Now().Add(3 * time.Second))
			initial, err := hex.DecodeString(strings.TrimSpace(ipv4RecoveryQUICInitial))
			if err != nil {
				t.Fatal(err)
			}
			for _, payload := range [][]byte{initial, []byte("next voice packet")} {
				if _, err := packets.WriteTo(payload, target.UDPAddr()); err != nil {
					t.Fatal(err)
				}
				buffer := make([]byte, 4096)
				n, source, err := packets.ReadFrom(buffer)
				if err != nil {
					t.Fatal("cached IPv6 QUIC did not reach the IPv4 peer:", err)
				}
				if !bytes.Equal(buffer[:n], payload) || M.SocksaddrFromNet(source) != target {
					t.Fatalf("UDP payload or original IPv6 source was lost: source=%s", source)
				}
			}
			select {
			case target := <-proxied:
				t.Fatalf("QUIC recovery used the proxy: %s", target)
			default:
			}
		})
	}
}

func startDirectIPv4Fixture(t *testing.T, target string, edit func(object)) (string, *box.Box, <-chan string) {
	t.Helper()
	return startDirectIPv4InboundFixture(t, target, "tun-in", edit)
}

func startDirectIPv4InboundFixture(t *testing.T, target, inbound string, edit func(object)) (string, *box.Box, <-chan string) {
	t.Helper()
	hosts, stop, socksAddress := startSOCKSPeer(t, target)
	t.Cleanup(stop)
	dnsAddress := startDNSPeer(t)
	proxyAddress := unusedTCPAddress(t)
	config, err := loadTestConfig(t, fmt.Sprintf(`
state_dir: ./state
listen: %s
log_level: error
tailscale:
  exit_node: 100.64.0.10
dns:
  direct: %s
  proxy: %s
  direct_ipv6: false
rules:
  proxy_domains: [proxy.test]
  proxy_ips: [2001:db8:1::/48]
  direct_domains: [safe.proxy.test, www.google.com]
tun:
  enabled: true
`, proxyAddress, dnsAddress, dnsAddress))
	if err != nil {
		t.Fatal(err)
	}
	config.TUN.Enabled = inbound == "tun-in"
	compiled, err := Compile(config)
	if err != nil {
		t.Fatal(err)
	}
	var native object
	if err := json.Unmarshal(compiled.JSON, &native); err != nil {
		t.Fatal(err)
	}
	// Keep the real TUN routing policy, but use only unprivileged loopback
	// listeners and a SOCKS fixture instead of changing the host network.
	native["inbounds"] = []object{listener("mixed", inbound, proxyAddress)}
	delete(native, "endpoints")
	host, portText, _ := net.SplitHostPort(socksAddress)
	port, _ := strconv.Atoi(portText)
	native["outbounds"] = append(native["outbounds"].([]any), object{
		"type": "socks", "tag": "tailnet", "server": host, "server_port": port,
	})
	if edit != nil {
		edit(native)
	}
	compiled.JSON, _ = json.Marshal(native)
	if err := privateState(config.StateDir); err != nil {
		t.Fatal(err)
	}
	ctx := engineContext(context.Background())
	boxsocks.RegisterOutbound(service.FromContext[adapter.OutboundRegistry](ctx).(*outbound.Registry))
	options, err := parseNative(ctx, compiled.JSON)
	if err != nil {
		t.Fatal(err)
	}
	instance, err := box.New(box.Options{Context: ctx, Options: options})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = instance.Close() })
	if err := instance.Start(); err != nil {
		t.Fatal(err)
	}
	return proxyAddress, instance, hosts
}
