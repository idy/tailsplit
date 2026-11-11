package tailsplit

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"path/filepath"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/option"
	"github.com/sagernet/sing/common/buf"
	"github.com/sagernet/sing/common/bufio"
	M "github.com/sagernet/sing/common/metadata"
	N "github.com/sagernet/sing/common/network"
)

type trafficTestOutbound struct {
	adapter.Outbound
	tag string
}

func (o trafficTestOutbound) Tag() string { return o.tag }

func fixtureTraffic(focus string) *trafficCollector {
	return newTrafficCollector(Config{Focus: focus, Exits: []ExitConfig{{ID: "office"}}}, option.Options{
		Outbounds: []option.Outbound{{Tag: "direct"}},
		Endpoints: []option.Endpoint{{Type: "tailscale", Tag: "tailnet"}, {Type: "tailscale", Tag: "exit-office"}},
	}, nil)
}

type trafficTestConn struct {
	net.Conn
	read       *bytes.Reader
	writeLimit int
	writeErr   error
}

func (c *trafficTestConn) Read(p []byte) (int, error) { return c.read.Read(p) }
func (c *trafficTestConn) Write(p []byte) (int, error) {
	if c.writeLimit >= 0 && len(p) > c.writeLimit {
		return c.writeLimit, c.writeErr
	}
	return len(p), c.writeErr
}

func TestTrafficStreamDirectionAndPartialWrites(t *testing.T) {
	collector := fixtureTraffic("")
	conn := collector.RoutedConnection(context.Background(), &trafficTestConn{
		read: bytes.NewReader([]byte("upload")), writeLimit: 3, writeErr: io.ErrShortWrite,
	}, adapter.InboundContext{Inbound: "proxy-in"}, nil, trafficTestOutbound{tag: "tailnet"})
	if data, err := io.ReadAll(conn); err != nil || string(data) != "upload" {
		t.Fatalf("upload: %q, %v", data, err)
	}
	if n, err := conn.Write([]byte("download")); n != 3 || !errors.Is(err, io.ErrShortWrite) {
		t.Fatalf("download: %d, %v", n, err)
	}
	if got := collector.snapshot().Routes[MainExit]; got != (TrafficCounters{UploadBytes: 6, DownloadBytes: 3}) {
		t.Fatal("wrong direction or partial write accounting:", got)
	}
}

func TestTrafficOptimizedStreamCopy(t *testing.T) {
	collector := fixtureTraffic("")
	conn := collector.RoutedConnection(context.Background(), &trafficTestConn{
		read: bytes.NewReader([]byte("local upload")), writeLimit: -1,
	}, adapter.InboundContext{Inbound: "tun-in"}, nil, trafficTestOutbound{tag: "exit-office"})
	// sing's copy path unwraps CounterConn and invokes its count functions;
	// bytes.Reader also uses io.WriterTo for the reverse copy.
	if n, err := bufio.Copy(io.Discard, conn); err != nil || n != 12 {
		t.Fatalf("optimized upload: %d, %v", n, err)
	}
	if n, err := bufio.Copy(conn, bytes.NewReader([]byte("remote download"))); err != nil || n != 15 {
		t.Fatalf("optimized download: %d, %v", n, err)
	}
	if got := collector.snapshot().Routes["office"]; got != (TrafficCounters{UploadBytes: 12, DownloadBytes: 15}) {
		t.Fatal("optimized copies bypassed accounting:", got)
	}
}

type trafficTestPacketConn struct {
	N.PacketConn
	writeErr error
}

func (*trafficTestPacketConn) ReadPacket(buffer *buf.Buffer) (M.Socksaddr, error) {
	_, err := buffer.WriteString("datagram upload")
	return M.Socksaddr{}, err
}
func (c *trafficTestPacketConn) WritePacket(buffer *buf.Buffer, _ M.Socksaddr) error {
	buffer.Release()
	return c.writeErr
}

