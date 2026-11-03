package tailsplit

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/netip"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/log"
	"github.com/sagernet/sing-box/option"
	M "github.com/sagernet/sing/common/metadata"
	N "github.com/sagernet/sing/common/network"
)

func TestDirectIPv4RecoveryIsExplicitAndDoesNotChangeRoutes(t *testing.T) {
	var originalRoutes []byte
	for _, policy := range []string{"", "true", "false"} {
		t.Run("policy="+policy, func(t *testing.T) {
			text := "tailscale:\n  exit_node: 100.64.0.10\ntun:\n  enabled: true\n"
			if policy != "" {
				text += "dns:\n  direct_ipv6: " + policy + "\n"
			}
			config, err := loadTestConfig(t, text)
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
			route := native["route"].(map[string]any)
			if policy == "false" {
				rules := route["rules"].([]any)
				expected := object{
					"inbound": []any{"proxy-in"}, "action": "sniff",
					"sniffer": []any{"http", "tls", "quic"}, "timeout": "300ms",
				}
				if len(rules) < 3 || !reflect.DeepEqual(rules[2], expected) {
					t.Fatal("IPv4 recovery did not add hostname sniffing before route selection")
				}
				route["rules"] = append(rules[:2:2], rules[3:]...)
			}
			routes, _ := json.Marshal(route)
			if originalRoutes == nil {
				originalRoutes = routes
			} else if !bytes.Equal(routes, originalRoutes) {
				t.Fatal("IPv4 recovery changed original-IP route selection")
			}
			direct := native["outbounds"].([]any)[0].(map[string]any)
			if policy == "false" {
				resolver, ok := direct["domain_resolver"].(map[string]any)
				if direct["type"] != directIPv4Type || !ok || resolver["strategy"] != "ipv4_only" {
					t.Fatal("disabled direct IPv6 did not configure IPv4 recovery")
				}
			} else if direct["type"] != "direct" || direct["domain_resolver"] != "dns-direct" {
				t.Fatal("IPv4 recovery was enabled without an explicit policy")
			}
		})
	}
}

func TestDirectIPv4RecoveryLeavesLocalAndIPv4DestinationsAlone(t *testing.T) {
	out := &ipv4DirectOutbound{lookupIPv4: func(context.Context, string) ([]netip.Addr, error) {
		t.Fatal("local or IPv4 traffic was re-resolved")
		return nil, nil
	}}
	for _, address := range []string{"127.0.0.1", "192.0.2.1", "::ffff:192.0.2.1", "::1", "fd00::1", "fe80::1%en0", "ff02::1", "::"} {
		t.Run(address, func(t *testing.T) {
			target := M.SocksaddrFrom(netip.MustParseAddr(address), 443)
			metadata := adapter.InboundContext{Domain: "voice.test", Destination: target}
			addresses := []netip.Addr{target.Addr}
			got, resolved, err := out.recoverDestination(adapter.WithContext(context.Background(), &metadata), target, addresses)
			if err != nil || got != target || !reflect.DeepEqual(resolved, addresses) {
				t.Fatal("unrelated destination changed:", got, resolved, err)
			}
		})
	}
}

func TestDirectIPv4RecoveryRequiresAHostnameAndIPv4Answer(t *testing.T) {
	target := M.ParseSocksaddr("[2001:db8::10]:443")
	for _, host := range []string{"", "127.0.0.1", "[2001:db8::1]", "bad host"} {
		t.Run("hostname="+host, func(t *testing.T) {
			out := &ipv4DirectOutbound{lookupIPv4: func(context.Context, string) ([]netip.Addr, error) {
				t.Fatal("an invalid recovered hostname reached DNS")
				return nil, nil
			}}
			metadata := adapter.InboundContext{Domain: host, Destination: target}
			if _, _, err := out.recoverDestination(adapter.WithContext(context.Background(), &metadata), target, nil); err == nil {
				t.Fatal("missing hostname was accepted")
			}
		})
	}
	lookupFailure := errors.New("fixture DNS failure")
	for _, test := range []struct {
		name      string
		addresses []netip.Addr
		err       error
	}{
		{"NXDOMAIN", nil, lookupFailure},
		{"empty answer", nil, nil},
		{"IPv6-only answer", []netip.Addr{netip.MustParseAddr("2001:db8::1")}, nil},
	} {
		t.Run(test.name, func(t *testing.T) {
			out := &ipv4DirectOutbound{lookupIPv4: func(_ context.Context, host string) ([]netip.Addr, error) {
				if host != "voice.test" {
					t.Fatal("the recovered hostname was lost")
				}
				return test.addresses, test.err
			}}
			metadata := adapter.InboundContext{Domain: "voice.test", Destination: target}
			_, _, err := out.recoverDestination(adapter.WithContext(context.Background(), &metadata), target, nil)
			if err == nil || test.err != nil && !errors.Is(err, test.err) {
				t.Fatal("IPv4 lookup failure was not preserved:", err)
			}
		})
	}
}

