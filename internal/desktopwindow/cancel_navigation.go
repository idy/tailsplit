package desktopwindow

import (
	"context"
	"encoding/json"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/egoist/mygo"
)

const nativeCancelPath = "/native-cancel"
const nativeCancelAttemptTimeout = 15 * time.Second
const nativeCancelAckTimeout = 2 * time.Second

type nativeCancelHandler func(context.Context, string, string) error

// This queue belongs to the host, not to any renderer or website data store.
// Only its delivery worker calls the guarded handler; journal reads never gate it.
type nativeCancellationInbox struct {
	ctx          context.Context
	handler      func() nativeCancelHandler
	mu           sync.Mutex
	pending      map[journalCancellation]bool
	acknowledged map[journalCancellation]bool
	job          *journalRecovery
	running      bool
	wake         chan struct{}
	ack          func(journalCancellation) // Must not wait for renderer Eval.
}

func newNativeCancellationInbox(ctx context.Context, handler func() nativeCancelHandler) *nativeCancellationInbox {
	return &nativeCancellationInbox{ctx: ctx, handler: handler, pending: make(map[journalCancellation]bool), acknowledged: make(map[journalCancellation]bool), wake: make(chan struct{}, 1)}
}

func (i *nativeCancellationInbox) retry() {
	select {
	case i.wake <- struct{}{}:
	default:
	}
}

func (i *nativeCancellationInbox) enqueue(record journalCancellation) bool {
	i.mu.Lock()
	if i.ctx.Err() != nil {
		i.mu.Unlock()
		return false
	}
	if i.acknowledged[record] {
		ack := i.ack
		i.mu.Unlock()
		if ack != nil {
			ack(record)
		}
		return true
	}
	i.pending[record] = true
	if i.job == nil {
		i.job = &journalRecovery{done: make(chan struct{})}
	}
	if !i.running {
		i.running = true
		go i.deliver()
	} else {
		i.retry()
	}
	i.mu.Unlock()
	return true
}

func (i *nativeCancellationInbox) pendingRecovery() *journalRecovery {
	i.mu.Lock()
	defer i.mu.Unlock()
	return i.job
}

func (i *nativeCancellationInbox) finishLocked(err error) {
	if i.job != nil {
		i.job.err = err
		close(i.job.done)
		i.job = nil
	}
	i.running = false
}

func (i *nativeCancellationInbox) deliver() {
	delay := cancelJournalRetry
	for {
		i.mu.Lock()
		if err := i.ctx.Err(); err != nil {
			i.finishLocked(err)
			i.mu.Unlock()
			return
		}
		if len(i.pending) == 0 {
			i.finishLocked(nil)
			i.mu.Unlock()
			return
		}
		var targets []journalCancellation
		for target := range i.pending {
			targets = append(targets, target)
		}
		i.mu.Unlock()
		sort.Slice(targets, func(a, b int) bool {
			if targets[a].Request != targets[b].Request {
				return targets[a].Request < targets[b].Request
			}
			return targets[a].OperationID < targets[b].OperationID
		})
		for _, target := range targets {
			if i.ctx.Err() != nil {
				break
			}
			handler := i.handler()
			if handler == nil {
				continue
			}
			ctx, stop := context.WithTimeout(i.ctx, nativeCancelAttemptTimeout)
			err := handler(ctx, target.Request, target.OperationID)
			stop()
			if err != nil {
				continue
			}
			i.mu.Lock()
			delete(i.pending, target)
			i.acknowledged[target] = true
			ack := i.ack
			i.mu.Unlock()
			if ack != nil {
				ack(target)
			}
		}
		i.mu.Lock()
		if len(i.pending) == 0 {
			i.finishLocked(nil)
			i.mu.Unlock()
			return
		}
		i.mu.Unlock()
		timer := time.NewTimer(delay)
		select {
		case <-i.ctx.Done():
		case <-i.wake:
		case <-timer.C:
		}
		timer.Stop()
		if delay < 2*time.Second {
			delay *= 2
		}
	}
}

func sameCancellationOrigin(candidate *url.URL, origin string) bool {
	base, err := url.Parse(origin)
	if err != nil || candidate.User != nil || candidate.Opaque != "" || candidate.Scheme != base.Scheme {
		return false
	}
	if candidate.Host == base.Host {
		return true
	}
	// WebKit omits the default HTTP port in its navigation URLs.
	return base.Scheme == "http" && base.Port() == "80" && candidate.Host == base.Hostname()
}

