package desktopwindow

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/egoist/mygo"
)

const journalReaderPath = "/native-recovery.html"
const journalReaderTimeout = 2 * time.Second

var errJournalReaderNotReady = errors.New("native recovery store is not ready")
var errJournalEntryChanged = errors.New("native recovery entry changed before acknowledgment")

type journalStorageEntry struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

// This reader runs only host-supplied Eval on the separate, untrusted keeper.
// Its document has no app scripts, capability or Go-call bridge.
type journalStorage struct {
	ready  func(context.Context) error
	list   func(context.Context) ([]journalStorageEntry, error)
	remove func(context.Context, journalStorageEntry) error
}

type journalStorageResult struct {
	Ready   bool                  `json:"ready"`
	Entries []journalStorageEntry `json:"entries"`
	Removed bool                  `json:"removed"`
	Changed bool                  `json:"changed"`
}

func newJournalStorageReader(scope, origin string, eval func(context.Context, string) (any, error)) *journalStorage {
	prefix := cancelJournalPrefix + scope + "-"
	guard := `location.origin === ` + strconv.Quote(origin) + ` && location.pathname === ` + strconv.Quote(journalReaderPath) +
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

func waitJournalStorageReady(ctx context.Context, storage *journalStorage) error {
	ticker := time.NewTicker(pageProbeInterval)
	defer ticker.Stop()
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := storage.ready(ctx); err == nil {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

// Gate every source load on the keeper's same-origin reader, using the ordinary
// page-load token to discard completions from superseded or canceled attempts.
func (w *Window) loadPageWithJournalReader(frame *mygo.Window, origin string, storage *journalStorage) {
	w.stopLoadProbe()
	w.loaded = false
	w.preparingJournal = true
	load := newPageLoad()
	w.load = load
	go func() {
		err := waitJournalStorageReady(load.ctx, storage)
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
			w.loadPage(frame, origin)
		})
	}()
}