func TestTrafficPacketDirectionAndReleasedBuffers(t *testing.T) {
	collector := fixtureTraffic("")
	underlying := new(trafficTestPacketConn)
	conn := collector.RoutedPacketConnection(context.Background(), underlying,
		adapter.InboundContext{Inbound: "proxy-in"}, nil, trafficTestOutbound{tag: "direct"})
	buffer := buf.New()
	if _, err := conn.ReadPacket(buffer); err != nil {
		t.Fatal(err)
	}
	buffer.Release()
	if err := conn.WritePacket(buf.As([]byte("download")), M.Socksaddr{}); err != nil {
		t.Fatal(err)
	}
	underlying.writeErr = io.ErrClosedPipe
	if err := conn.WritePacket(buf.As([]byte("failed download")), M.Socksaddr{}); err == nil {
		t.Fatal("expected failed datagram")
	}
	if got := collector.snapshot().Routes["direct"]; got != (TrafficCounters{UploadBytes: 15, DownloadBytes: 8}) {
		t.Fatal("wrong datagram direction, released buffer length or failed-write accounting:", got)
	}
}

func TestTrafficNativeFlowsConcurrentAndSessionReset(t *testing.T) {
	collector := fixtureTraffic("")
	flow := collector.RoutedFlow(context.Background(), adapter.InboundContext{Inbound: "tun-in"}, nil, trafficTestOutbound{tag: "exit-office"})
	var workers sync.WaitGroup
	for range 16 {
		workers.Go(func() {
			for range 1000 {
				flow.CountForward(60)
				flow.CountReverse(120)
			}
		})
	}
	for range 100 {
		_ = collector.snapshot()
	}
	workers.Wait()
	flow.CountForward(-1)
	flow.CountReverse(0)
	before := collector.snapshot()
	if got := before.Routes["office"]; got != (TrafficCounters{UploadBytes: 960000, DownloadBytes: 1920000}) {
		t.Fatal("native flow lost direction or concurrent increments:", got)
	}
	after := collector.snapshot()
	// Windows can return the same wall-clock tick for consecutive samples.
	if after.SampledAt.IsZero() || after.SampledAt.Before(before.SampledAt) || after.SessionID != before.SessionID {
		t.Fatal("sample lost its session or timestamp moved backward")
	}
	before.Routes["office"] = TrafficCounters{}
	if collector.snapshot().Routes["office"].UploadBytes == 0 {
		t.Fatal("snapshot aliases live counters")
	}
	restarted := fixtureTraffic("").snapshot()
	if restarted.SessionID == before.SessionID || restarted.Routes["office"] != (TrafficCounters{}) {
		t.Fatal("new engine reused old session counters:", restarted)
	}
}

func TestTrafficOnlyCompiledLocalRoutesAndFocusedExit(t *testing.T) {
	collector := newTrafficCollector(Config{Focus: "office"}, option.Options{
		Endpoints: []option.Endpoint{{Type: "tailscale", Tag: "tailnet"}},
	}, nil)
	if got := collector.snapshot().Routes; !reflect.DeepEqual(got, map[string]TrafficCounters{"office": {}}) {
		t.Fatal("focused Global session invented inactive main/direct routes:", got)
	}
	for _, test := range []struct{ inbound, outbound string }{
		{"tailnet", "tailnet"}, {"tun-in", "direct"}, {"proxy-in", "unknown"}, {"", "tailnet"},
	} {
		if flow := collector.RoutedFlow(context.Background(), adapter.InboundContext{Inbound: test.inbound}, nil, trafficTestOutbound{tag: test.outbound}); flow != nil {
			t.Fatal("counting non-local or unknown route:", test)
		}
	}
}

