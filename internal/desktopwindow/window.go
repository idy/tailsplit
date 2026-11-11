// Package desktopwindow hosts TailSplit's local panel in MyGo's system WebView.
package desktopwindow

import (
	"context"
	"errors"
	"fmt"
	"os"
	"runtime"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/egoist/mygo"
	"github.com/idy/tailsplit/internal/tray"
)

var ErrNotReady = errors.New("TailSplit window is not ready")

// Window keeps its page alive while hidden, preserving unsaved settings drafts.
// MyGo owns the sole native event loop; Run only waits for this window's lifetime.
type Window struct {
	url                   string
	port                  uint16
	native                *mygo.Window // UI thread only.
	loaded                bool         // UI thread only.
	preparingJournal      bool         // UI thread: source navigation waits for the keeper reader.
	load                  *pageLoad    // UI thread only.
	visible               atomic.Bool
	quitting              atomic.Bool
	everShown             atomic.Bool
	rendererUnavailable   atomic.Bool
	start                 sync.Once
	create                sync.Once
	finish                sync.Once
	done                  chan struct{}
	mu                    sync.Mutex
	err                   error
	cancelHandler         func(context.Context, string, string) error
	navigationInbox       *nativeCancellationInbox // Protected by mu; survives renderer destruction.
	connectRequestHandler func()
	journalScope          string
	journal               *cancellationJournal // UI thread setup; immutable after creation.
	journalContext        context.Context
	stopJournal           context.CancelFunc
	recoveryDispatch      func(func()) // Test UI dispatcher; nil uses MyGo's main thread.
	releaseJournalSession func()       // UI thread: releases the isolated private-session reader.
	journalRelease        sync.Once
}

func New(rawURL string, background bool) (*Window, error) {
	port, err := tray.ConfigurationWindowPort(rawURL)
	if err != nil {
		return nil, err
	}
	journalURL, scope := cancellationJournalURL(rawURL)
	journalContext, stopJournal := context.WithCancel(context.Background())
	w := &Window{url: journalURL, port: port, done: make(chan struct{}), journalScope: scope, journalContext: journalContext, stopJournal: stopJournal}
	w.visible.Store(!background)
	// Register application handlers even when a background macOS launch defers
	// its native window until Show (including a second-instance request).
	mygo.App.OnBeforeQuit(func(*mygo.QuitEvent) {
		w.quitting.Store(true)
		w.stopCancellationRecovery()
		w.stopLoadProbe()
	})
	mygo.App.OnQuit(func() {
		w.stopCancellationRecovery()
		w.stopLoadProbe()
		w.complete(nil)
	})
	mygo.App.OnSecondInstance(func([]string, string) { mygo.App.WhenReady(func() { _ = w.Show() }) })
	mygo.App.OnActivate(func(hasVisible bool) {
		if !hasVisible && w.everShown.Load() && !w.quitting.Load() && !trayPopupOpen() {
			_ = w.Show()
		}
	})
	return w, nil
}

func (w *Window) complete(err error) {
	w.finish.Do(func() { w.mu.Lock(); w.err = err; w.mu.Unlock(); close(w.done) })
}

func (w *Window) isDone() bool {
	select {
	case <-w.done:
		return true
	default:
		return false
	}
}

func (w *Window) result() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.err
}

func (w *Window) schedule() {
	w.start.Do(func() { mygo.App.WhenReady(w.createWindow) })
}

// Run may start before App.Run, on a worker goroutine. It does not own an OS loop.
// Background macOS launches keep only the tray until the first Show; Windows and
// proof runs retain their eager window creation.
func (w *Window) Run() error {
	if !w.isDone() && createWindowOnRun(runtime.GOOS, w.visible.Load(), os.Getenv("TAILSPLIT_DESKTOP_PROOF_DIR") != "") {
		w.schedule()
	}
	<-w.done
	return w.result()
}

