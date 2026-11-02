//go:build windows

// Package winui hosts the Windows configuration panel in TailSplit's native DLL.
package winui

import (
	"errors"
	"fmt"
	"net/url"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"unsafe"

	"golang.org/x/sys/windows"
)

// ErrNotReady means Run has not created and loaded the window yet. Callers may
// retry Show while Run starts on its own goroutine.
var ErrNotReady = errors.New("TailSplit window is not ready")

// Window owns a reference to the architecture-matched TailSplit window DLL.
// A Window must not be copied, and Run may be called only once.
type Window struct {
	mu        sync.Mutex
	library   *windows.DLL
	runProc   *windows.Proc
	findProc  *windows.Proc
	showProc  *windows.Proc
	hideProc  *windows.Proc
	quitProc  *windows.Proc
	url       []uint16
	userData  []uint16
	className []uint16
	started   bool
}

// New loads TailSplit's own window host from an absolute path. userData must be
// an absolute, private directory supplied by the app. No WebView2 runtime is
// downloaded; a missing or outdated installed runtime is reported by Run.
func New(libraryPath, rawURL, userData, className string) (*Window, error) {
	if runtime.GOARCH != "amd64" && runtime.GOARCH != "arm64" {
		return nil, errors.New("TailSplit's window host requires Windows x64 or ARM64")
	}
	if !filepath.IsAbs(libraryPath) {
		return nil, errors.New("window host DLL path must be absolute")
	}
	if !filepath.IsAbs(userData) {
		return nil, errors.New("WebView2 user data directory must be absolute")
	}
	if err := validateWindowURL(rawURL); err != nil {
		return nil, err
	}

	w := &Window{}
	var err error
	if w.url, err = windows.UTF16FromString(rawURL); err != nil {
		return nil, errors.New("window URL contains an invalid null character")
	}
	if w.userData, err = windows.UTF16FromString(filepath.Clean(userData)); err != nil {
		return nil, fmt.Errorf("WebView2 user data directory: %w", err)
	}
	if w.className, err = windows.UTF16FromString(className); err != nil {
		return nil, fmt.Errorf("window class name: %w", err)
	}
	if len(w.className) < 2 || len(w.className) > 256 {
		return nil, errors.New("window class name must contain 1 to 255 UTF-16 code units")
	}

	libraryPath = filepath.Clean(libraryPath)
	handle, err := windows.LoadLibraryEx(libraryPath, 0,
		windows.LOAD_LIBRARY_SEARCH_DLL_LOAD_DIR|windows.LOAD_LIBRARY_SEARCH_SYSTEM32)
	if err != nil {
		return nil, fmt.Errorf("load TailSplit window host for %s: %w", runtime.GOARCH, err)
	}
	w.library = &windows.DLL{Name: libraryPath, Handle: handle}
	for _, symbol := range []struct {
		name string
		proc **windows.Proc
	}{
		{"TailSplitWindowRun", &w.runProc},
		{"TailSplitWindowFind", &w.findProc},
		{"TailSplitWindowShow", &w.showProc},
		{"TailSplitWindowHide", &w.hideProc},
		{"TailSplitWindowQuit", &w.quitProc},
	} {
		proc, err := w.library.FindProc(symbol.name)
		if err != nil {
			_ = w.library.Release()
			return nil, fmt.Errorf("resolve window host export %s: %w", symbol.name, err)
		}
		*symbol.proc = proc
	}
	// New need not be followed by Run. A running Window is explicitly kept alive
	// through the native call and its shutdown, so this only frees an unused DLL.
	runtime.SetFinalizer(w, (*Window).finalize)
	return w, nil
}

