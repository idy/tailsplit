package desktopwindow

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"io"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/egoist/mygo"
)

const journalReaderPath = "/native-recovery.html"
const journalReaderTimeout = 2 * time.Second

func journalReaderAllows(origin, raw string) bool {
	parsed, err := url.Parse(raw)
	return err == nil && sameCancellationOrigin(parsed, origin) && parsed.Path == journalReaderPath && parsed.EscapedPath() == journalReaderPath && parsed.RawQuery == "" && !parsed.ForceQuery && parsed.Fragment == "" && !strings.Contains(raw, "#")
}

var errJournalReaderNotReady = errors.New("native recovery store is not ready")
var errJournalEntryChanged = errors.New("native recovery entry changed before acknowledgment")

type journalStorageEntry struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

// This reader runs only host-supplied Eval on the separate, untrusted keeper.
// Its document has no app scripts, capability or Go-call bridge.
type journalStorage struct {
	ready     func(context.Context) error
	negotiate func(context.Context) (bool, error)
	list      func(context.Context) ([]journalStorageEntry, error)
	remove    func(context.Context, journalStorageEntry) error
}

type journalStorageResult struct {
	Ready   bool                  `json:"ready"`
	Entries []journalStorageEntry `json:"entries"`
	Removed bool                  `json:"removed"`
	Changed bool                  `json:"changed"`
	Usable  *bool                 `json:"usable"`
}

func newJournalStorageReader(scope, origin string, eval func(context.Context, string) (any, error)) *journalStorage {
	prefix := cancelJournalPrefix + scope + "-"
	probeName, probeValue := prefix+"probe-"+rand.Text(), rand.Text()
	guard := `location.origin === ` + strconv.Quote(normalizePageOrigin(origin)) + ` && location.pathname === ` + strconv.Quote(journalReaderPath) +
		` && document.readyState === 'complete' && document.querySelector('meta[name="tailsplit-native-recovery"]')?.content === '1'`
	call := func(ctx context.Context, body string) (journalStorageResult, error) {
		ctx, stop := context.WithTimeout(ctx, journalReaderTimeout)
		defer stop()
		value, err := eval(ctx, `(() => { const entries = []; try { if (!(`+guard+`)) return JSON.stringify({ready:false,entries}); `+body+
			` } catch (_) { return JSON.stringify({ready:false,entries}); } })()`)
		if err != nil {
			return journalStorageResult{}, err
		}
		text, ok := value.(string)
		if !ok {
			return journalStorageResult{}, errJournalReaderNotReady
		}
		decoder := json.NewDecoder(strings.NewReader(text))
		decoder.DisallowUnknownFields()
		var result journalStorageResult
		var extra any
		if decoder.Decode(&result) != nil || decoder.Decode(&extra) != io.EOF || result.Entries == nil {
			return journalStorageResult{}, errJournalReaderNotReady
		}
		if !result.Ready {
			return result, errJournalReaderNotReady
		}
		return result, nil
	}
	return &journalStorage{
		ready: func(ctx context.Context) error {
			_, err := call(ctx, `return JSON.stringify({ready:true,entries});`)
			return err
		},
		negotiate: func(ctx context.Context) (bool, error) {
			if scope == "" {
				return false, errJournalReaderNotReady
			}
			result, err := call(ctx, `const name = `+strconv.Quote(probeName)+`, value = `+strconv.Quote(probeValue)+`;
				try {
					const storage = localStorage;
					storage.setItem(name, value);
					const usable = storage.getItem(name) === value;
					if (usable) storage.removeItem(name);
					return JSON.stringify({ready:true,entries,usable});
				} catch (error) {
					if (['SecurityError','QuotaExceededError','NS_ERROR_DOM_QUOTA_REACHED'].includes(error?.name))
						return JSON.stringify({ready:true,entries,usable:false});
					throw error;
				}`)
			if err != nil {
				return false, err
			}
			if result.Usable == nil {
				return false, errJournalReaderNotReady
			}
			return *result.Usable, nil
		},
		list: func(ctx context.Context) ([]journalStorageEntry, error) {
			result, err := call(ctx, `const prefix = `+strconv.Quote(prefix)+`;
				for (let i = 0; i < localStorage.length; i++) {
					const name = localStorage.key(i);
					if (typeof name !== 'string' || !name.startsWith(prefix)) continue;
					const value = localStorage.getItem(name);
					if (typeof value === 'string') entries.push({name,value});
				}
				return JSON.stringify({ready:true,entries});`)
			return result.Entries, err
		},
		remove: func(ctx context.Context, entry journalStorageEntry) error {
			if scope == "" || !strings.HasPrefix(entry.Name, prefix) {
				return errJournalReaderNotReady
			}
			result, err := call(ctx, `const name = `+strconv.Quote(entry.Name)+`, expected = `+strconv.Quote(entry.Value)+`;
				const current = localStorage.getItem(name), removed = current === expected;
				const changed = current !== null && current !== expected;
				if (removed) localStorage.removeItem(name);
				return JSON.stringify({ready:true,entries,removed,changed});`)
			if err == nil && result.Changed {
				return errJournalEntryChanged
			}
			return err
		},
	}
}

