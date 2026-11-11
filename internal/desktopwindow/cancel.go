package desktopwindow

import (
	"context"
	"errors"
	"sync"

	"github.com/egoist/mygo"
)

// Native cancellation must reach the host independently of the panel's HTTP
// channel and ephemeral storage. The host controller is shared by the WebView
// and its browser fallback.
var panelCancellation = cancellationBindings{windows: make(map[*mygo.Window]cancellationBinding)}
var bindPanelCancellation sync.Once

type cancellationBinding struct {
	page   *mygo.Page
	window *Window
}

type cancellationBindings struct {
	mu      sync.Mutex
	windows map[*mygo.Window]cancellationBinding
}

type panelCancelService struct{}

func (panelCancelService) Cancel(ctx context.Context, request, operation string) error {
	return panelCancellation.cancelFrom(ctx, mygo.CallerWindow(ctx), mygo.CallerPage(ctx), request, operation)
}

// RetainCancel acknowledges only after the host owns the explicit intent. The
// panel must obtain this receipt before accepting cancellation locally; neither
// renderer storage nor a later navigation is responsible for keeping it alive.
func (panelCancelService) RetainCancel(ctx context.Context, scope, request, operation string) (journalCancellation, error) {
	return panelCancellation.retainFrom(mygo.CallerWindow(ctx), mygo.CallerPage(ctx), scope, request, operation)
}

func (b *cancellationBindings) retainFrom(frame *mygo.Window, page *mygo.Page, scope, request, operation string) (journalCancellation, error) {
	if scope == "" || request == "" && operation == "" || len(request)+len(operation) > 1024 {
		return journalCancellation{}, errors.New("无法读取操作请求。")
	}
	b.mu.Lock()
	binding, known := b.windows[frame]
	b.mu.Unlock()
	if !known || page == nil || binding.page != page || binding.window == nil || binding.window.journalScope != scope {
		return journalCancellation{}, errors.New("无法读取操作请求。")
	}
	record := journalCancellation{Scope: scope, Request: request, OperationID: operation}
	if !binding.window.nativeCancellationInbox().enqueue(record) {
		return journalCancellation{}, ErrNotReady
	}
	return record, nil
}

// SetCancelHandler installs the same guarded cancellation used by the local API.
// Only this window's main page can invoke it; MyGo additionally enforces the
// exact TrustedOrigins configured before that page loads.
func (w *Window) SetCancelHandler(handler func(context.Context, string, string) error) {
	w.mu.Lock()
	w.cancelHandler = handler
	inbox := w.navigationInbox
	w.mu.Unlock()
	if inbox != nil {
		inbox.retry()
	}
	bindPanelCancellation.Do(func() { mygo.BindAs("TailSplitPanel", panelCancelService{}) })
}

func (b *cancellationBindings) register(frame *mygo.Window, page *mygo.Page, window *Window) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.windows[frame] = cancellationBinding{page: page, window: window}
	// Keep the binding for already-admitted IPC after renderer teardown. A
	// closing page must not discard a cancellation delivered before fallback.
}

func (b *cancellationBindings) cancelFrom(ctx context.Context, frame *mygo.Window, page *mygo.Page, request, operation string) error {
	if request == "" && operation == "" || len(request)+len(operation) > 1024 {
		return errors.New("无法读取操作请求。")
	}
	b.mu.Lock()
	binding, known := b.windows[frame]
	b.mu.Unlock()
	if !known || page == nil || binding.page != page || binding.window == nil {
		return errors.New("无法读取操作请求。")
	}
	binding.window.mu.Lock()
	handler := binding.window.cancelHandler
	binding.window.mu.Unlock()
	if handler == nil {
		return ErrNotReady
	}
	// MyGo cancels page contexts on navigation/close. Recording the revocation
	// must survive that event, and the guarded host cleanup has its own bound.
	return handler(context.WithoutCancel(ctx), request, operation)
}
