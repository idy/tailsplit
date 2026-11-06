package tailsplit

import (
	"context"
	"crypto/tls"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"math/rand/v2"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"strconv"
	"sync"
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

func TestTUNDomainRulesRecoverTLSNamesFromCachedRealIPs(t *testing.T) {
	httpPeer := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprint(w, "fixture-only")
	}))
	defer httpPeer.Close()
	target := httpPeer.Listener.Addr().String()
	hosts, stopSOCKS, socksAddress := startSOCKSPeer(t, target)
	defer stopSOCKS()
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
rules:
  proxy_domains: [proxy.test]
  direct_domains: [safe.proxy.test]
tun:
  enabled: true
`, proxyAddress, dnsAddress, dnsAddress))
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
	// Exercise the exact TUN route policy through a loopback mixed listener.
	// No kernel TUN or system DNS changes are used by this test.
	native["inbounds"] = []object{listener("mixed", "tun-in", proxyAddress)}
	delete(native, "endpoints")
	socksHost, socksPort, _ := net.SplitHostPort(socksAddress)
	port, _ := strconv.Atoi(socksPort)
	native["outbounds"] = append(native["outbounds"].([]any), object{
		"type": "socks", "tag": "tailnet", "server": socksHost, "server_port": port,
	})
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
	proxyURL, _ := url.Parse("http://" + proxyAddress)
	request := func(serverName string) {
		t.Helper()
		transport := &http.Transport{
			Proxy: http.ProxyURL(proxyURL), DisableKeepAlives: true,
			// The fixture certificate is self-signed; the real production
			// engine does not override or decrypt TLS.
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true, ServerName: serverName},
		}
		defer transport.CloseIdleConnections()
		client := &http.Client{Transport: transport, Timeout: 3 * time.Second}
		getFixture(t, client, "https://"+target+"/")
	}
	request("proxy.test")
	select {
	case <-hosts:
	case <-time.After(time.Second):
		t.Fatal("cached real-IP flow bypassed its domain-selected proxy")
	}
	request("safe.proxy.test")
	select {
	case name := <-hosts:
		t.Fatalf("sniffed direct exception incorrectly used proxy: %q", name)
	default:
	}
}

// Exercise the actual embedded router, mixed inbound, fake-IP persistence and
// DNS transports. Only the Tailnet transport is substituted with a loopback
// SOCKS peer; this fixture never authenticates or contacts a real Tailnet.
func TestEmbeddedRoutingFakeDNSAndNoDirectFallback(t *testing.T) {
	var served atomic.Int32
	httpPeer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		served.Add(1)
		_, _ = fmt.Fprint(w, "fixture-only")
	}))
	defer httpPeer.Close()
	dnsAddress := startDNSPeer(t)
	target := httpPeer.Listener.Addr().String()
	hosts, stopSOCKS, socksAddress := startSOCKSPeer(t, target)
	defer stopSOCKS()

	proxyAddress := unusedTCPAddress(t)
	dnsListen := unusedTCPAddress(t)
	config, err := loadTestConfig(t, fmt.Sprintf(`
state_dir: ./state
listen: %s
log_level: error
tailscale:
  exit_node: 100.64.0.10
dns:
  direct: %s
  direct_ipv6: false
  proxy: %s
  fake_ip: true
rules:
  proxy_domains: [proxy.test]
  direct_domains: [safe.proxy.test]