const (
	journalBackupUnknown uint32 = iota
	journalBackupDisabled
	journalBackupEnabled
)

// The recovery view is immutable; its mode may transition only once, before
// source admission. A later read failure can never disable an enabled store.
type negotiatedJournalBackup struct {
	storage *journalStorage
	mu      sync.Mutex
	mode    atomic.Uint32
}

func (b *negotiatedJournalBackup) prepare(ctx context.Context) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	switch b.mode.Load() {
	case journalBackupDisabled:
		return false, nil
	case journalBackupEnabled:
		if b.storage.ready == nil {
			return true, errJournalReaderNotReady
		}
		return true, b.storage.ready(ctx)
	}
	if b.storage.negotiate == nil {
		return false, errJournalReaderNotReady
	}
	usable, err := b.storage.negotiate(ctx)
	if err != nil {
		return false, err
	}
	if err := ctx.Err(); err != nil {
		return false, err
	}
	if usable {
		b.mode.Store(journalBackupEnabled)
	} else {
		b.mode.Store(journalBackupDisabled)
	}
	return usable, nil
}

func (b *negotiatedJournalBackup) recoveryStore() *journalStorage {
	return &journalStorage{
		list: func(ctx context.Context) ([]journalStorageEntry, error) {
			// Unknown is possible only before any source load. There cannot be
			// source-written records to recover at that initial failure boundary.
			if b.mode.Load() != journalBackupEnabled {
				return nil, nil
			}
			return b.storage.list(ctx)
		},
		remove: func(ctx context.Context, entry journalStorageEntry) error {
			if b.mode.Load() != journalBackupEnabled {
				return nil
			}
			return b.storage.remove(ctx, entry)
		},
	}
}

func journalSourceURL(raw string, enabled bool) (string, error) {
	parsed, err := url.Parse(raw)
	if err != nil {
		return "", errJournalReaderNotReady
	}
	fragment, err := url.ParseQuery(parsed.Fragment)
	if err != nil {
		return "", errJournalReaderNotReady
	}
	mode := "0"
	if enabled {
		mode = "1"
	}
	fragment.Set("journal_backup", mode)
	fragment.Set("cancel_host", "1") // Main navigation inbox is installed before this URL is loaded.
	fragment.Del("cancel_guard")     // Retired source-loss guards must not survive an old fragment.
	parsed.Fragment, parsed.RawFragment = fragment.Encode(), ""
	return parsed.String(), nil
}

func prepareJournalSourceURL(ctx context.Context, raw string, backup *negotiatedJournalBackup) (string, error) {
	ticker := time.NewTicker(pageProbeInterval)
	defer ticker.Stop()
	for {
		enabled, err := backup.prepare(ctx)
		if err == nil {
			return journalSourceURL(raw, enabled)
		}
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-ticker.C:
		}
	}
}

// Gate every source load on the keeper's same-origin reader, using the ordinary
// page-load token to discard completions from superseded or canceled attempts.
func (w *Window) loadPageWithJournalReader(frame *mygo.Window, origin string, backup *negotiatedJournalBackup) {
	w.stopLoadProbe()
	w.loaded = false
	w.preparingJournal = true
	load := newPageLoad()
	w.load = load
	rawURL := w.url // Immutable; derive the negotiated load URL without changing it.
	go func() {
		sourceURL, err := prepareJournalSourceURL(load.ctx, rawURL, backup)
		if errors.Is(err, context.Canceled) {
			return
		}
		mygo.RunOnMain(func() {
			if !w.currentLoad(load, frame.IsDestroyed()) {
				return
			}
			if err != nil {
				w.failLoad(load, false, errWebViewReadiness, frame.Destroy)
				return
			}
			w.preparingJournal = false
			w.loadPage(frame, origin, sourceURL)
		})
	}()
}