func TestCompiledPrivateExitsFollowCanonicalSuffixOwnership(t *testing.T) {
	for _, test := range []struct {
		name    string
		change  func(*Config)
		blocked map[string]bool
		want    []string
	}{
		{name: "distinct primary and extra", want: []string{MainExit, "office"}},
		{name: "no suffix ownership", change: func(c *Config) {
			c.DNS.MagicDomains = nil
			c.Exits[0].Tailnet = "Office"
			c.Rules.Routes = []RouteConfig{{Exit: "office", Domains: []string{"ordinary.example"}}}
			c.Rules.ProxyDomains = []string{"proxy.example"}
		}, want: []string{}},
		{name: "canonical primary wins duplicate", change: func(c *Config) {
			c.DNS.MagicDomains = []string{"*.PRIMARY.TS.NET."}
			c.Exits[0].MagicDNSSuffix = "Primary.TS.NET."
		}, want: []string{MainExit}},
		{name: "custom primary wins canonical duplicate", change: func(c *Config) {
			c.DNS.MagicDomains = nil
			c.Tailscale.Domains = []string{"BÜCHER.example.", "service.example", "service.example"}
			c.Exits[0].MagicDNSSuffix = "xn--bcher-kva.example"
		}, want: []string{MainExit}},
		{name: "first extra wins duplicate", change: func(c *Config) {
			c.DNS.MagicDomains = nil
			c.Exits[1].ExitNode = "100.101.0.12"
			c.Exits[1].MagicDNSSuffix = "Office.TS.NET."
		}, want: []string{"office"}},
		{name: "longest suffix keeps both owners", change: func(c *Config) {
			c.DNS.MagicDomains = []string{"ts.net"}
		}, want: []string{MainExit, "office"}},
		{name: "blocked primary retains duplicate", change: func(c *Config) {
			c.Rules.DefaultExit = "office"
			c.Exits[0].MagicDNSSuffix = "primary.ts.net"
		}, blocked: map[string]bool{MainExit: true}, want: []string{MainExit}},
		{name: "blocked first extra retains duplicate", change: func(c *Config) {
			c.Exits[1].ExitNode = "100.101.0.12"
			c.Exits[1].MagicDNSSuffix = "office.ts.net"
		}, blocked: map[string]bool{"office": true}, want: []string{MainExit, "office"}},
		{name: "pending extra has no ownership", change: func(c *Config) {
			c.Exits[0].ExitNode, c.Exits[0].Tailnet = "", "Office"
		}, want: []string{MainExit}},
		{name: "focused extra uses logical ID", change: func(c *Config) {
			c.Focus = "office"
		}, want: []string{"office"}},
		{name: "Global extra uses logical ID", change: func(c *Config) {
			c.Rules.Mode, c.Rules.GlobalExit = RoutingGlobal, "office"
		}, want: []string{"office"}},
		{name: "Global primary", change: func(c *Config) {
			c.Rules.Mode = RoutingGlobal
		}, want: []string{MainExit}},
		{name: "extras only", change: func(c *Config) {
			c.Tailscale, c.DNS.MagicDomains = nil, nil
			c.Rules.DefaultExit = "office"
		}, want: []string{"office"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			config := focusedExitTestConfig(t)
			config.Rules.Final, config.Rules.Routes = "direct", nil
			if test.change != nil {
				test.change(&config)
			}
			compiled, err := compileWithOptions(t.Context(), config, compileOptions{validateTraffic: true, blockedExits: test.blocked})
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(compiled.privateExits, test.want) {
				t.Fatalf("compiled private owners = %v; want %v", compiled.privateExits, test.want)
			}
			if len(compiled.privateExits) > len(config.ExitIDs()) {
				t.Fatal("private ownership exceeds configured devices")
			}
		})
	}
}

func TestEngineTrafficPrivateExitsUsePreparedPlanAndStayIndependent(t *testing.T) {
	if !TailscaleEnabled {
		t.Skip("constructing a native Tailscale engine requires with_gvisor")
	}
	config := focusedExitTestConfig(t)
	config.Tailscale.AuthKeyEnv = ""
	config.Rules.Final, config.Rules.Routes = "direct", nil
	config.Exits[0].Tailnet = "Office"
	runtime, err := CompileContext(t.Context(), config)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(runtime.privateExits, []string{MainExit}) {
		t.Fatal("unobserved office device invented a private namespace:", runtime.privateExits)
	}
	// Preflight learns this namespace from the SDK and recompiles only runtime.
	current := &adapter.TailscaleEndpointStatus{BackendState: "Running", NetworkName: "Office", MagicDNSSuffix: "office.ts.net"}
	latest := map[string]Status{"office": connectionStatus(current, config.Exits[0].ExitNode)}
	if err := isolateOptionalExits(t.Context(), config, &runtime, nil, latest, nil); err != nil {
		t.Fatal(err)
	}
	want := []string{MainExit, "office"}
	if !reflect.DeepEqual(runtime.privateExits, want) || config.Exits[0].MagicDNSSuffix != "" {
		t.Fatal("prepared plan lost observed ownership or rewrote saved metadata:", runtime.privateExits, config.Exits)
	}
	// Later live metadata now collides with main, while a later saved draft drops
	// main's suffix. Neither is the policy already compiled for this engine.
	current.MagicDNSSuffix = "primary.ts.net"
	latest["office"] = connectionStatus(current, config.Exits[0].ExitNode)
	config.DNS.MagicDomains = nil
	config.Exits[0].MagicDNSSuffix = "changed.ts.net"
	engine, err := Open(t.Context(), config, runtime)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := engine.Close(); err != nil {
			t.Error(err)
		}
	})
	// The collector must own a copy even if the caller reuses its compiled plan.
	runtime.privateExits[0] = "mutated-plan"
	first := engine.Traffic()
	if !reflect.DeepEqual(first.PrivateExits, want) {
		t.Fatal("engine ownership came from saved/live metadata instead of its prepared plan:", first.PrivateExits)
	}
	first.PrivateExits[0] = "mutated-snapshot"
	if got := engine.Traffic(); !reflect.DeepEqual(got.PrivateExits, want) || got.SessionID != first.SessionID {
		t.Fatal("traffic samples alias private ownership or changed runtime session:", got)
	}
}