func parseNativeCancellation(origin, scope, raw string) (journalCancellation, bool) {
	var empty journalCancellation
	if scope == "" || len(raw) > 8192 {
		return empty, false
	}
	parsed, err := url.Parse(raw)
	if err != nil || !sameCancellationOrigin(parsed, origin) || parsed.Path != nativeCancelPath || parsed.EscapedPath() != nativeCancelPath || parsed.RawQuery != "" || parsed.ForceQuery {
		return empty, false
	}
	// Parse the escaped fragment once. Parsing URL.Fragment would decode escaped
	// '&' or '=' twice and could change the exact target fields.
	fields, err := url.ParseQuery(parsed.EscapedFragment())
	if err != nil || len(fields) != 3 {
		return empty, false
	}
	for _, name := range []string{"scope", "request", "operation_id"} {
		if len(fields[name]) != 1 {
			return empty, false
		}
	}
	record := journalCancellation{Scope: fields.Get("scope"), Request: fields.Get("request"), OperationID: fields.Get("operation_id")}
	if record.Scope != scope || record.Request == "" && record.OperationID == "" || len(record.Request)+len(record.OperationID) > 1024 {
		return empty, false
	}
	return record, true
}

func nativeCancellationCandidate(raw string) bool {
	head, _, _ := strings.Cut(raw, "#")
	parsed, err := url.Parse(head)
	return err == nil && parsed.Path == nativeCancelPath
}

func (w *Window) nativeCancellationInbox() *nativeCancellationInbox {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.navigationInbox == nil {
		ctx := w.journalContext
		if ctx == nil {
			ctx = context.Background()
		}
		w.navigationInbox = newNativeCancellationInbox(ctx, func() nativeCancelHandler {
			w.mu.Lock()
			defer w.mu.Unlock()
			return w.cancelHandler
		})
	}
	return w.navigationInbox
}

func (w *Window) pendingNativeCancellation() *journalRecovery {
	w.mu.Lock()
	inbox := w.navigationInbox
	w.mu.Unlock()
	if inbox == nil {
		return nil
	}
	return inbox.pendingRecovery()
}

// SDK WillNavigate emits only requested top-level navigations (not subframe,
// reload or back/forward events). Register only the bound main Page, before the
// ordinary navigation policy. Keeper pages have neither this hook nor binding.
func (b *cancellationBindings) cancellationNavigation(w *Window, frame *mygo.Window, page *mygo.Page, origin, sourceURL string, destroyed bool, event *mygo.NavigateEvent) bool {
	if !nativeCancellationCandidate(event.URL) {
		return false
	}
	event.PreventDefault()
	b.mu.Lock()
	binding, known := b.windows[frame]
	b.mu.Unlock()
	if destroyed || !known || binding.window != w || page == nil || binding.page != page {
		return false
	}
	source, err := url.Parse(sourceURL)
	if err != nil || !sameCancellationOrigin(source, origin) || source.Path != "/" || source.RawQuery != "" {
		return false
	}
	record, valid := parseNativeCancellation(origin, w.journalScope, event.URL)
	return valid && w.nativeCancellationInbox().enqueue(record)
}

func (w *Window) registerCancellationNavigation(frame *mygo.Window, page *mygo.Page, origin string) {
	inbox := w.nativeCancellationInbox()
	inbox.mu.Lock()
	inbox.ack = func(record journalCancellation) {
		go func() {
			if frame.IsDestroyed() {
				return
			}
			ctx, stop := context.WithTimeout(inbox.ctx, nativeCancelAckTimeout)
			defer stop()
			data, _ := json.Marshal(record)
			_, _ = page.EvalContext(ctx, `(() => { if (location.origin === `+strconv.Quote(origin)+` && location.pathname === '/') return window.dispatchEvent(new CustomEvent('tailsplit-native-cancel-ack',{detail:`+string(data)+`})); return false; })()`)
		}()
	}
	inbox.mu.Unlock()
	page.OnWillNavigate(func(event *mygo.NavigateEvent) {
		panelCancellation.cancellationNavigation(w, frame, page, origin, page.URL(), frame.IsDestroyed(), event)
	})
}
