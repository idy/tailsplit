package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"time"

	"github.com/idy/tailsplit"
	"github.com/idy/tailsplit/internal/appservice"

	singlog "github.com/sagernet/sing-box/log"
)

type eventWrite struct {
	data   []byte
	result chan error
}

type eventStream struct {
	ctx         context.Context
	cancel      context.CancelFunc
	writes      chan eventWrite
	done        chan struct{}
	err         error // Published by closing done.
	captureLogs bool
}

func newEventStream(ctx context.Context, out io.Writer, captureLogs bool) *eventStream {
	ctx, cancel := context.WithCancel(ctx)
	s := &eventStream{ctx: ctx, cancel: cancel, writes: make(chan eventWrite), done: make(chan struct{}), captureLogs: captureLogs}
	go s.writeEvents(out)
	return s
}

// close releases senders without closing caller-owned output or waiting for an
// arbitrary Writer. At most this stream's single worker can remain in Write;
// there is no event queue and no goroutine created for an individual send.
func (s *eventStream) close() { s.cancel() }

func (s *eventStream) writeEvents(out io.Writer) {
	defer close(s.done)
	for {
		select {
		case <-s.ctx.Done():
			s.err = s.ctx.Err()
			return
		case write := <-s.writes:
			if err := s.ctx.Err(); err != nil {
				s.err = err
				return
			}
			n, err := out.Write(write.data)
			if err == nil && n != len(write.data) {
				err = io.ErrShortWrite
			}
			if err != nil {
				s.err = err
				return
			}
			write.result <- nil
		}
	}
}

func (s *eventStream) outputError() error {
	select {
	case <-s.done:
		return s.err
	default:
		return s.ctx.Err()
	}
}

func (s *eventStream) send(event any) error {
	if err := s.outputError(); err != nil {
		return err
	}
	// Encode before handing off ownership: cancellation can release this sender
	// while Write is still blocked, so the worker must retain only its own bytes.
	data, err := json.Marshal(event)
	if err != nil {
		return err
	}
	write := eventWrite{data: append(data, '\n'), result: make(chan error, 1)}
	select {
	case <-s.ctx.Done():
		return s.outputError()
	case <-s.done:
		return s.err
	case s.writes <- write:
	}
	select {
	case err := <-write.result:
		return err
	case <-s.done:
		return s.err
	case <-s.ctx.Done():
		// Keep an already completed write's result, including a real I/O error,
		// when cancellation and its acknowledgement become ready together.
		select {
		case err := <-write.result:
			return err
		default:
			return s.outputError()
		}
	}
}

func (s *eventStream) WriteMessage(_ singlog.Level, message string) {
	_ = s.send(map[string]string{"event": "log", "message": message})
}

func runLoginEvents(ctx context.Context, config tailsplit.Config, stream *eventStream) error {
	return runLoginEventsWith(ctx, config, stream, tailsplit.AuthenticateTailnet)
}

func runNewLoginEvents(ctx context.Context, config tailsplit.Config, stream *eventStream) error {
	return runLoginEventsWith(ctx, config, stream, tailsplit.AuthenticateNewTailnet)
}

func runLoginEventsWith(ctx context.Context, config tailsplit.Config, stream *eventStream,
	authenticate func(context.Context, tailsplit.Config, func(tailsplit.Status), singlog.PlatformWriter) error) error {
	config.TUN.Enabled = false
	if err := stream.send(map[string]string{"event": "preflight", "mode": "login"}); err != nil {
		return err
	}
	if err := authenticate(ctx, config, func(status tailsplit.Status) {
		_ = stream.send(status)
	}, stream); err != nil {
		if ctx.Err() == nil {
			_ = stream.send(map[string]string{"event": "error", "message": err.Error()})
		}
		return err
	}
	return stream.send(map[string]string{"event": "authenticated", "mode": "login", "backend_state": "Running"})
}

func runDiscoveryEvents(ctx context.Context, config tailsplit.Config, stream *eventStream) error {
	config.TUN.Enabled = false
	if err := stream.send(map[string]string{"event": "preflight", "mode": "discovery"}); err != nil {
		return err
	}
	if err := tailsplit.DiscoverTailnet(ctx, config, func(status tailsplit.Status) {
		_ = stream.send(status)
	}, stream); err != nil {
		if ctx.Err() == nil {
			_ = stream.send(map[string]string{"event": "error", "message": "无法刷新出口列表；请检查网络和设备登录"})
		}
		return err
	}
	return stream.send(map[string]string{"event": "discovered", "mode": "discovery"})
}

func runEvents(ctx context.Context, config tailsplit.Config, compiled tailsplit.Compiled, stream *eventStream) error {
	return runEventsWithPreparation(ctx, config, compiled, stream, tailsplit.PrepareConnection)
}

func runEventsWithPreparation(ctx context.Context, config tailsplit.Config, compiled tailsplit.Compiled, stream *eventStream,
	prepare func(context.Context, tailsplit.Config, tailsplit.Compiled, func(tailsplit.Status), singlog.PlatformWriter) (tailsplit.Compiled, error)) (err error) {
	var logWriter singlog.PlatformWriter
	if stream.captureLogs {
		logWriter = stream
	}
	if len(config.ExitIDs()) > 0 {
		compiled, err = prepare(ctx, config, compiled, func(status tailsplit.Status) {
			_ = stream.send(status)
		}, logWriter)
		if err != nil {
			return err
		}
	}
	// Preparation can recompile to isolate devices or use observed DNS metadata.
	// Check that final projection before the traffic engine opens any state.
	if config.ExpectedRulesRevision != "" && config.ExpectedRulesRevision != compiled.RulesRevision {
		return tailsplit.ErrRulesChanged
	}
	engine, err := tailsplit.OpenWithLogWriter(ctx, config, compiled, logWriter)
	if err != nil {
		return err
	}
	if err := engine.Start(); err != nil {
		return err
	}
	// The later unregister defer joins in-flight pings before Close releases
	// the embedded client and state lock, including on early stream failures.
	defer func() { err = errors.Join(err, engine.Close()) }()
	unregisterPing := appservice.RegisterExitPing(ctx, engine.PingExitNode)
	defer unregisterPing()
	mode := "proxy"
	if config.TUN.Enabled {
		mode = "tun"
	}
	if err := stream.send(map[string]string{
		"event": "ready", "mode": mode, "routing_mode": config.Rules.Mode,
		"rules_revision": compiled.RulesRevision,
	}); err != nil {
		return err
	}
	go func() {
		_ = engine.WatchStatus(ctx, func(status tailsplit.Status) {
			_ = stream.send(status)
		})
	}()
	return streamTraffic(ctx, engine.Traffic, stream)
}

// The same event stream is forwarded unchanged by appservice, including when
// the engine runs in the privileged service. Sample only live traffic sessions.
func streamTraffic(ctx context.Context, sample func() tailsplit.TrafficSnapshot, stream *eventStream) error {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		if err := ctx.Err(); err != nil {
			return nil
		}
		if err := stream.send(struct {
			Event   string                    `json:"event"`
			Traffic tailsplit.TrafficSnapshot `json:"traffic"`
		}{Event: "traffic", Traffic: sample()}); err != nil {
			if ctx.Err() != nil && errors.Is(err, ctx.Err()) {
				return nil
			}
			return err
		}
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
	}
}
