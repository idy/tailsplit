package tailsplit

import (
	"errors"
	"net"
	"os"
	"testing"
)

func TestFixtureAddressReleasesTCPAndUDPProbes(t *testing.T) {
	address, err := fixtureAddress(net.Listen, net.ListenPacket)
	if err != nil {
		t.Fatal("could not find a port usable by both TCP and UDP:", err)
	}
	listener, err := net.Listen("tcp4", address)
	if err != nil {
		t.Fatal("fixture did not release its TCP probe:", err)
	}
	defer listener.Close()
	packets, err := net.ListenPacket("udp4", address)
	if err != nil {
		t.Fatal("fixture did not release its UDP probe:", err)
	}
	defer packets.Close()
}

func TestFixtureAddressHandlesUDPExcludedPorts(t *testing.T) {
	for _, test := range []struct {
		name     string
		failures int
		wantErr  bool
	}{
		{"retry", 2, false},
		{"exhausted", 100, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			var listeners []*fixtureProbeListener
			listen := func(network, address string) (net.Listener, error) {
				candidate, err := net.ResolveTCPAddr(network, address)
				if err != nil {
					t.Fatal(err)
				}
				if len(listeners) == 0 {
					if candidate.Port != 0 {
						t.Fatal("first attempt did not use the TCP allocator")
					}
					candidate.Port = 55000
				} else if candidate.Port < 1024 || candidate.Port > 65535 {
					t.Fatal("retry reused the excluded allocator range or a privileged port:", address)
				}
				probe := &fixtureProbeListener{address: candidate}
				listeners = append(listeners, probe)
				return probe, nil
			}
			attempts := 0
			var packetProbe *fixtureProbePacketConn
			listenPacket := func(network, address string) (net.PacketConn, error) {
				probe := listeners[len(listeners)-1]
				if network != "udp4" || address != probe.Addr().String() || probe.closed {
					t.Fatal("UDP probe must use the still-bound TCP port")
				}
				attempts++
				if attempts <= test.failures {
					return nil, os.ErrPermission
				}
				// Keep retry counts independent of real UDP port availability.
				// The real TCP/UDP bind is covered by the test above.
				packetProbe = &fixtureProbePacketConn{}
				return packetProbe, nil
			}
			address, err := fixtureAddress(listen, listenPacket)
			if test.wantErr {
				if address != "" || !errors.Is(err, os.ErrPermission) {
					t.Fatalf("lost the UDP bind failure: address=%q err=%v", address, err)
				}
			} else if err != nil || address == "" {
				t.Fatalf("did not recover from UDP-excluded ports: address=%q err=%v", address, err)
			}
			wantAttempts := test.failures
			if !test.wantErr {
				wantAttempts++
			}
			if attempts != wantAttempts || len(listeners) != wantAttempts {
				t.Fatalf("unexpected probes: UDP=%d TCP=%d want=%d", attempts, len(listeners), wantAttempts)
			}
			for _, listener := range listeners {
				if !listener.closed {
					t.Error("TCP probe leaked after a UDP bind attempt")
				}
			}
			if !test.wantErr && (packetProbe == nil || !packetProbe.closed) {
				t.Error("successful UDP probe was not closed")
			}
		})
	}
}

func TestFixtureAddressRetriesTCPExcludedFallbackPorts(t *testing.T) {
	tcpAttempts, udpAttempts := 0, 0
	var probes []*fixtureProbeListener
	address, err := fixtureAddress(func(network, address string) (net.Listener, error) {
		tcpAttempts++
		if tcpAttempts == 2 {
			return nil, os.ErrPermission
		}
		candidate, err := net.ResolveTCPAddr(network, address)
		if err != nil {
			t.Fatal(err)
		}
		if candidate.Port == 0 {
			candidate.Port = 55000
		}
		probe := &fixtureProbeListener{address: candidate}
		probes = append(probes, probe)
		return probe, nil
	}, func(string, string) (net.PacketConn, error) {
		udpAttempts++
		if udpAttempts == 1 {
			return nil, os.ErrPermission
		}
		return &fixtureProbePacketConn{}, nil
	})
	if err != nil || address == "" || tcpAttempts != 3 || udpAttempts != 2 {
		t.Fatalf("did not recover from UDP and TCP exclusions: %q, TCP=%d UDP=%d err=%v", address, tcpAttempts, udpAttempts, err)
	}
	for _, probe := range probes {
		if !probe.closed {
			t.Fatal("fallback TCP probe leaked")
		}
	}
}

func TestFixtureAddressReportsTCPBindFailure(t *testing.T) {
	address, err := fixtureAddress(
		func(string, string) (net.Listener, error) { return nil, os.ErrPermission },
		func(string, string) (net.PacketConn, error) {
			t.Error("UDP was probed after TCP allocation failed")
			return nil, os.ErrPermission
		},
	)
	if address != "" || !errors.Is(err, os.ErrPermission) {
		t.Fatalf("lost the TCP allocation failure: address=%q err=%v", address, err)
	}
}

type fixtureProbeListener struct {
	net.Listener
	address net.Addr
	closed  bool
}

func (p *fixtureProbeListener) Addr() net.Addr { return p.address }

func (p *fixtureProbeListener) Close() error {
	p.closed = true
	return nil
}

type fixtureProbePacketConn struct {
	net.PacketConn
	closed bool
}

func (p *fixtureProbePacketConn) Close() error {
	p.closed = true
	return nil
}