func TestTrafficPrivateExitsAlwaysEncodesAnArray(t *testing.T) {
	for _, owners := range [][]string{nil, {}, {MainExit, "office"}} {
		snapshot := newTrafficCollector(Config{}, option.Options{}, owners).snapshot()
		data, err := json.Marshal(snapshot)
		if err != nil {
			t.Fatal(err)
		}
		var encoded map[string]json.RawMessage
		if err := json.Unmarshal(data, &encoded); err != nil {
			t.Fatal(err)
		}
		if got := encoded["private_exits"]; len(got) == 0 || got[0] != '[' {
			t.Fatalf("private_exits must be an explicit array, even without owners: %s", data)
		}
		if len(owners) == 0 && string(encoded["private_exits"]) != "[]" {
			t.Fatalf("no-owner metadata must be explicit []: %s", data)
		}
	}
}

func TestEngineTrafficCountsActualDirectProxyPayload(t *testing.T) {
	remote, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer remote.Close()
	remoteDone := make(chan error, 1)
	go func() {
		conn, err := remote.Accept()
		if err != nil {
			remoteDone <- err
			return
		}
		defer conn.Close()
		_ = conn.SetDeadline(time.Now().Add(3 * time.Second))
		data := make([]byte, 6)
		if _, err := io.ReadFull(conn, data); err != nil {
			remoteDone <- err
			return
		}
		_, err = conn.Write([]byte("download"))
		remoteDone <- err
	}()
	proxyAddress := unusedTCPAddress(t)
	_, port, _ := net.SplitHostPort(proxyAddress)
	data := []byte(`{"log":{"level":"error"},"inbounds":[{"type":"mixed","tag":"proxy-in","listen":"127.0.0.1","listen_port":` + port + `}],"outbounds":[{"type":"direct","tag":"direct"}],"route":{"final":"direct"}}`)
	engine, err := Open(context.Background(), Config{StateDir: filepath.Join(t.TempDir(), "state")}, Compiled{JSON: data})
	if err != nil {
		t.Fatal(err)
	}
	defer engine.Close()
	if err := engine.Start(); err != nil {
		t.Fatal(err)
	}
	conn, err := net.DialTimeout("tcp", proxyAddress, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(3 * time.Second))
	// HTTP CONNECT negotiation must not be counted as remote application data.
	if _, err := io.WriteString(conn, "CONNECT "+remote.Addr().String()+" HTTP/1.1\r\nHost: "+remote.Addr().String()+"\r\n\r\n"); err != nil {
		t.Fatal(err)
	}
	var response []byte
	for !bytes.HasSuffix(response, []byte("\r\n\r\n")) {
		var next [1]byte
		if _, err := conn.Read(next[:]); err != nil {
			t.Fatal(err)
		}
		response = append(response, next[0])
	}
	if _, err := conn.Write([]byte("upload")); err != nil {
		t.Fatal(err)
	}
	reply := make([]byte, 8)
	if _, err := io.ReadFull(conn, reply); err != nil {
		t.Fatal(err)
	}
	if err := <-remoteDone; err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(time.Second)
	for {
		got := engine.Traffic().Routes["direct"]
		if got == (TrafficCounters{UploadBytes: 6, DownloadBytes: 8}) {
			break
		}
		if time.Now().After(deadline) {
			encoded, _ := json.Marshal(engine.Traffic())
			t.Fatal("actual proxy bytes were not counted exactly:", string(encoded))
		}
		time.Sleep(time.Millisecond)
	}
}