func TestDirectIPv4RecoveryPreservesMetadataAndCancellation(t *testing.T) {
	target := M.ParseSocksaddr("[2001:db8::10]:443")
	metadata := adapter.InboundContext{Domain: "voice.test", Destination: target}
	before := metadata
	out := &ipv4DirectOutbound{
		logger: log.NewNOPFactory().NewLogger("test"),
		lookupIPv4: func(ctx context.Context, _ string) ([]netip.Addr, error) {
			if ctx.Err() != nil {
				t.Fatal("a canceled request reached DNS")
			}
			return []netip.Addr{netip.MustParseAddr("2001:db8::1"), netip.MustParseAddr("127.0.0.1")}, nil
		},
	}
	ctx := adapter.WithContext(context.Background(), &metadata)
	recovered, addresses, err := out.recoverDestination(ctx, target, nil)
	if err != nil || recovered.Fqdn != metadata.Domain || recovered.Port != target.Port ||
		len(addresses) != 1 || !addresses[0].Is4() || !reflect.DeepEqual(metadata, before) {
		t.Fatal("recovery changed the manager's original destination:", recovered, addresses, err)
	}
	ctx, cancel := context.WithCancel(ctx)
	cancel()
	if _, _, err := out.recoverDestination(ctx, target, nil); !errors.Is(err, context.Canceled) {
		t.Fatal("cancellation was ignored:", err)
	}
}

type ipv4RecoveryLogCapture struct{ bytes.Buffer }

func (w *ipv4RecoveryLogCapture) WriteMessage(_ log.Level, message string) {
	_, _ = w.WriteString(message + "\n")
}

func TestDirectIPv4RecoveryAppLogging(t *testing.T) {
	for _, level := range []string{"info", "debug"} {
		t.Run(level, func(t *testing.T) {
			var captured ipv4RecoveryLogCapture
			threshold, err := log.ParseLevel(level)
			if err != nil {
				t.Fatal(err)
			}
			factory, err := log.New(log.Options{Context: context.Background(),
				Options: option.LogOptions{Level: level}, DefaultWriter: io.Discard,
				PlatformWriter: filteredLogWriter{writer: &captured, level: threshold}})
			if err != nil {
				t.Fatal(err)
			}
			if err := factory.Start(); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = factory.Close() })
			target := M.ParseSocksaddr("[2001:db8::10]:443")
			metadata := adapter.InboundContext{Domain: "voice.test", Destination: target}
			answers := []netip.Addr{netip.MustParseAddr("192.0.2.10"), netip.MustParseAddr("192.0.2.11")}
			out := &ipv4DirectOutbound{logger: factory.NewLogger("direct"),
				lookupIPv4: func(context.Context, string) ([]netip.Addr, error) { return answers, nil }}
			got, resolved, err := out.recoverDestination(adapter.WithContext(context.Background(), &metadata), target, nil)
			if err != nil || got.Fqdn != "voice.test" || !reflect.DeepEqual(resolved, answers) || metadata.Destination != target {
				t.Fatalf("app logging changed destination recovery: %v %v %v", got, resolved, err)
			}
			if level == "debug" {
				for _, text := range []string{"recover direct IPv6 destination", "voice.test", "192.0.2.10", "192.0.2.11"} {
					if !strings.Contains(captured.String(), text) {
						t.Fatalf("missing recovery diagnostic %q", text)
					}
				}
			} else if captured.Len() != 0 {
				t.Fatal("info logging exposed filtered debug messages")
			}
		})
	}
}

