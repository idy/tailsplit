package main

import (
	"context"
	"encoding/json"
	"errors"
	"sync"

	"github.com/idy/tailsplit"
	"github.com/idy/tailsplit/internal/appservice"

	singlog "github.com/sagernet/sing-box/log"
)

type eventStream struct {
	mu          sync.Mutex
	encoder     *json.Encoder
	captureLogs bool
}

func (s *eventStream) send(event any) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.encoder.Encode(event)
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

func runEvents(ctx context.Context, config tailsplit.Config, compiled tailsplit.Compiled, stream *eventStream) (err error) {
	var logWriter singlog.PlatformWriter
	if stream.captureLogs {
		logWriter = stream
	}
	if len(config.ExitIDs()) > 0 {
		compiled, err = tailsplit.PrepareConnection(ctx, config, compiled, func(status tailsplit.Status) {
			_ = stream.send(status)
		}, logWriter)
		if err != nil {
			return err
		}
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
	}); err != nil {
		return err
	}
	go func() {
		_ = engine.WatchStatus(ctx, func(status tailsplit.Status) {
			_ = stream.send(status)
		})
	}()
	<-ctx.Done()
	return nil
}
