package desktopwindow

import (
	"context"
	"errors"
	"log"
	"net/url"
	"runtime"
	"strconv"
	"sync/atomic"
	"time"

	"github.com/egoist/mygo"
	"github.com/idy/tailsplit/internal/tray"
)

const trayPopupLoadTimeout = 5 * time.Second

var errTrayPopupURL = errors.New("tray popup requires a local /tray.html capability URL")

// TrayPopup owns a short-lived, tray-anchored page. Its native window must stay
// strongly referenced until OnClosed: MyGo's window registry uses weak pointers.
// Toggle and Close, including their callbacks, run on the MyGo UI thread.
// Unlike Window, a popup neither restores the Dock icon nor handles activation.
type TrayPopup struct {
	url     string
	port    uint16
	attempt *popupAttempt
}

type popupAttempt struct {
	load     *pageLoad
	native   *mygo.Window
	loaded   bool
	closed   bool
	fallback func()
	failure  string // Fixed reason codes only; never WebView errors or URLs.
	finished bool   // OnDidFinishLoad observed, on the UI thread.
	probing  atomic.Bool
	observed atomic.Pointer[popupPageObservation]
}

// Only fixed booleans cross from JavaScript into diagnostics. Page text, URL
// fragments, console messages and evaluation errors can contain capabilities.
type popupPageObservation struct {
	Ready    bool `json:"ready"`
	Complete bool `json:"document_complete"`
	Marker   bool `json:"ready_marker"`
	Hidden   bool `json:"document_hidden"`
	Body     bool `json:"body_present"`
	I18n     bool `json:"i18n_present"`
	Failed   bool `json:"evaluation_failed"`
}

// NewTrayPopup validates the URL without creating a native window. The page must
// set document.documentElement.dataset.trayReady = 'true' when it can be shown.
func NewTrayPopup(rawURL string) (*TrayPopup, error) {
	port, err := tray.ConfigurationWindowPort(rawURL)
	if err != nil {
		return nil, err
	}
	u, err := url.Parse(rawURL)
	if err != nil || u.Path != "/tray.html" || u.Fragment == "" {
		return nil, errTrayPopupURL
	}
	return &TrayPopup{url: rawURL, port: port}, nil
}

// Toggle opens a fresh ephemeral popup, or closes an existing/loading one. A
// failed WebView initialization, navigation or readiness probe invokes fallback
// after disposing of the failed window. Closing the popup never quits the app.
func (p *TrayPopup) Toggle(anchor *mygo.Tray, fallback func()) {
	if p.attempt != nil {
		p.Close()
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), trayPopupLoadTimeout)
	a := &popupAttempt{load: &pageLoad{ctx: ctx, cancel: cancel}, fallback: fallback}
	p.attempt = a
	defer func() {
		if recover() != nil {
			// Native construction errors may contain the capability URL.
			p.failWithReason(a, "native-create")
		}
	}()
	if !mygo.WebViewAvailable() {
		p.failWithReason(a, "runtime-unavailable")
		return
	}
	origin := "http://127.0.0.1:" + strconv.Itoa(int(p.port))
	// Load only after every policy and lifecycle handler is installed.
	a.native = mygo.NewFlyout(mygo.FlyoutOptions{
		Tray: anchor, Placement: mygo.PlacementBottom, Gap: 4,
		Width: 360, Height: 320, FitContent: true,
		MinWidth: 360, MaxWidth: 360, MinHeight: 240, MaxHeight: 420,
		Hidden: true, Focusable: true, Shadow: true, Popover: runtime.GOOS == "darwin",
		Page: mygo.PageOptions{Session: mygo.NewEphemeralSession(), TrustedOrigins: []string{origin}, DevTools: mygo.DevToolsDisabled},
	})
	frame := a.native
	configurePagePolicy(frame.Page(), p.port)
	frame.OnClosed(func() { p.closed(a) })
	frame.Page().OnDidFinishLoad(func() { a.finished = true })
	frame.Page().OnDidFailLoad(func(*mygo.LoadError) { p.failWithReason(a, "page-load") })
	frame.Page().OnRenderProcessGone(func(string) { p.failWithReason(a, "render-process") })
	if err := frame.Page().LoadURL(p.url); err != nil {
		p.failWithReason(a, "load-url")
		return
	}
	go func() {
		err := probeTrayPopupReadiness(ctx, func(ctx context.Context, code string) (any, error) {
			return a.observeReadiness(ctx, code, frame.Page().EvalContext)
		}, origin, pageProbeInterval)
		if errors.Is(err, context.Canceled) {
			return
		}
		mygo.RunOnMain(func() {
			if !p.pending(a) {
				return
			}
			if err != nil {
				reason := "readiness-timeout"
				if errors.Is(err, errWebViewReadiness) {
					reason = "readiness-evaluation"
				}
				p.failWithReason(a, reason)
				return
			}
			if !tray.ConfigurationWindowAllows(p.port, frame.Page().URL()) {
				p.failWithReason(a, "page-origin")
				return
			}
			a.loaded = true
			a.load.cancel()
			frame.Show()
			// AppKit can decline to show a popover whose status item has no
			// usable anchor. A loaded but invisible popup must also fall back.
			time.AfterFunc(time.Second, func() {
				mygo.RunOnMain(func() {
					if p.current(a) && !frame.IsVisible() {
						p.failWithReason(a, "native-not-visible")
					}
				})
			})
		})
	}()
}