func (w *Window) createWindow() {
	w.create.Do(func() {
		defer func() {
			if recover() != nil {
				w.rendererUnavailable.Store(true)
				w.stopLoadProbe()
				w.complete(errors.New("could not create the TailSplit window"))
				if w.native != nil && !w.native.IsDestroyed() {
					w.native.Destroy()
				}
				if w.releaseJournalSession != nil {
					w.releaseJournalAfterRecovery(w.beginCancellationRecovery())
				}
			}
		}()
		if w.quitting.Load() || w.isDone() {
			w.complete(nil)
			return
		}
		if !mygo.WebViewAvailable() {
			w.complete(errors.New("the system WebView runtime is unavailable"))
			return
		}
		height := 44
		var trafficLights *mygo.Point
		if runtime.GOOS == "darwin" {
			height = 40
			// Give AppKit's 14-point buttons equal top and left insets.
			inset := (height - 14) / 2
			trafficLights = &mygo.Point{X: inset, Y: inset}
		}
		origin := normalizePageOrigin("http://127.0.0.1:" + strconv.Itoa(int(w.port)))
		// Leave URL empty: no page may load before all policies and lifecycle
		// handlers are registered, including permission and download denial.
		session := mygo.NewEphemeralSession()
		w.native = mygo.NewWindow(mygo.WindowOptions{
			Title: "TailSplit", Hidden: true,
			Width: 1120, Height: 780, MinWidth: 760, MinHeight: 560, UseContentSize: true,
			TitleBarStyle: mygo.TitleBarHidden, TitleBarHeight: height, TrafficLightPosition: trafficLights,
			HideWindowButtons: runtime.GOOS == "windows", DisableFullScreen: runtime.GOOS == "darwin",
			Page: mygo.PageOptions{Session: session, TrustedOrigins: []string{origin}, DevTools: mygo.DevToolsDisabled},
		})
		frame := w.native
		// Keep the ephemeral store independently of the renderer. The failed
		// main page can be destroyed before replay, freezing further JS writes.
		keeper := mygo.NewWindow(mygo.WindowOptions{
			Title: "Recovery Store", Hidden: true, Width: 1, Height: 1,
			TitleBarStyle: mygo.TitleBarHidden, HideWindowButtons: true,
			Page: mygo.PageOptions{Session: session, Untrusted: true, DevTools: mygo.DevToolsDisabled},
		})
		w.releaseJournalSession = keeper.Destroy
		w.journal = newCancellationJournal(session, w.journalScope, origin)
		storage := newJournalStorageReader(w.journalScope, origin, keeper.Page().EvalContext)
		backup := &negotiatedJournalBackup{storage: storage}
		w.journal.backup = backup.recoveryStore()
		readerURL := origin + journalReaderPath
		configurePagePolicy(keeper.Page(), w.port)
		keeper.Page().OnWillNavigate(func(event *mygo.NavigateEvent) {
			if !journalReaderAllows(origin, event.URL) {
				event.PreventDefault()
			}
		})
		keeper.Page().OnRenderProcessGone(func(string) {
			if !w.quitting.Load() && !keeper.IsDestroyed() {
				_ = keeper.Page().LoadURL(readerURL)
			}
		})
		panelCancellation.register(frame, frame.Page(), w)
		w.registerCancellationNavigation(frame, frame.Page(), origin)
		if os.Getenv("TAILSPLIT_DESKTOP_PROOF_DIR") == "" {
			restoreNativeFrame(frame.NativeHandle())
		}
		configurePagePolicy(frame.Page(), w.port)
		frame.Page().OnDidFailLoad(func(*mygo.LoadError) {
			// LoadError.Error and Description may contain the capability URL.
			w.failLoad(w.load, frame.IsDestroyed(), errPanelLoad, frame.Destroy)
		})
		frame.Page().OnRenderProcessGone(func(string) {
			if !w.quitting.Load() && !w.isDone() && !frame.IsDestroyed() {
				if w.rendererUnavailable.Swap(true) {
					return
				}
				w.loaded = false
				w.stopLoadProbe()
				w.afterCancellationRecovery(func(_ *journalRecovery, _ bool) {
					if !w.quitting.Load() && !w.isDone() && !frame.IsDestroyed() {
						w.loadPageWithJournalReader(frame, origin, backup)
					}
				})
			}
		})
		frame.Page().OnDidFinishLoad(func() {
			if !w.preparingJournal && w.currentLoad(w.load, frame.IsDestroyed()) && tray.ConfigurationWindowAllows(w.port, frame.Page().URL()) {
				w.pageLoaded(frame)
			}
		})
		frame.OnClose(func(event *mygo.CloseEvent) {
			if !w.quitting.Load() && !w.isDone() {
				event.PreventDefault()
				w.visible.Store(false)
				w.hideWindow()
				w.beginCancellationRecovery()
			}
		})
		frame.OnClosed(func() {
			knownFailure := w.isDone()
			w.stopLoadProbe()
			w.rendererUnavailable.Store(true)
			w.complete(nil)
			if !knownFailure && !w.quitting.Load() && w.journal != nil {
				w.releaseJournalAfterRecovery(w.beginCancellationRecovery())
			}
		})
		if err := keeper.Page().LoadURL(readerURL); err != nil {
			w.failAfterCancellationRecovery(errPanelLoad, frame.Destroy)
			return
		}
		w.loadPageWithJournalReader(frame, origin, backup)
		// The tray proof owns focus while exercising transient dismissal. Keep
		// the full-window proof from racing its open/Escape/outside-click steps.
		if directory := os.Getenv("TAILSPLIT_DESKTOP_PROOF_DIR"); directory != "" && os.Getenv("TAILSPLIT_TRAY_PROOF_DIR") == "" {
			go func() {
				if err := verifyWindow(frame, true, directory); err != nil {
					fmt.Fprintln(os.Stderr, "TailSplit MyGo verification FAILED:", err)
				} else {
					fmt.Fprintln(os.Stderr, "TailSplit MyGo verification PASSED: fonts, content scrolling, window and titlebar interactions")
				}
			}()
		}
	})
}

