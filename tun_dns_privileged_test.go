package tailsplit

import (
	"context"
	"fmt"
	"net"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/miekg/dns"
)

// This test is opt-in on disposable GitHub runners only. The normal local
// suite never creates a TUN, binds privileged ports, or changes host routes.
func TestPrivilegedTUNDNS(t *testing.T) {
	if os.Getenv("TAILSPLIT_PRIVILEGED_TEST") != "1" {
		t.Skip("real TUN test runs only in the dedicated CI step")
	}
	if os.Getenv("GITHUB_ACTIONS") != "true" || os.Geteuid() != 0 {
		t.Fatal("refusing to create a TUN outside an explicitly enabled root CI test")
	}
	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
		t.Skip("CI TUN fixture supports Linux and macOS")
	}
	var competingQueries atomic.Int32
	handler := dns.HandlerFunc(func(w dns.ResponseWriter, request *dns.Msg) {
		competingQueries.Add(1)
		reply := new(dns.Msg)
		reply.SetReply(request)
		if len(request.Question) != 0 {
			reply.Answer = []dns.RR{&dns.A{
				Hdr: dns.RR_Header{Name: request.Question[0].Name, Rrtype: dns.TypeA, Class: dns.ClassINET},
				A:   net.IPv4(192, 0, 2, 99),
			}}
		}
		_ = w.WriteMsg(reply)
	})
	// A wildcard host listener is a stronger conflict fixture than a
	// loopback listener: packets must reach the virtual peer, not the host.
	for _, network := range []string{"udp4", "tcp4", "udp6", "tcp6"} {
		address := "0.0.0.0:53"
		if strings.HasSuffix(network, "6") {
			address = "[::]:53"
		}
		server := &dns.Server{Net: network, Handler: handler}
		var err error
		if strings.HasPrefix(network, "udp") {
			server.PacketConn, err = net.ListenPacket(network, address)
		} else {
			server.Listener, err = net.Listen(network, address)
		}
		if err != nil {
			t.Fatalf("bind competing %s DNS server: %v", network, err)
		}
		done := make(chan error, 1)
		go func() { done <- server.ActivateAndServe() }()
		t.Cleanup(func() {
			_ = server.Shutdown()
			select {
			case <-done:
			case <-time.After(time.Second):
				t.Error("competing DNS fixture did not stop")
			}
		})
	}
	before := defaultRouteFixture(t)
	upstream := startDNSPeer(t)
	iface := "lo"
	if runtime.GOOS == "darwin" {
		iface = "lo0"
	}
	config, err := loadTestConfig(t, fmt.Sprintf(`
state_dir: ./state
listen: %s
interface: %s
log_level: error
dns:
  direct: %s
tun:
  enabled: false
`, unusedTCPAddress(t), iface, upstream))
	if err != nil {
		t.Fatal(err)
	}
	compiled, err := Compile(config)
	if err != nil {
		t.Fatal(err)
	}
	engine, err := Open(context.Background(), config, compiled)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := engine.Close(); err != nil {
			t.Error(err)
		}
	})
	if err := engine.Start(); err != nil {
		t.Fatal("DNS TUN could not start alongside wildcard port-53 listeners:", err)
	}
	if engine.closeDNS != nil || engine.dnsListen != "" {
		t.Fatal("proxy-only mode installed a system DNS override")
	}
	query := new(dns.Msg)
	query.SetQuestion("tun-fixture.test.", dns.TypeA)
	for _, network := range []string{"udp", "tcp"} {
		for _, peer := range tunDNSAddresses() {
			client := &dns.Client{Net: network, Timeout: 3 * time.Second}
			reply, _, err := client.Exchange(query, net.JoinHostPort(peer, "53"))
			if err != nil {
				t.Fatalf("%s DNS through TUN peer %s: %v", network, peer, err)
			}
			if len(reply.Answer) != 1 || reply.Answer[0].(*dns.A).A.String() != "127.0.0.1" {
				t.Fatalf("TUN DNS reached the competing host resolver: %s", reply)
			}
		}
	}
	if competingQueries.Load() != 0 {
		t.Fatal("virtual DNS traffic was delivered to a host port-53 listener")
	}
	for _, network := range []string{"udp", "tcp"} {
		client := &dns.Client{Net: network, Timeout: 3 * time.Second}
		reply, _, err := client.Exchange(query, "127.0.0.1:53")
		if err != nil || len(reply.Answer) != 1 || reply.Answer[0].(*dns.A).A.String() != "192.0.2.99" {
			t.Fatal("the existing host DNS service was intercepted:", reply, err)
		}
	}
	if after := defaultRouteFixture(t); before != after {
		t.Fatalf("DNS-only TUN changed the default route:\nbefore: %s\nafter: %s", before, after)
	}
	if err := engine.Close(); err != nil {
		t.Fatal(err)
	}
	if after := defaultRouteFixture(t); before != after {
		t.Fatal("DNS-only TUN shutdown did not preserve the original default route")
	}
}

func defaultRouteFixture(t *testing.T) string {
	t.Helper()
	// An Internet destination also detects a new, more-specific default-like
	// route even when the original main-table default remains in place.
	command := exec.Command("ip", "-4", "route", "get", "203.0.113.5")
	if runtime.GOOS == "darwin" {
		command = exec.Command("/sbin/route", "-n", "get", "203.0.113.5")
	}
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("read CI default route: %v: %s", err, output)
	}
	if runtime.GOOS != "darwin" {
		first, _, _ := strings.Cut(string(output), "\n")
		return strings.Join(strings.Fields(first), " ")
	}
	var selected []string
	for line := range strings.SplitSeq(string(output), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "gateway:") || strings.HasPrefix(line, "interface:") {
			selected = append(selected, line)
		}
	}
	return strings.Join(selected, "\n")
}