// Close also cancels a pending open, so a late readiness result cannot reopen it
// or replace a deliberate dismissal with a fallback menu.
func (p *TrayPopup) Close() {
	if a := p.attempt; a != nil {
		a.load.cancel()
		if a.native != nil && !a.native.IsDestroyed() {
			a.native.Close()
		} else {
			p.closed(a)
		}
	}
}

func (p *TrayPopup) pending(a *popupAttempt) bool {
	return p.current(a) && !a.loaded && !errors.Is(a.load.ctx.Err(), context.Canceled)
}

func (p *TrayPopup) current(a *popupAttempt) bool {
	return a != nil && p.attempt == a && !a.closed
}

func (p *TrayPopup) closed(a *popupAttempt) {
	a.load.cancel()
	a.closed = true
	// Do not let a delayed close notification discard a newer window.
	if p.attempt == a {
		p.attempt = nil
	}
	a.native = nil
}

func (p *TrayPopup) failWithReason(a *popupAttempt, reason string) {
	if p.current(a) {
		a.failure = reason
	}
	p.fail(a)
}

func (p *TrayPopup) fail(a *popupAttempt) {
	if !p.current(a) || errors.Is(a.load.ctx.Err(), context.Canceled) && !a.loaded {
		return
	}
	// Mark the attempt before Destroy synchronously emits OnClosed. The failure
	// callback runs at most once, including a crash after the popup was shown.
	a.closed = true
	a.load.cancel()
	if a.native != nil && !a.native.IsDestroyed() {
		a.native.Destroy()
	} else {
		p.closed(a)
	}
	log.Printf("desktoptray: popup unavailable (reason=%s, loaded=%t, document_finished=%t)", a.failure, a.loaded, a.finished)
	if a.fallback != nil {
		a.fallback()
	}
}

func (a *popupAttempt) observeReadiness(ctx context.Context, code string, eval func(context.Context, string) (any, error)) (any, error) {
	a.probing.Store(true)
	defer a.probing.Store(false)
	value, err := eval(ctx, `(() => ({ready: (`+code+`), complete: document.readyState === 'complete', marker: document.documentElement.dataset.trayReady === 'true', hidden: document.hidden, body: Boolean(document.body), i18n: Boolean(window.TailSplitI18n)}))()`)
	observation := &popupPageObservation{Failed: err != nil}
	if values, ok := value.(map[string]any); ok {
		observation.Ready, _ = values["ready"].(bool)
		observation.Complete, _ = values["complete"].(bool)
		observation.Marker, _ = values["marker"].(bool)
		observation.Hidden, _ = values["hidden"].(bool)
		observation.Body, _ = values["body"].(bool)
		observation.I18n, _ = values["i18n"].(bool)
	}
	a.observed.Store(observation)
	return observation.Ready, err
}

func probeTrayPopupReadiness(ctx context.Context, eval func(context.Context, string) (any, error), origin string, interval time.Duration) error {
	// Wrap the existing bounded probe so runtime errors receive the same secret-
	// free handling. The marker excludes HTTP error pages and unrendered shells.
	return probePageReadiness(ctx, func(ctx context.Context, code string) (any, error) {
		return eval(ctx, code+` && location.pathname === '/tray.html' && document.documentElement.dataset.trayReady === 'true'`)
	}, origin, interval)
}

func trayPopupOpen() bool {
	for _, frame := range mygo.Windows() {
		if frame.IsFlyout() && !frame.IsDestroyed() {
			return true
		}
	}
	return false
}