func (w *Window) loadPage(frame *mygo.Window, origin, sourceURL string) {
	w.stopLoadProbe()
	w.loaded = false
	load := newPageLoad()
	w.load = load
	setStartupStage(frame.NativeHandle(), 1, false)
	if err := frame.Page().LoadURL(sourceURL); err != nil {
		w.failLoad(load, frame.IsDestroyed(), errPanelLoad, frame.Destroy)
		return
	}
	go func() {
		err := probePageReadiness(load.ctx, frame.Page().EvalContext, origin, pageProbeInterval)
		if errors.Is(err, context.Canceled) {
			return
		}
		mygo.RunOnMain(func() {
			if !w.currentLoad(load, frame.IsDestroyed()) {
				return
			}
			if err != nil {
				w.failLoad(load, false, err, frame.Destroy)
				return
			}
			w.pageLoaded(frame)
		})
	}()
}

func (w *Window) pageLoaded(frame *mygo.Window) {
	w.stopLoadProbe()
	w.loaded = true
	w.rendererUnavailable.Store(false)
	setStartupStage(frame.NativeHandle(), 4, true)
	if w.visible.Load() {
		w.showWindow()
	}
}

func (w *Window) showWindow() {
	if w.native == nil || w.native.IsDestroyed() {
		return
	}
	if runtime.GOOS == "darwin" {
		mygo.App.Dock.Show()
	}
	w.offerConnectRequest()
	w.everShown.Store(true)
	w.native.Show()
	w.native.Restore()
	w.native.Focus()
	// Show can be requested while already visible, without a visibility/focus
	// event. Wake the preserved page so native Connect is handled immediately.
	page := w.native.Page()
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_, _ = page.EvalContext(ctx, `window.dispatchEvent(new Event('tailsplit-window-shown'))`)
	}()
}

func (w *Window) hideWindow() {
	if w.native != nil && !w.native.IsDestroyed() {
		w.native.Hide()
	}
	if runtime.GOOS == "darwin" {
		mygo.App.Dock.Hide()
	}
}

// WantsVisible reports the current visibility intent even when loading failed
// before a native window could be shown. Hosts use it for asynchronous fallback.
func (w *Window) WantsVisible() bool { return w.visible.Load() && !w.quitting.Load() }

func (w *Window) Show() error {
	if w.isDone() || w.quitting.Load() {
		if err := w.result(); err != nil {
			return err
		}
		return ErrNotReady
	}
	// An already-interactive page can act before its asynchronous Show wakeup.
	// Reserve its request in the host before exposing another click to that page.
	w.offerConnectRequestIfShown()
	w.visible.Store(true)
	w.schedule()
	if !mygo.App.IsReady() {
		return nil
	}
	mygo.RunOnMain(func() {
		if w.quitting.Load() || w.isDone() {
			return
		}
		w.createWindow()
		if w.native != nil && w.loaded {
			w.showWindow()
		}
	})
	return w.result()
}

func (w *Window) Hide() error {
	w.visible.Store(false)
	if mygo.App.IsReady() {
		mygo.RunOnMain(w.hideWindow)
	}
	return nil
}

func (w *Window) Quit() error {
	w.quitting.Store(true)
	w.stopCancellationRecovery()
	mygo.App.Quit()
	return nil
}
