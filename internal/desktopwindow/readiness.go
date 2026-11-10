package desktopwindow

import (
	"context"
	"errors"
	"strconv"
	"time"
)

const (
	pageLoadTimeout   = 30 * time.Second
	pageProbeInterval = 100 * time.Millisecond
)

var (
	errPanelLoad        = errors.New("could not load the TailSplit configuration panel")
	errWebViewReadiness = errors.New("could not initialize the system WebView for the TailSplit configuration panel")
	errPanelLoadTimeout = errors.New("timed out loading the TailSplit configuration panel")
)

type pageLoad struct {
	ctx    context.Context
	cancel context.CancelFunc
}

func newPageLoad() *pageLoad {
	ctx, cancel := context.WithTimeout(context.Background(), pageLoadTimeout)
	return &pageLoad{ctx: ctx, cancel: cancel}
}

// probePageReadiness also observes asynchronous WebView2 environment/controller
// failures: MyGo returns them from EvalContext without emitting OnDidFailLoad.
// An evaluation may race navigation, so retry until the load's bounded deadline.
// Neither returned errors nor the script include the capability-bearing page URL.
func probePageReadiness(ctx context.Context, eval func(context.Context, string) (any, error), origin string, interval time.Duration) error {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	code := `document.readyState === 'complete' && location.origin === ` + strconv.Quote(origin)
	var lastError error
	failure := func() error {
		if errors.Is(ctx.Err(), context.Canceled) {
			return context.Canceled
		}
		if lastError != nil {
			return errWebViewReadiness
		}
		return errPanelLoadTimeout
	}
	for {
		if ctx.Err() != nil {
			return failure()
		}
		value, err := eval(ctx, code)
		if ctx.Err() != nil {
			return failure()
		}
		if err == nil && value == true {
			return nil
		}
		lastError = err
		select {
		case <-ctx.Done():
			return failure()
		case <-ticker.C:
		}
	}
}

// These methods run on the UI thread, including the callback accepting a probe's
// result. Clearing the attempt invalidates an already queued result as well as
// canceling an evaluation that has not answered yet.
func (w *Window) stopLoadProbe() {
	if w.load != nil {
		w.load.cancel()
		w.load = nil
	}
}

func (w *Window) currentLoad(load *pageLoad, destroyed bool) bool {
	return load != nil && w.load == load && !w.loaded && !w.quitting.Load() && !destroyed && !w.isDone()
}

func (w *Window) failLoad(load *pageLoad, destroyed bool, err error, destroy func()) {
	if err == nil || errors.Is(err, context.Canceled) || !w.currentLoad(load, destroyed) {
		return
	}
	w.stopLoadProbe()
	// Destroy emits OnClosed synchronously. Publish the failure first so its nil
	// completion cannot win and silently discard the startup error.
	w.complete(err)
	destroy()
}

func createWindowOnRun(platform string, visible, proof bool) bool {
	return platform != "darwin" || visible || proof
}
