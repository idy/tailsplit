package main

import "sync/atomic"

// configurationFallback joins synchronous Show failures with the terminal
// asynchronous Run failure, without opening two browser tabs for one request.
// A later explicit Show starts a new attempt even if the native host is gone.
type configurationFallback struct {
	opened atomic.Bool
	launch func()
}

func (f *configurationFallback) beginShow() { f.opened.Store(false) }

func (f *configurationFallback) open() {
	if f.opened.CompareAndSwap(false, true) {
		f.launch()
	}
}

func (f *configurationFallback) nativeFailed(wantsVisible bool) {
	if wantsVisible {
		f.open()
	}
}
