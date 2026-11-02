package tailsplit

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	box "github.com/sagernet/sing-box"
	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/adapter/outbound"
	"github.com/sagernet/sing-box/protocol/socks"
	"github.com/sagernet/sing/service"
	"golang.org/x/net/dns/dnsmessage"
)

func TestGlobalRoutingOverridesDirectExceptionsAndFailsClosed(t *testing.T) {
	var served, directQueries atomic.Int32
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		served.Add(1)
		_, _ = fmt.Fprint(w, "fixture-only")
	}))
	defer peer.Close()
	hosts, stopSOCKS, socksAddress := startSOCKSPeer(t, peer.Listener.Addr().String())
	defer stopSOCKS()
	directDNS := startDNSPeerWithQueries(t, &directQueries)
	proxyAddress, dnsListen := unusedTCPAddress(t), unusedTCPAddress(t)
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
  mode: global
  final: direct
  gfwlist: ./not-needed-in-global.txt
  direct_domains: [safe.proxy.test]
  direct_ips: [127.0.0.0/8]
tun:
  enabled: true
`, proxyAddress, directDNS, directDNS))
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
	// Preserve the complete global policy but replace the kernel TUN with
	// loopback and the encrypted peer with a controllable SOCKS transport.
	native["inbounds"] = []object{
		listener("mixed", "proxy-in", proxyAddress),
		dnsFixtureInbound(dnsListen),
	}
	delete(native, "endpoints")
	host, port, _ := net.SplitHostPort(socksAddress)
	number, _ := strconv.Atoi(port)
	native["outbounds"] = []object{{
		"type": "socks", "tag": "tailnet", "server": host, "server_port": number,
	}}
	native["dns"].(map[string]any)["timeout"] = "200ms"
	compiled.JSON, _ = json.Marshal(native)
	if err := privateState(config.StateDir); err != nil {
		t.Fatal(err)
	}
	ctx := engineContext(context.Background())
	socks.RegisterOutbound(service.FromContext[adapter.OutboundRegistry](ctx).(*outbound.Registry))
	options, err := parseNative(ctx, compiled.JSON)
	if err != nil {
		t.Fatal(err)
	}
	instance, err := box.New(box.Options{Context: ctx, Options: options})
	if err != nil {
		t.Fatal(err)
	}
	if err := instance.Start(); err != nil {
		t.Fatal(err)
	}
	defer instance.Close()

	fake := queryA(t, dnsListen, "safe.proxy.test")
	if !netip.MustParsePrefix("198.18.0.0/15").Contains(fake) {
		t.Fatal("a saved direct exception still uses physical DNS")
	}
	v6 := queryDNS(t, dnsListen, "ordinary.test", dnsmessage.TypeAAAA)
	if len(v6.Answers) == 0 {
		t.Fatal("direct_ipv6 suppressed globally proxied IPv6")
	}
	proxyURL, _ := url.Parse("http://" + proxyAddress)
	transport := &http.Transport{Proxy: http.ProxyURL(proxyURL), DisableKeepAlives: true}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 3 * time.Second}
	_, targetPort, _ := net.SplitHostPort(peer.Listener.Addr().String())
	addresses := []string{
		"http://" + net.JoinHostPort(fake.String(), targetPort) + "/",
		"http://" + net.JoinHostPort("ordinary.test", targetPort) + "/",
		peer.URL,
	}
	for _, address := range addresses {
		getFixture(t, client, address)
		select {
		case <-hosts:
		case <-time.After(time.Second):
			t.Fatal("a direct exception, private IP or ordinary domain bypassed the exit")
		}
	}
	// This SOCKS fixture rejects UDP. A non-FakeIP DNS query must fail via
	// that exit, even while the physical resolver remains reachable.
	expectFailedDNS(t, dnsListen)
	if directQueries.Load() != 0 {
		t.Fatal("client DNS leaked to the physical resolver")
	}
	stopSOCKS()
	before := served.Load()
	for _, address := range addresses {
		response, err := client.Get(address)
		if err == nil {
			response.Body.Close()
			if response.StatusCode == http.StatusOK {
				t.Fatal("global traffic fell back to a directly reachable target")
			}
		}
	}
	if served.Load() != before || directQueries.Load() != 0 {
		t.Fatal("exit failure caused direct HTTP or DNS traffic")
	}
}

func expectFailedDNS(t *testing.T, address string) {
	t.Helper()
	name, _ := dnsmessage.NewName("no-direct-dns.test.")
	query := dnsmessage.Message{
		Header: dnsmessage.Header{ID: 456, RecursionDesired: true},
		Questions: []dnsmessage.Question{{
			Name: name, Type: dnsmessage.TypeTXT, Class: dnsmessage.ClassINET,
		}},
	}
	wire, _ := query.Pack()
	connection, err := net.DialTimeout("udp", address, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close()
	_ = connection.SetDeadline(time.Now().Add(time.Second))
	if _, err := connection.Write(wire); err != nil {
		t.Fatal(err)
	}
	buffer := make([]byte, 4096)
	n, err := connection.Read(buffer)
	if err != nil {
		if timeout, ok := err.(net.Error); ok && timeout.Timeout() {
			return // The DNS hijacker may drop a failed exchange.
		}
		t.Fatal(err)
	}
	var answer dnsmessage.Message
	if err := answer.Unpack(buffer[:n]); err != nil {
		t.Fatal(err)
	}
	if answer.RCode == dnsmessage.RCodeSuccess {
		t.Fatal("an unavailable exit DNS transport unexpectedly succeeded")
	}
}