func validateWindowURL(raw string) error {
	invalid := errors.New("window URL must use http://127.0.0.1:<port> without credentials or a nonempty query")
	parsed, err := url.Parse(raw)
	// Do not wrap url.Parse's error: it may contain the capability fragment.
	if err != nil || !strings.HasPrefix(raw, "http://127.0.0.1:") ||
		parsed.Scheme != "http" || parsed.Opaque != "" || parsed.User != nil ||
		parsed.RawQuery != "" || strings.Contains(raw, `\`) {
		return invalid
	}
	port := parsed.Port()
	if port == "" || parsed.Host != "127.0.0.1:"+port {
		return invalid
	}
	for _, digit := range port {
		if digit < '0' || digit > '9' {
			return invalid
		}
	}
	number, err := strconv.ParseUint(port, 10, 16)
	if err != nil || number == 0 {
		return invalid
	}
	return nil
}

// Run blocks on a dedicated, locked OS thread until Quit or an initialization
// failure. The DLL owns this thread's STA and message loop. Start Run in its own
// goroutine; Show and Quit may be called concurrently from other goroutines.
func (w *Window) Run() error {
	w.mu.Lock()
	if w.started {
		w.mu.Unlock()
		return errors.New("TailSplit window Run may only be called once")
	}
	if w.library == nil {
		w.mu.Unlock()
		return errors.New("TailSplit window must be initialized with New")
	}
	w.started = true
	w.mu.Unlock()

	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	status, _, _ := w.runProc.Call(
		uintptr(unsafe.Pointer(&w.url[0])),
		uintptr(unsafe.Pointer(&w.userData[0])),
		uintptr(unsafe.Pointer(&w.className[0])),
	)
	// The native Run has drained startup callbacks, detached event handlers and
	// closed the controller before returning. Serialize unloading with controls.
	w.mu.Lock()
	releaseErr := w.releaseLibrary()
	w.mu.Unlock()
	runtime.SetFinalizer(w, nil)
	runtime.KeepAlive(w)

	if code := uint32(status); int32(code) < 0 {
		return windowRunError(code)
	}
	if releaseErr != nil {
		return fmt.Errorf("release TailSplit window host: %w", releaseErr)
	}
	return nil
}

// Show restores and activates the app's loaded window. Until initialization has
// completed it returns ErrNotReady. Closing the title-bar window only hides it.
func (w *Window) Show() error {
	return w.control("show", w.showProc)
}

// Hide posts the same close-to-hide request as the title-bar close button. It
// also works after the HWND is created but before WebView2 has finished loading.
func (w *Window) Hide() error {
	return w.control("hide", w.hideProc)
}

// Quit requests destruction of the app's window and termination of Run. It also
// works during WebView2 initialization; wait for Run to return before exiting.
func (w *Window) Quit() error {
	return w.control("quit", w.quitProc)
}

func (w *Window) control(operation string, proc *windows.Proc) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.library == nil {
		return errors.New("TailSplit window has stopped")
	}
	if !w.started {
		return ErrNotReady
	}
	hwnd, _, _ := w.findProc.Call(uintptr(unsafe.Pointer(&w.className[0])))
	if hwnd == 0 {
		return ErrNotReady
	}
	var pid uint32
	if _, err := windows.GetWindowThreadProcessId(windows.HWND(hwnd), &pid); err != nil {
		return fmt.Errorf("identify TailSplit window: %w", err)
	}
	if pid != windows.GetCurrentProcessId() {
		return errors.New("refusing to control a TailSplit window owned by another process")
	}
	result, _, callErr := proc.Call(hwnd)
	runtime.KeepAlive(w)
	if result == 0 {
		if errors.Is(callErr, windows.ERROR_NOT_READY) {
			return ErrNotReady
		}
		if callErr == nil || errors.Is(callErr, windows.ERROR_SUCCESS) {
			callErr = windows.ERROR_GEN_FAILURE
		}
		return fmt.Errorf("%s TailSplit window: %w", operation, callErr)
	}
	return nil
}

// releaseLibrary is called with mu held and after all native calls finish.
func (w *Window) releaseLibrary() error {
	if w.library == nil {
		return nil
	}
	err := w.library.Release()
	w.library = nil
	return err
}

func (w *Window) finalize() {
	w.mu.Lock()
	defer w.mu.Unlock()
	if !w.started {
		_ = w.releaseLibrary()
	}
}

func windowRunError(code uint32) error {
	switch code {
	case 0x80070002, 0x80070003, 0x8007007e, 0x80040154:
		return fmt.Errorf("Microsoft Edge WebView2 Runtime was not found; install it with the TailSplit installer and try again (HRESULT 0x%08X)", code)
	case 0x80004002:
		return fmt.Errorf("Microsoft Edge WebView2 Runtime is too old for TailSplit's window security settings; update it and try again (HRESULT 0x%08X)", code)
	default:
		if code&0xffff0000 == 0x80070000 {
			return fmt.Errorf("run TailSplit window (HRESULT 0x%08X): %w", code, windows.Errno(code&0xffff))
		}
		return fmt.Errorf("initialize or load the TailSplit window failed (HRESULT 0x%08X)", code)
	}
}