func TestDirectIPv4ParallelDialMethodsAndTCPHalfClose(t *testing.T) {
	peer, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = peer.Close() })
	go func() {
		for {
			conn, err := peer.Accept()
			if err != nil {
				return
			}
			go func() {
				defer conn.Close()
				_ = conn.SetDeadline(time.Now().Add(3 * time.Second))
				data, _ := io.ReadAll(conn)
				_, _ = conn.Write(data)
			}()
		}
	}()
	_, instance, _ := startDirectIPv4Fixture(t, peer.Addr().String(), nil)
	registered, found := instance.Outbound().Outbound("direct")
	if !found {
		t.Fatal("direct outbound is missing")
	}
	out := registered.(*ipv4DirectOutbound)
	port := M.SocksaddrFromNet(peer.Addr()).Port
	target := M.SocksaddrFrom(netip.MustParseAddr("2001:db8::30"), port)
	metadata := adapter.InboundContext{Domain: "voice.test", Destination: target}
	ctx, cancel := context.WithTimeout(adapter.WithContext(context.Background(), &metadata), 5*time.Second)
	defer cancel()
	for name, dial := range map[string]func() (net.Conn, error){
		"context": func() (net.Conn, error) { return out.DialContext(ctx, "tcp", target) },
		"parallel": func() (net.Conn, error) {
			return out.DialParallel(ctx, "tcp", target, []netip.Addr{target.Addr})
		},
		"parallel network": func() (net.Conn, error) {
			return out.DialParallelNetwork(ctx, "tcp", target, []netip.Addr{target.Addr}, nil, nil, nil, 0)
		},
	} {
		t.Run(name, func(t *testing.T) {
			conn, err := dial()
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			_ = conn.SetDeadline(time.Now().Add(3 * time.Second))
			if _, err := conn.Write([]byte("half-close fixture")); err != nil {
				t.Fatal(err)
			}
			// The upstream direct dialer wraps traffic counters; use the same
			// half-close unwrapping as sing-box's connection-copy path.
			if err := N.CloseWrite(conn); err != nil {
				t.Fatal(err)
			}
			data, err := io.ReadAll(conn)
			if err != nil || string(data) != "half-close fixture" {
				t.Fatal("response after client FIN was lost:", string(data), err)
			}
			if !M.AddrFromNet(conn.RemoteAddr()).Is4() || metadata.Destination != target {
				t.Fatal("parallel dialing bypassed IPv4 recovery or changed original metadata")
			}
		})
	}
}

func TestDirectIPv4PacketDialMethods(t *testing.T) {
	peer := startIPv4EchoPackets(t)
	_, instance, _ := startDirectIPv4Fixture(t, peer.LocalAddr().String(), nil)
	registered, _ := instance.Outbound().Outbound("direct")
	out := registered.(*ipv4DirectOutbound)
	target := M.SocksaddrFrom(netip.MustParseAddr("2001:db8::40"), M.SocksaddrFromNet(peer.LocalAddr()).Port)
	metadata := adapter.InboundContext{Domain: "voice.test", Destination: target}
	ctx := adapter.WithContext(context.Background(), &metadata)
	for name, listen := range map[string]func() (net.PacketConn, M.Socksaddr, error){
		"packet connection": func() (net.PacketConn, M.Socksaddr, error) {
			conn, err := out.ListenPacket(ctx, target)
			return conn, target, err
		},
		"with destination": func() (net.PacketConn, M.Socksaddr, error) {
			conn, address, err := out.ListenPacketWithDestination(ctx, target)
			return conn, M.SocksaddrFrom(address, target.Port), err
		},
		"serial network": func() (net.PacketConn, M.Socksaddr, error) {
			conn, address, err := out.ListenSerialNetworkPacket(ctx, target, []netip.Addr{target.Addr}, nil, nil, nil, 0)
			return conn, M.SocksaddrFrom(address, target.Port), err
		},
	} {
		t.Run(name, func(t *testing.T) {
			conn, destination, err := listen()
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			if name != "packet connection" && !destination.Addr.Is4() {
				t.Fatal("packet dialer did not report the actual IPv4 destination")
			}
			_ = conn.SetDeadline(time.Now().Add(3 * time.Second))
			if _, err := conn.WriteTo([]byte("voice packet"), destination.UDPAddr()); err != nil {
				t.Fatal(err)
			}
			buffer := make([]byte, 128)
			n, from, err := conn.ReadFrom(buffer)
			if err != nil || string(buffer[:n]) != "voice packet" || M.SocksaddrFromNet(from) != destination {
				t.Fatal("UDP entry point lost payload or reply address:", from, err)
			}
			if metadata.Destination != target {
				t.Fatal("packet dialing changed the manager's original destination")
			}
		})
	}
}

func startIPv4EchoPackets(t *testing.T) net.PacketConn {
	t.Helper()
	peer, err := net.ListenPacket("udp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = peer.Close() })
	go func() {
		buffer := make([]byte, 4096)
		for {
			n, source, err := peer.ReadFrom(buffer)
			if err != nil {
				return
			}
			_, _ = peer.WriteTo(buffer[:n], source)
		}
	}()
	return peer
}

func TestDirectIPv4ResolvedHostnameFiltersIPv6WithoutChangingInput(t *testing.T) {
	out := &ipv4DirectOutbound{}
	original := []netip.Addr{netip.MustParseAddr("2001:db8::1"), netip.MustParseAddr("192.0.2.1")}
	target := M.ParseSocksaddr("voice.test:443")
	_, addresses, err := out.recoverDestination(context.Background(), target, original)
	if err != nil || len(addresses) != 1 || !addresses[0].Is4() || !original[0].Is6() {
		t.Fatal("pre-resolved IPv6 bypassed the direct IPv4 policy:", addresses, err)
	}
}
