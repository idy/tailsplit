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
	})
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
	if !after.SampledAt.After(before.SampledAt) || after.SessionID != before.SessionID {
		t.Fatal("sample does not preserve session and advance time")
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
	})
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
