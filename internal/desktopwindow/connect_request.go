package desktopwindow

// SetConnectRequestHandler reserves a pending Connect request at the host before
// an interactive native page can act on it. A later browser fallback must not
// replay that request when delivery of a native cancellation is uncertain.
func (w *Window) SetConnectRequestHandler(handler func()) {
	w.mu.Lock()
	w.connectRequestHandler = handler
	w.mu.Unlock()
}

func (w *Window) offerConnectRequest() {
	if w.isDone() || w.quitting.Load() {
		return
	}
	w.mu.Lock()
	handler := w.connectRequestHandler
	w.mu.Unlock()
	if handler != nil {
		handler()
	}
}

// CanPresentConnectRequest is checked under the controller's request lock at
// first presentation, so a queued Show cannot reserve requests after teardown.
func (w *Window) CanPresentConnectRequest() bool {
	return !w.isDone() && !w.quitting.Load() && !w.rendererUnavailable.Load()
}

// CanReceiveConnectRequest also rejects late HTTP reads from a dead renderer;
// they must not reserve a fresh browser request after native failure.
func (w *Window) CanReceiveConnectRequest() bool {
	return w.everShown.Load() && w.CanPresentConnectRequest()
}

func (w *Window) offerConnectRequestIfShown() {
	if w.CanReceiveConnectRequest() {
		w.offerConnectRequest()
	}
}