`, proxyAddress, dnsAddress, dnsAddress))
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
	// Replace the kernel DNS TUN with an unprivileged loopback fixture,
	// preserving its inbound identity and virtual port-53 destination.
	native["inbounds"] = []object{
		listener("mixed", "proxy-in", proxyAddress), dnsFixtureInbound(dnsListen),
	}
	delete(native, "endpoints")
	socksHost, socksPort, _ := net.SplitHostPort(socksAddress)
	port, _ := strconv.Atoi(socksPort)
	native["outbounds"] = append(native["outbounds"].([]any), object{
		"type": "socks", "tag": "tailnet", "server": socksHost, "server_port": port,
	})
	compiled.JSON, _ = json.Marshal(native)
	if err := privateState(config.StateDir); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ctx = engineContext(ctx)
	registry := service.FromContext[adapter.OutboundRegistry](ctx).(*outbound.Registry)
	socks.RegisterOutbound(registry)
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

	fake := queryA(t, dnsListen, "proxy.test")
	if !netip.MustParsePrefix("198.18.0.0/15").Contains(fake) {
		t.Fatalf("proxy domain got a real address: %s", fake)
	}
	real := queryA(t, dnsListen, "safe.proxy.test")
	if real != netip.MustParseAddr("127.0.0.1") {
		t.Fatalf("direct exception was fake-resolved: %s", real)
	}
	for _, domain := range []string{"safe.proxy.test", "ordinary-direct.test"} {
		answer := queryDNS(t, dnsListen, domain, dnsmessage.TypeAAAA)
		if answer.RCode != dnsmessage.RCodeSuccess || len(answer.Answers) != 0 {
			t.Fatalf("direct name %s still advertises unreachable IPv6: %+v", domain, answer)
		}
	}
	proxyIPv6 := queryDNS(t, dnsListen, "proxy.test", dnsmessage.TypeAAAA)
	foundProxyIPv6 := false
	for _, answer := range proxyIPv6.Answers {
		if body, ok := answer.Body.(*dnsmessage.AAAAResource); ok &&
			netip.MustParsePrefix("2001:2::/48").Contains(netip.AddrFrom16(body.AAAA)) {
			foundProxyIPv6 = true
		}
	}
	if !foundProxyIPv6 {
		t.Fatalf("the direct IPv6 policy disabled proxied IPv6: %+v", proxyIPv6)
	}
	proxyURL, _ := url.Parse("http://" + proxyAddress)
	transport := &http.Transport{Proxy: http.ProxyURL(proxyURL), DisableKeepAlives: true}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 5 * time.Second}
	_, targetPort, _ := net.SplitHostPort(target)
	requestURL := "http://" + net.JoinHostPort(fake.String(), targetPort) + "/"
	getFixture(t, client, requestURL)
	select {
	case host := <-hosts:
		if host != "proxy.test" {
			t.Fatalf("fake-IP domain was not restored before proxying: %q", host)
		}
	case <-time.After(time.Second):
		t.Fatal("proxied flow did not reach the selected outbound")
	}
	getFixture(t, client, "http://"+net.JoinHostPort("safe.proxy.test", targetPort)+"/")
	select {
	case host := <-hosts:
		t.Fatalf("direct exception reached the proxy: %q", host)
	default:
	}
	stopSOCKS()
	before := served.Load()
	response, err := client.Get(requestURL)
	if err == nil {
		response.Body.Close()
		if response.StatusCode == http.StatusOK {
			t.Fatal("unavailable selected exit silently fell back to direct")
		}
	}
	if served.Load() != before {
		t.Fatal("failed proxy traffic escaped to the direct HTTP peer")
	}
}

func getFixture(t *testing.T, client *http.Client, address string) {
	t.Helper()
	response, err := client.Get(address)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil || response.StatusCode != 200 || string(body) != "fixture-only" {
		t.Fatalf("unexpected HTTP fixture response: %d %q %v", response.StatusCode, body, err)
	}
}

func unusedTCPAddress(t *testing.T) string {
	t.Helper()
	address, err := fixtureAddress(net.Listen, net.ListenPacket)
	if err != nil {
		t.Fatal(err)
	}
	return address
}

func fixtureAddress(
	listen func(string, string) (net.Listener, error),
	listenPacket func(string, string) (net.PacketConn, error),
) (string, error) {
	// Try the TCP allocator first, then sample nonprivileged ports across the
	// full range: Windows can keep choosing a TCP port from a UDP-excluded
	// range. Every candidate must support both protocols before it is returned.
	var lastErr error
	for attempt := 0; attempt < 100; attempt++ {
		candidate := "127.0.0.1:0"
		if attempt > 0 {
			candidate = fmt.Sprintf("127.0.0.1:%d", 1024+rand.IntN(65536-1024))
		}
		listener, err := listen("tcp4", candidate)
		if err != nil {
			if attempt == 0 {
				return "", fmt.Errorf("could not allocate a TCP fixture port: %w", err)
			}
			lastErr = err
			continue
		}
		address := listener.Addr().String()
		packets, err := listenPacket("udp4", address)
		_ = listener.Close()
		if err == nil {
			_ = packets.Close()
			return address, nil
		}
		lastErr = err
	}
	return "", fmt.Errorf("could not allocate a TCP/UDP fixture port: %w", lastErr)
}

func dnsFixtureInbound(listen string) object {
	inbound := listener("direct", "tun-in", listen)
	inbound["override_address"] = tunIPv4.Addr().Next().String()
	inbound["override_port"] = 53
	return inbound
}

func startDNSPeer(t *testing.T) string {
	return startDNSPeerWithQueries(t, nil)
}

func startDNSPeerWithQueries(t *testing.T, queries *atomic.Int32) string {
	t.Helper()
	listener, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { listener.Close() })
	go func() {
		for {
			buffer := make([]byte, 4096)
			n, address, err := listener.ReadFrom(buffer)
			if err != nil {
				return
			}
			var message dnsmessage.Message
			if message.Unpack(buffer[:n]) != nil || len(message.Questions) != 1 {
				continue
			}
			if queries != nil {
				queries.Add(1)
			}
			message.Header.Response = true
			message.Header.RecursionAvailable = true
			question := message.Questions[0]
			header := dnsmessage.ResourceHeader{
				Name: question.Name, Type: question.Type, Class: dnsmessage.ClassINET, TTL: 30,
			}
			switch question.Type {
			case dnsmessage.TypeA:
				message.Answers = []dnsmessage.Resource{{
					Header: header, Body: &dnsmessage.AResource{A: [4]byte{127, 0, 0, 1}},
				}}
			case dnsmessage.TypeAAAA:
				message.Answers = []dnsmessage.Resource{{
					Header: header, Body: &dnsmessage.AAAAResource{AAAA: netip.MustParseAddr("2001:db8::1").As16()},
				}}
			}
			reply, _ := message.Pack()
			_, _ = listener.WriteTo(reply, address)
		}
	}()
	return listener.LocalAddr().String()
}

func queryA(t *testing.T, address, domain string) netip.Addr {
	t.Helper()
	message := queryDNS(t, address, domain, dnsmessage.TypeA)
	for _, answer := range message.Answers {
		if body, ok := answer.Body.(*dnsmessage.AResource); ok {
			return netip.AddrFrom4(body.A)
		}
	}
	t.Fatalf("no A answer for %s: %+v", domain, message)
	return netip.Addr{}
}

func queryDNS(t *testing.T, address, domain string, queryType dnsmessage.Type) dnsmessage.Message {
	t.Helper()
	name, _ := dnsmessage.NewName(domain + ".")
	message := dnsmessage.Message{
		Header:    dnsmessage.Header{ID: 123, RecursionDesired: true},
		Questions: []dnsmessage.Question{{Name: name, Type: queryType, Class: dnsmessage.ClassINET}},
	}
	wire, _ := message.Pack()
	connection, err := net.DialTimeout("udp", address, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close()
	_ = connection.SetDeadline(time.Now().Add(3 * time.Second))
	if _, err := connection.Write(wire); err != nil {
		t.Fatal(err)
	}
	buffer := make([]byte, 4096)
	n, err := connection.Read(buffer)
	if err != nil {
		t.Fatal(err)
	}
	if err := message.Unpack(buffer[:n]); err != nil {
		t.Fatal(err)
	}
	return message
}

func startSOCKSPeer(t *testing.T, target string) (<-chan string, func(), string) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	hosts := make(chan string, 16)
	done := make(chan struct{})
	var handlers sync.WaitGroup
	go func() {
		defer close(done)
		for {
			client, err := listener.Accept()
			if err != nil {
				return
			}
			handlers.Add(1)
			go func() {
				defer handlers.Done()
				defer client.Close()
				_ = client.SetDeadline(time.Now().Add(5 * time.Second))
				prefix := make([]byte, 2)
				if _, err := io.ReadFull(client, prefix); err != nil || prefix[0] != 5 {
					return
				}
				methods := make([]byte, prefix[1])
				if _, err := io.ReadFull(client, methods); err != nil {
					return
				}
				_, _ = client.Write([]byte{5, 0})
				header := make([]byte, 4)
				if _, err := io.ReadFull(client, header); err != nil || header[1] != 1 {
					return
				}
				var host string
				switch header[3] {
				case 1, 4:
					size := 4
					if header[3] == 4 {
						size = 16
					}
					address := make([]byte, size)
					if _, err := io.ReadFull(client, address); err != nil {
						return
					}
					parsed, _ := netip.AddrFromSlice(address)
					host = parsed.String()
				case 3:
					var length [1]byte
					if _, err := io.ReadFull(client, length[:]); err != nil {
						return
					}
					address := make([]byte, int(length[0]))
					if _, err := io.ReadFull(client, address); err != nil {
						return
					}
					host = string(address)
				default:
					return
				}
				var port [2]byte
				if _, err := io.ReadFull(client, port[:]); err != nil || binary.BigEndian.Uint16(port[:]) == 0 {
					return
				}
				hosts <- host
				// Never forward a fixture request to the public network.
				upstream, err := net.DialTimeout("tcp", target, time.Second)
				if err != nil {
					return
				}
				defer upstream.Close()
				_, _ = client.Write([]byte{5, 0, 0, 1, 127, 0, 0, 1, 0, 0})
				copied := make(chan struct{})
				go func() {
					_, _ = io.Copy(upstream, client)
					if tcp, ok := upstream.(*net.TCPConn); ok {
						_ = tcp.CloseWrite()
					}
					close(copied)
				}()
				_, _ = io.Copy(client, upstream)
				client.Close()
				<-copied
			}()
		}
	}()
	var once sync.Once
	stop := func() {
		once.Do(func() {
			listener.Close()
			<-done
			handlers.Wait()
		})
	}
	return hosts, stop, listener.Addr().String()
}
