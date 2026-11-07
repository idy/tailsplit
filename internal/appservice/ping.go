package appservice

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"net"
	"time"

	"github.com/idy/tailsplit"
)

type exitPingRegistrarKey struct{}
type exitPingHandler func(context.Context, string) (tailsplit.ExitPingResult, error)
type exitPingRegistrar func(exitPingHandler) func()

// RegisterExitPing publishes only the engine owned by this service session.
// Call its returned cleanup before closing the engine; cleanup joins any ping.
// Non-service CLI sessions have no registrar and advertise no remote capability.
func RegisterExitPing(ctx context.Context, handler func(context.Context, string) (tailsplit.ExitPingResult, error)) func() {
	if register, ok := ctx.Value(exitPingRegistrarKey{}).(exitPingRegistrar); ok {
		return register(handler)
	}
	return func() {}
}

func (s *Server) pingContext(ctx context.Context) context.Context {
	return context.WithValue(ctx, exitPingRegistrarKey{}, exitPingRegistrar(func(handler exitPingHandler) func() {
		s.pingMu.Lock()
		s.pingLive = func(requestCtx context.Context, id string) (tailsplit.ExitPingResult, error) {
			requestCtx, cancel := context.WithCancel(requestCtx)
			defer cancel()
			stop := context.AfterFunc(ctx, cancel)
			defer stop()
			return handler(requestCtx, id)
		}
		s.pingMu.Unlock()
		return func() {
			s.pingMu.Lock()
			defer s.pingMu.Unlock()
			s.pingLive = nil
		}
	}))
}

func (s *Server) handlePing(ctx context.Context, connection net.Conn, reader *bufio.Reader, request Request, stream *eventWriter) {
	ctx, cancel := context.WithTimeout(ctx, tailsplit.ExitPingTimeout)
	defer cancel()
	watched := make(chan struct{})
	go func() {
		defer close(watched)
		var command [1]byte
		_, _ = reader.Read(command[:])
		cancel()
	}()
	defer func() {
		_ = connection.SetReadDeadline(time.Now())
		<-watched
	}()
	result, err := s.pingExit(ctx, request)
	reply := tailsplit.ExitPingReply{Result: &result}
	if err != nil {
		reply = tailsplit.ExitPingReply{Error: tailsplit.PingErrorCode(err)}
	}
	_ = stream.send(reply)
}

func (s *Server) pingExit(ctx context.Context, request Request) (tailsplit.ExitPingResult, error) {
	if request.Target == "" || len(request.Target) > 256 {
		return tailsplit.ExitPingResult{}, tailsplit.ErrPingTarget
	}
	if s.Ping == nil {
		return tailsplit.ExitPingResult{}, tailsplit.ErrPingUnavailable
	}
	if !s.pingGate.TryLock() {
		return tailsplit.ExitPingResult{}, tailsplit.ErrPingBusy
	}
	defer s.pingGate.Unlock()
	if !s.active.TryLock() {
		// Startup, login, discovery and logout also hold active. Only a fully
		// started network engine registers a handler; none can be interrupted.
		s.pingMu.Lock()
		defer s.pingMu.Unlock()
		if s.pingLive == nil {
			return tailsplit.ExitPingResult{}, tailsplit.ErrPingBusy
		}
		return s.pingLive(ctx, request.Target)
	}
	defer s.active.Unlock()
	if request.Config == nil {
		return tailsplit.ExitPingResult{}, tailsplit.ErrPingUnavailable
	}
	config, err := serviceConfig(*request.Config, runtimeUserDirectory(s.Base, s.userKey()))
	if err != nil {
		return tailsplit.ExitPingResult{}, tailsplit.ErrPingUnavailable
	}
	config.TUN.Enabled = false
	config.Rules.GFWList = ""
	if _, err := prepareRuntimeUser(s.Base, s.userKey(), request.Files); err != nil {
		return tailsplit.ExitPingResult{}, tailsplit.ErrPingUnavailable
	}
	return s.Ping(ctx, config, request.Target)
}

// PingExitNode never installs or restarts the network service. The service
// chooses its existing engine or a scoped listener-free saved-device session.
func PingExitNode(ctx context.Context, executable string, config tailsplit.Config, id string) (tailsplit.ExitPingResult, error) {
	ctx, cancel := context.WithTimeout(ctx, tailsplit.ExitPingTimeout)
	defer cancel()
	config.TUN.Enabled = false
	config.Rules.GFWList = ""
	connection, err := openSession(ctx, executable, "ping", config, io.Discard, id)
	if err != nil {
		return tailsplit.ExitPingResult{}, err
	}
	defer connection.Close()
	return readPingReply(ctx, connection)
}

func readPingReply(ctx context.Context, connection net.Conn) (tailsplit.ExitPingResult, error) {
	stop := context.AfterFunc(ctx, func() { _ = connection.Close() })
	defer stop()
	if deadline, ok := ctx.Deadline(); ok {
		_ = connection.SetDeadline(deadline)
	}
	var reply tailsplit.ExitPingReply
	err := json.NewDecoder(io.LimitReader(connection, 4096)).Decode(&reply)
	if ctx.Err() != nil {
		return tailsplit.ExitPingResult{}, ctx.Err()
	}
	if err != nil {
		return tailsplit.ExitPingResult{}, tailsplit.ErrPingFailed
	}
	if reply.Error != "" {
		return tailsplit.ExitPingResult{}, tailsplit.PingError(reply.Error)
	}
	if reply.Result == nil {
		return tailsplit.ExitPingResult{}, tailsplit.ErrPingUnavailable
	}
	return *reply.Result, nil
}
