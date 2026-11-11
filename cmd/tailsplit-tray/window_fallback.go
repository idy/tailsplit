package main

import (
	"context"
	"sync/atomic"
)

// configurationFallback joins synchronous Show failures with the terminal
// asynchronous Run failure, without opening two browser tabs for one request.
// A later explicit Show starts a new attempt even if the native host is gone.
type configurationFallback struct {
	opened atomic.Bool
	launch func()
	ctx    context.Context // Nil preserves legacy hosts without a bound lifetime.
}

func (f *configurationFallback) beginShow() { f.opened.Store(false) }

func (f *configurationFallback) appRunning() bool { return f.ctx == nil || f.ctx.Err() == nil }

func (f *configurationFallback) open() {
	if f.appRunning() && f.opened.CompareAndSwap(false, true) && f.appRunning() {
		f.launch()
	}
}

// A view failure hands off presentation only. Controller, tray and native
// recovery retain their lifetimes until an explicit application quit.
func (f *configurationFallback) nativeFailure(err error, wantsVisible bool, report func(error)) {
	if err == nil || !f.appRunning() {
		return
	}
	if report != nil {
		report(err)
	}
	f.nativeFailed(wantsVisible)
}

func (f *configurationFallback) nativeFailed(wantsVisible bool) {
	if wantsVisible {
		f.open()
	}
}
