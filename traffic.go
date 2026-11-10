package tailsplit

import (
	"context"
	"crypto/rand"
	"net"
	"sync/atomic"
	"time"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/option"
	"github.com/sagernet/sing-tun"
	"github.com/sagernet/sing/common/bufio"
	N "github.com/sagernet/sing/common/network"
)

// TrafficSnapshot contains cumulative bytes for one running engine. Consumers
// calculate rates from successive samples with the same SessionID. A new engine
// always starts new counters, even when it uses the same saved configuration.
type TrafficSnapshot struct {
	SessionID string                     `json:"session_id"`
	SampledAt time.Time                  `json:"sampled_at"`
	Routes    map[string]TrafficCounters `json:"routes"`
	// PrivateExits lists the compiled runtime's private-name owners by logical
	// exit ID, including blocked owners whose reserved namespaces reject traffic.
	// It describes ownership, not availability; an empty array means no owners.
	PrivateExits []string `json:"private_exits"`
}

// TrafficCounters measures local-user upload and download at the routed inbound.
// Stream/datagram bytes are payload; native TUN flow bytes include IP headers.
// These are router-observed bytes, not physical-link usage: Tailscale encryption,
// transport and control traffic are excluded. No payload estimates are used.
type TrafficCounters struct {
	UploadBytes   uint64 `json:"upload_bytes"`
	DownloadBytes uint64 `json:"download_bytes"`
}

type routeTraffic struct {
	upload, download atomic.Uint64
}

type trafficCollector struct {
	session string
	// Route maps and ownership are immutable after construction; connections
	// update only the counters' atomics.
	tags         map[string]*routeTraffic
	routes       map[string]*routeTraffic
	privateExits []string
}

var _ adapter.ConnectionTracker = (*trafficCollector)(nil)

func newTrafficCollector(config Config, options option.Options, privateExits []string) *trafficCollector {
	t := &trafficCollector{
		session: rand.Text(), tags: map[string]*routeTraffic{}, routes: map[string]*routeTraffic{},
		privateExits: append([]string{}, privateExits...),
	}
	add := func(tag, id string) {
		counter := new(routeTraffic)
		t.tags[tag], t.routes[id] = counter, counter
	}
	for _, outbound := range options.Outbounds {
		if outbound.Tag == "direct" {
			add(outbound.Tag, "direct")
		}
	}
	for _, endpoint := range options.Endpoints {
		if endpoint.Type != "tailscale" {
			continue
		}
		if endpoint.Tag == "tailnet" {
			id := config.Focus
			if id == "" {
				id = MainExit
			}
			add(endpoint.Tag, id)
		} else {
			for _, exit := range config.Exits {
				if endpoint.Tag == ExitTag(exit.ID) {
					add(endpoint.Tag, exit.ID)
				}
			}
		}
	}
	return t
}

func (t *trafficCollector) snapshot() TrafficSnapshot {
	snapshot := TrafficSnapshot{
		SessionID: t.session, SampledAt: time.Now().UTC(), Routes: make(map[string]TrafficCounters, len(t.routes)),
		PrivateExits: append([]string{}, t.privateExits...),
	}
	for id, counter := range t.routes {
		snapshot.Routes[id] = TrafficCounters{UploadBytes: counter.upload.Load(), DownloadBytes: counter.download.Load()}
	}
	return snapshot
}

func (t *trafficCollector) route(metadata adapter.InboundContext, outbound adapter.Outbound) *routeTraffic {
	// Traffic arriving from another Tailnet peer is not local-user upload.
	// DNS hijacks and the core's own outbound/control traffic bypass this hook.
	if metadata.Inbound != "proxy-in" && metadata.Inbound != "tun-in" || outbound == nil {
		return nil
	}
	return t.tags[outbound.Tag()]
}

func (t *trafficCollector) RoutedConnection(_ context.Context, conn net.Conn, metadata adapter.InboundContext, _ adapter.Rule, outbound adapter.Outbound) net.Conn {
	counter := t.route(metadata, outbound)
	if counter == nil {
		return conn
	}
	// This is the local-facing connection: reading is upload, writing download.
	return bufio.NewCounterConn(conn, []N.CountFunc{counter.uploadBytes}, []N.CountFunc{counter.downloadBytes})
}

func (t *trafficCollector) RoutedPacketConnection(_ context.Context, conn N.PacketConn, metadata adapter.InboundContext, _ adapter.Rule, outbound adapter.Outbound) N.PacketConn {
	counter := t.route(metadata, outbound)
	if counter == nil {
		return conn
	}
	return bufio.NewCounterPacketConn(conn, []N.CountFunc{counter.uploadBytes}, []N.CountFunc{counter.downloadBytes})
}

func (t *trafficCollector) RoutedFlow(_ context.Context, metadata adapter.InboundContext, _ adapter.Rule, outbound adapter.Outbound) tun.FlowTracker {
	counter := t.route(metadata, outbound)
	if counter == nil {
		return nil
	}
	return counter
}

func (c *routeTraffic) uploadBytes(n int64) {
	if n > 0 {
		c.upload.Add(uint64(n))
	}
}

func (c *routeTraffic) downloadBytes(n int64) {
	if n > 0 {
		c.download.Add(uint64(n))
	}
}

func (*routeTraffic) AttachFlow(tun.FlowHandle)     {}
func (*routeTraffic) FlowEstablished()              {}
func (*routeTraffic) CloseFlow(tun.FlowCloseReason) {}

func (c *routeTraffic) CountForward(n int) {
	if n > 0 {
		c.upload.Add(uint64(n))
	}
}

func (c *routeTraffic) CountReverse(n int) {
	if n > 0 {
		c.download.Add(uint64(n))
	}
}

// Traffic returns an independent, credential-free sample of this engine's
// counters. Only compiled routes are present, keyed by main, direct or exit ID.
func (e *Engine) Traffic() TrafficSnapshot { return e.traffic.snapshot() }
