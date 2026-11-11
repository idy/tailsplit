package desktopwindow

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/egoist/mygo"
	"github.com/idy/tailsplit/internal/tray"
)

// Verify exercises an opt-in native popup proof from a worker goroutine. It only
// opens/captures/dismisses the popup; it never clicks application action buttons
// or changes networking. The caller enables it with TAILSPLIT_TRAY_PROOF_DIR.
func (p *TrayPopup) Verify(anchor *mygo.Tray, directory string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 35*time.Second)
	defer cancel()
	defer mygo.RunOnMain(p.Close)
	// Unlike a physical tray click, this worker can run before onReady installs
	// the icon and AppKit lays out its status item. Wait for the actual anchor.
	if err := waitWindowState(ctx, func() bool {
		bounds := anchor.Bounds()
		return bounds.Width > 0 && bounds.Height > 0
	}); err != nil {
		return p.popupProofError("tray anchor was not placed", nil, nil, anchor, directory)
	}
	// The external full-screen fixture starts this process first, then enters
	// its own Space. This separates application startup activation from opening
	// the popup, matching an already-running menu-bar app.
	fullscreenProof := os.Getenv("TAILSPLIT_TRAY_PROOF_FULLSCREEN") == "1"
	if fullscreenProof {
		if err := os.WriteFile(filepath.Join(directory, "tray-ready"), nil, 0o600); err != nil {
			return err
		}
		if err := waitPopupProofFile(ctx, directory, "fullscreen-ready"); err != nil {
			return err
		}
	}
	open := func() (*mygo.Window, error) {
		var a *popupAttempt
		var frame *mygo.Window
		mygo.RunOnMain(func() {
			p.Toggle(anchor, nil)
			a = p.attempt
			if a != nil {
				frame = a.native
			}
		})
		visible := false
		// Outlive both host readiness (5s) and native visibility (1s) checks,
		// rather than canceling the host at its own deadline and hiding why.
		err := waitPopupProofState(ctx, trayPopupLoadTimeout+2*time.Second, func() (done bool) {
			mygo.RunOnMain(func() {
				visible = a != nil && a.loaded && frame != nil && frame.IsVisible()
				done = visible || a == nil || a.closed || frame == nil || frame.IsDestroyed()
			})
			return done
		})
		if err != nil || !visible {
			return nil, p.popupProofError("tray popup did not become visible", a, frame, anchor, directory)
		}
		return frame, nil
	}
	frame, err := open()
	if err != nil {
		return err
	}
	var nativeState map[string]bool
	if err := waitPopupProofState(ctx, 2*time.Second, func() bool {
		mygo.RunOnMain(func() { nativeState = nativePopupProofState(frame.NativeHandle()) })
		return nativePopupProofReady(nativeState)
	}); err != nil {
		return fmt.Errorf("tray popup native presentation failed: %v", nativeState)
	}
	value, err := frame.Page().EvalContext(ctx, `(async () => {
		await document.fonts.ready;
		const root = document.documentElement;
		if (root.dataset.trayReady !== 'true' || innerWidth !== 360 || innerHeight < 240 || innerHeight > 420)
			throw new Error('popup readiness or bounded size mismatch');
		return {width: innerWidth, height: innerHeight, ready: true, title: document.title};
	})()`)
	if err != nil {
		return errors.New("tray popup content verification failed")
	}
	picture, err := frame.CapturePage()
	if err != nil {
		return errors.New("could not capture native tray popup")
	}
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(directory, "tray-native.png"), picture, 0o600); err != nil {
		return err
	}
	if fullscreenProof {
		state, err := json.Marshal(nativeState)
		if err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(directory, "tray-presented.json"), state, 0o600); err != nil {
			return err
		}
		// Keep this first popup open until the other process confirms it still
		// owns the foreground full-screen Space. Later outside clicks may
		// legitimately activate their target.
		if err := waitPopupProofFile(ctx, directory, "fullscreen-checked"); err != nil {
			return err
		}
	}
	// The same Toggle path as a second tray click must destroy the native frame,
	// while the application's native event loop stays alive for the next open.
	mygo.RunOnMain(func() { p.Toggle(anchor, nil) })
	if err := waitWindowState(ctx, frame.IsDestroyed); err != nil {
		return errors.New("second tray click did not dismiss the popup")
	}
	frame, err = open()
	if err != nil {
		return err
	}
	// Exercise the WebView's actual Escape-to-native bridge. This is synthetic
	// keyboard input; physical keyboard/tray hit testing remains manual.
	_, _ = frame.Page().EvalContext(ctx, `document.dispatchEvent(new KeyboardEvent('keydown', {key: 'Escape', code: 'Escape', bubbles: true, cancelable: true}))`)
	if err := waitWindowState(ctx, frame.IsDestroyed); err != nil {
		return errors.New("Escape did not dismiss the popup")
	}
	// On macOS, post an AppKit click to an inert window of this process. The
	// native window handles the outside click, without global input
	// injection or clicking any desktop/application controls.
	var target uintptr
	mygo.RunOnMain(func() { target = newPopupProofTarget() })
	outsideClickTested := target != 0
	if outsideClickTested {
		defer mygo.RunOnMain(func() { closePopupProofTarget(target) })
		frame, err = open()
		if err != nil {
			return err
		}
		mygo.RunOnMain(func() { clickPopupProofTarget(target) })
		if err := waitWindowState(ctx, frame.IsDestroyed); err != nil {
			return errors.New("outside click did not dismiss the popup")
		}
	}
	report, err := json.MarshalIndent(map[string]any{
		"webview": value, "native": nativeState, "second_click_dismissal": true, "escape_bridge_dismissal": true,
		"last_popup_close_keeps_app_alive": true, "outside_click_tested": outsideClickTested,
		"physical_tray_click_tested": false, "network_actions_tested": false,
	}, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(directory, "tray-verification.json"), append(report, '\n'), 0o600)
}

