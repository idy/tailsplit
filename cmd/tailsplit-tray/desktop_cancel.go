package main

import (
	"context"
	"sync"

	"github.com/idy/tailsplit/internal/tray"
)

// Polling may offer a request only after safe window-pointer publication and
// while that window can actually receive it. Initial load and late dead-page
// requests cannot reserve an otherwise eligible browser fallback.
func observeLiveNativeConnectRequests(controller *tray.Controller, ready <-chan struct{}, live func() bool) func(string) {
	return func(request string) {
		select {
		case <-ready:
			controller.ObserveNativeConnectRequest(request, live)
		default:
		}
	}
}

// Cancellation records identities without touching the event-loop-owned flow or
// waiting for its action worker. The wakeup is coalesced and never blocks Stop.
type desktopLoginCancellations struct {
	mu      sync.Mutex
	pending map[string]bool
	wake    chan struct{}
}

func newDesktopLoginCancellations() *desktopLoginCancellations {
	return &desktopLoginCancellations{wake: make(chan struct{}, 1)}
}

func (c *desktopLoginCancellations) callback(controller *tray.Controller) func(context.Context, string, string) error {
	return func(_ context.Context, request, operation string) error {
		return controller.CancelWithNotify(request, operation, c.record)
	}
}

func (c *desktopLoginCancellations) record(operations []string) {
	c.mu.Lock()
	if c.pending == nil {
		c.pending = make(map[string]bool)
	}
	for _, id := range operations {
		if id != "" {
			c.pending[id] = true
		}
	}
	c.mu.Unlock()
	select {
	case c.wake <- struct{}{}:
	default:
	}
}

// Apply is called exclusively on the LoginFlow's owning event loop, including
// before browser dispatch and after an action worker completes.
func (c *desktopLoginCancellations) apply(flow *tray.LoginFlow) {
	if c == nil {
		return
	}
	c.mu.Lock()
	pending := c.pending
	c.pending = nil
	c.mu.Unlock()
	for id := range pending {
		flow.CancelOperation(id)
	}
}

func (c *desktopLoginCancellations) events() <-chan struct{} {
	if c == nil {
		return nil
	}
	return c.wake
}
