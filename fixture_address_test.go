package tailsplit

import (
	"errors"
	"net"
	"os"
	"testing"
)

func TestFixtureAddressAvoidsTCPExcludedPorts(t *testing.T) {
	// Model Windows choosing UDP ports from a TCP-excluded range. TCP's
	// allocator can still choose an available ephemeral port of its own.
	listen := func(network, address string) (net.Listener, error) {
		if address != "127.0.0.1:0" {
			return nil, os.ErrPermission
		}
		return net.Listen(network, address)
	}
	address, err := fixtureAddress(listen, net.ListenPacket)
	if err != nil {
		t.Fatal("UDP-selected excluded ports prevented a TCP/UDP fixture:", err)
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
				listener, err := net.Listen(network, address)
				if err != nil {
					return nil, err
				}
				t.Cleanup(func() { _ = listener.Close() })
				probe := &fixtureProbeListener{Listener: listener}
				listeners = append(listeners, probe)
				return probe, nil
			}
			attempts := 0
			var packetProbe *fixtureProbePacketConn
			listenPacket := func(network, address string) (net.PacketConn, error) {
				attempts++
				if attempts <= test.failures {
					return nil, os.ErrPermission
				}
				packets, err := net.ListenPacket(network, address)
				if err != nil {
					return nil, err
				}
				t.Cleanup(func() { _ = packets.Close() })
				packetProbe = &fixtureProbePacketConn{PacketConn: packets}
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
	closed bool
}

func (p *fixtureProbeListener) Close() error {
	p.closed = true
	return p.Listener.Close()
}

type fixtureProbePacketConn struct {
	net.PacketConn
	closed bool
}

func (p *fixtureProbePacketConn) Close() error {
	p.closed = true
	return p.PacketConn.Close()
}