func waitPopupProofFile(ctx context.Context, directory, name string) error {
	return waitPopupProofState(ctx, 10*time.Second, func() bool {
		_, err := os.Stat(filepath.Join(directory, name))
		return err == nil
	})
}

func nativePopupProofReady(state map[string]bool) bool {
	if state == nil { // Native presentation introspection is macOS-only.
		return true
	}
	for _, name := range []string{"nonactivating_panel", "joins_all_spaces", "fullscreen_auxiliary", "on_active_space", "visible_on_screen", "key_window"} {
		if !state[name] {
			return false
		}
	}
	return true
}

func waitPopupProofState(ctx context.Context, timeout time.Duration, matches func() bool) error {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()
	for !matches() {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
	return nil
}

func (p *TrayPopup) popupProofError(message string, a *popupAttempt, frame *mygo.Window, anchor *mygo.Tray, directory string) error {
	state := map[string]any{}
	mygo.RunOnMain(func() {
		state["anchor"] = anchor.Bounds()
		state["attempt_present"] = a != nil
		state["native_present"] = frame != nil
		if a != nil {
			state["current"], state["pending"] = p.current(a), p.pending(a)
			state["loaded"], state["closed"] = a.loaded, a.closed
			state["document_finished"], state["failure"] = a.finished, a.failure
			state["evaluation_pending"], state["page_observation"] = a.probing.Load(), a.observed.Load()
		}
		if frame != nil {
			state["destroyed"], state["visible"] = frame.IsDestroyed(), frame.IsVisible()
			if !frame.IsDestroyed() {
				state["bounds"] = frame.Bounds()
				state["page_origin_allowed"] = tray.ConfigurationWindowAllows(p.port, frame.Page().URL())
			}
		}
	})
	report, _ := json.Marshal(state)
	if os.MkdirAll(directory, 0o700) == nil {
		_ = os.WriteFile(filepath.Join(directory, "tray-diagnostics.json"), append(report, '\n'), 0o600)
	}
	return fmt.Errorf("%s: %s", message, report)
}
