//go:build windows

package tailsplit

import (
	"context"
	"fmt"
	"net"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/miekg/dns"
	"golang.org/x/sys/windows"
)

// This opt-in fixture runs only on disposable elevated Windows CI runners.
// It installs the DNS-only adapter, with loopback fixtures and no Tailnet login.
func TestWindowsTUNDNSLifecycle(t *testing.T) {
	if os.Getenv("TAILSPLIT_WINDOWS_TUN_TEST") != "1" {
		t.Skip("real Windows TUN test runs only in its dedicated CI step")
	}
	if os.Getenv("GITHUB_ACTIONS") != "true" || !windows.GetCurrentProcessToken().IsElevated() {
		t.Fatal("refusing to create a TUN outside explicitly enabled elevated Windows CI")
	}
	interfaces, err := net.Interfaces()
	if err != nil {
		t.Fatal(err)
	}
	var loopback string
	for _, iface := range interfaces {
		if iface.Flags&net.FlagLoopback != 0 {
			loopback = iface.Name
			break
		}
	}
	if loopback == "" {
		t.Fatal("Windows loopback interface not found")
	}
	before := windowsDefaultRoutes(t)
	config, err := loadTestConfig(t, fmt.Sprintf(`
state_dir: ./state
listen: %s
interface: %q
log_level: error
dns:
  direct: %s
tun:
  enabled: false
`, unusedTCPAddress(t), loopback, startDNSPeer(t)))
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
		t.Fatal("start bundled Wintun DNS adapter:", err)
	}
	if engine.closeDNS != nil || engine.dnsListen != "" {
		t.Fatal("DNS-only mode installed a host DNS override")
	}
	query := new(dns.Msg)
	query.SetQuestion("windows-tun-fixture.test.", dns.TypeA)
	for _, network := range []string{"udp", "tcp"} {
		for _, peer := range tunDNSAddresses() {
			client := &dns.Client{Net: network, Timeout: time.Second}
			deadline := time.Now().Add(8 * time.Second)
			for {
				reply, _, err := client.Exchange(query, net.JoinHostPort(peer, "53"))
				if err == nil {
					if len(reply.Answer) != 1 || reply.Answer[0].(*dns.A).A.String() != "127.0.0.1" {
						t.Fatalf("unexpected %s DNS response through Windows TUN %s: %s", network, peer, reply)
					}
					break
				}
				if time.Now().After(deadline) {
					t.Fatalf("%s DNS through Windows TUN %s: %v", network, peer, err)
				}
				time.Sleep(100 * time.Millisecond)
			}
		}
	}
	if after := windowsDefaultRoutes(t); before != after {
		t.Fatalf("DNS-only adapter changed default routes:\nbefore: %s\nafter: %s", before, after)
	}
	if err := engine.Close(); err != nil {
		t.Fatal(err)
	}
	if after := windowsDefaultRoutes(t); before != after {
		t.Fatal("Windows TUN shutdown did not preserve default routes")
	}
}

func windowsDefaultRoutes(t *testing.T) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, "pwsh", "-NoProfile", "-NonInteractive", "-Command",
		"$ErrorActionPreference='Stop'; @(Get-NetRoute | Where-Object { @('0.0.0.0/0','0.0.0.0/1','128.0.0.0/1','::/0','::/1','8000::/1') -contains $_.DestinationPrefix } | Sort-Object AddressFamily,InterfaceIndex,DestinationPrefix,NextHop,RouteMetric | Select-Object AddressFamily,InterfaceIndex,DestinationPrefix,NextHop,RouteMetric) | ConvertTo-Json -Compress")
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("read Windows default routes: %v: %s", err, output)
	}
	return strings.TrimSpace(string(output))
}
