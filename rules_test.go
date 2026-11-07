package tailsplit

import (
	"context"
	"encoding/base64"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/idy/tailsplit/internal/domainrules"
	"github.com/idy/tailsplit/internal/privatefs"
)

func TestRuleRefreshKeepsLastGoodCache(t *testing.T) {
	var answer atomic.Value
	answer.Store("[AutoProxy 0.2.9]\n||example.com\n@@||safe.example.com\n")
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(answer.Load().(string)))
	}))
	defer server.Close()
	config, err := loadTestConfig(t, "tailscale:\n  exit_node: 100.64.0.10\nrules:\n  gfwlist: "+server.URL+"\nstate_dir: ./state\n")
	if err != nil {
		t.Fatal(err)
	}
	info, err := RefreshRules(context.Background(), config, server.Client())
	if err != nil || info.Proxy != 1 || info.Direct != 1 {
		t.Fatalf("valid refresh failed: %+v, %v", info, err)
	}
	cache := RulesCachePath(config)
	before, err := os.ReadFile(cache)
	if err != nil {
		t.Fatal(err)
	}
	if err := privatefs.CheckFile(cache); err != nil {
		t.Fatal("rule cache is not owner-only:", err)
	}
	if _, err := Compile(config); err != nil {
		t.Fatal(err)
	}
	answer.Store("not an AutoProxy list")
	if _, err := RefreshRules(context.Background(), config, server.Client()); err == nil {
		t.Fatal("accepted invalid upstream rules")
	}
	after, _ := os.ReadFile(cache)
	if string(after) != string(before) {
		t.Fatal("failed refresh replaced the working rule cache")
	}
	answer.Store(base64.StdEncoding.EncodeToString([]byte("[AutoProxy 0.2.9]\n||next.example\n")))
	if _, err := RefreshRules(context.Background(), config, server.Client()); err != nil {
		t.Fatal(err)
	}
	answer.Store(strings.Repeat("x", (2<<20)+1))
	good, _ := os.ReadFile(cache)
	if _, err := RefreshRules(context.Background(), config, server.Client()); err == nil {
		t.Fatal("accepted oversized upstream")
	}
	after, _ = os.ReadFile(cache)
	if string(after) != string(good) {
		t.Fatal("oversized refresh replaced the working rule cache")
	}
}

type ruleTransportFunc func(*http.Request) (*http.Response, error)

func (f ruleTransportFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}

func TestRuleCacheDownloadsOnceAndWorksOffline(t *testing.T) {
	data := base64.StdEncoding.EncodeToString([]byte("[AutoProxy 0.2.9]\n||fixture.example\n@@||safe.fixture.example\n"))
	for _, source := range []string{"builtin", domainrules.DefaultRulesURL, "https://rules.example/subscription"} {
		t.Run(source, func(t *testing.T) {
			var requests atomic.Int32
			transport := http.DefaultTransport
			t.Cleanup(func() { http.DefaultTransport = transport })
			http.DefaultTransport = ruleTransportFunc(func(request *http.Request) (*http.Response, error) {
				if requests.Add(1) > 1 {
					return nil, errors.New("offline")
				}
				want := source
				if want == "builtin" {
					want = domainrules.DefaultRulesURL
				}
				if request.URL.String() != want {
					t.Errorf("download source = %q, want %q", request.URL, want)
				}
				return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(data)), Header: make(http.Header)}, nil
			})
			config, err := loadTestConfig(t, "tailscale:\n  exit_node: 100.64.0.10\nrules:\n  gfwlist: "+source+"\nstate_dir: ./state\n")
			if err != nil {
				t.Fatal(err)
			}
			compiled, err := CompileContext(context.Background(), config)
			if err != nil {
				t.Fatal("first compile did not download rules:", err)
			}
			cache := RulesCachePath(config)
			cached, err := os.ReadFile(cache)
			if err != nil || string(cached) != data {
				t.Fatalf("original downloaded bytes were not cached: %v", err)
			}
			if err := privatefs.CheckFile(cache); err != nil {
				t.Fatal("rule cache is not owner-only:", err)
			}
			if compiled.GFWSource != cache || compiled.GFWList == nil || compiled.GFWList.Proxy != 1 || compiled.GFWList.Direct != 1 || compiled.GFWList.SupplementalProxy == 0 {
				t.Fatalf("downloaded rules were not projected from disk: %+v", compiled)
			}
			again, err := Compile(config)
			if err != nil || string(again.JSON) != string(compiled.JSON) || requests.Load() != 1 {
				t.Fatalf("offline compile did not reuse the saved cache: requests=%d, error=%v", requests.Load(), err)
			}
			if err := os.Remove(cache); err != nil {
				t.Fatal(err)
			}
			if _, err := Compile(config); err == nil || !strings.Contains(err.Error(), "no cached GFWList") {
				t.Fatalf("offline startup without a cache did not fail: %v", err)
			}
		})
	}
}

func TestLegacyRulesCacheMigratesForSavedSourceOffline(t *testing.T) {
	for _, test := range []struct{ name, source string }{
		{"builtin", "builtin"},
		{"official", domainrules.DefaultRulesURL},
		{"custom", "https://rules.example/legacy?token=fake-secret"},
	} {
		t.Run(test.name, func(t *testing.T) {
			directory := t.TempDir()
			state := filepath.Join(directory, "state")
			if err := privateState(state); err != nil {
				t.Fatal(err)
			}
			legacy := filepath.Join(state, "gfwlist.txt")
			data := []byte(base64.StdEncoding.EncodeToString([]byte("[AutoProxy 0.2.9]\n||saved.fixture.example\n")))
			if err := os.WriteFile(legacy, data, 0o600); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(directory, "config.yaml")
			text := "state_dir: ./state\ntailscale:\n  exit_node: 100.64.0.10\nrules:\n  gfwlist: " + test.source + "\n"
			if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
				t.Fatal(err)
			}
			transport := http.DefaultTransport
			t.Cleanup(func() { http.DefaultTransport = transport })
			http.DefaultTransport = ruleTransportFunc(func(request *http.Request) (*http.Response, error) {
				t.Error("a saved legacy cache required network access")
				return nil, errors.New("offline")
			})
			config, err := Load(path)
			if err != nil {
				t.Fatal(err)
			}
			cache := RulesCachePath(config)
			after, err := os.ReadFile(cache)
			if err != nil || string(after) != string(data) {
				t.Fatal("legacy cache bytes were not preserved in the source-bound cache:", err)
			}
			if _, err := os.Stat(legacy); !os.IsNotExist(err) {
				t.Fatal("unbound legacy cache could be claimed by a later source:", err)
			}
			if strings.Contains(filepath.Base(cache), "fake-secret") {
				t.Fatal("cache filename exposes subscription credentials")
			}
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			compiled, err := CompileContext(ctx, config)
			if err != nil || !strings.Contains(string(compiled.JSON), `"saved.fixture.example"`) {
				t.Fatal("migrated cache could not be used offline:", err)
			}
		})
	}
}

func TestDraftCannotClaimSavedLegacyCache(t *testing.T) {
	for _, test := range []struct{ name, saved, draft string }{
		{"custom to official", "https://rules.example/a", domainrules.DefaultRulesURL},
		{"official to custom", "builtin", "https://rules.example/b"},
	} {
		t.Run(test.name, func(t *testing.T) {
			saved, err := loadTestConfig(t, "state_dir: ./state\ntailscale:\n  exit_node: 100.64.0.10\nrules:\n  gfwlist: "+test.saved+"\n")
			if err != nil {
				t.Fatal(err)
			}
			if err := privateState(saved.StateDir); err != nil {
				t.Fatal(err)
			}
			legacy := filepath.Join(saved.StateDir, "gfwlist.txt")
			data := "[AutoProxy 0.2.9]\n||saved.fixture.example\n"
			if err := os.WriteFile(legacy, []byte(data), 0o600); err != nil {
				t.Fatal(err)
			}
			var requests atomic.Int32
			transport := http.DefaultTransport
			t.Cleanup(func() { http.DefaultTransport = transport })
			http.DefaultTransport = ruleTransportFunc(func(request *http.Request) (*http.Response, error) {
				requests.Add(1)
				if request.URL.String() != rulesSubscriptionSource(test.draft) {
					t.Error("the saved source unexpectedly required a download")
				}
				return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader("[AutoProxy 0.2.9]\n||draft.fixture.example\n")), Header: make(http.Header)}, nil
			})
			draft := saved
			draft.Rules.GFWList = test.draft
			compiled, err := Compile(draft)
			if err != nil || requests.Load() != 1 || !strings.Contains(string(compiled.JSON), `"draft.fixture.example"`) {
				t.Fatal("draft adopted a legacy cache instead of its own source:", err)
			}
			beforeMigration, err := os.ReadFile(legacy)
			if err != nil || string(beforeMigration) != data {
				t.Fatal("draft changed the saved source's legacy cache:", err)
			}
			compiled, err = Compile(saved)
			if err != nil || requests.Load() != 1 || !strings.Contains(string(compiled.JSON), `"saved.fixture.example"`) || strings.Contains(string(compiled.JSON), `"draft.fixture.example"`) {
				t.Fatal("saved source could not migrate and retain its own policy offline:", err)
			}
		})
	}
}

func TestOlderRuleSourcesKeepApplicationCoverageOffline(t *testing.T) {
	legacy := "[AutoProxy 0.2.9]\n! Last Modified: Mon, 21 Sep 2026 12:27:46 +0000\n||fixture.example\n||openai.com\n||chatgpt.com\n||anthropic.com\n||claude.ai\n||claude.com\n@@||safe.claude.app\n"
	for _, source := range []string{"builtin", domainrules.DefaultRulesURL, "https://rules.example/subscription", "./older-rules.txt"} {
		t.Run(source, func(t *testing.T) {
			config, err := loadTestConfig(t, "tailscale:\n  exit_node: 100.64.0.10\nrules:\n  gfwlist: "+source+"\nstate_dir: ./state\n")
			if err != nil {
				t.Fatal(err)
			}
			path, stored := config.Rules.GFWList, legacy
			if source == "builtin" || strings.HasPrefix(source, "https://") {
				if err := privateState(config.StateDir); err != nil {
					t.Fatal(err)
				}
				path = RulesCachePath(config)
				stored = base64.StdEncoding.EncodeToString([]byte(legacy))
			}
			if err := os.WriteFile(path, []byte(stored), 0o600); err != nil {
				t.Fatal(err)
			}
			transport := http.DefaultTransport
			t.Cleanup(func() { http.DefaultTransport = transport })
			http.DefaultTransport = ruleTransportFunc(func(request *http.Request) (*http.Response, error) {
				t.Error("existing legacy rules required network access")
				return nil, errors.New("offline")
			})
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			compiled, err := CompileContext(ctx, config)
			if err != nil {
				t.Fatal("older selected rules could not compile offline:", err)
			}
			for _, domain := range []string{"claude.app", "claudeusercontent.com", "claudemcpcontent.com"} {
				if !strings.Contains(string(compiled.JSON), `"`+domain+`"`) {
					t.Fatal("older selected rules lost prior proxy coverage:", domain)
				}
			}
			after, err := os.ReadFile(path)
			if err != nil || string(after) != stored {
				t.Fatal("compilation changed the selected rules or disk cache:", err)
			}
		})
	}
}

func TestMissingRuleCacheRejectsFailedOrInvalidDownloads(t *testing.T) {
	for _, test := range []struct {
		name   string
		status int
		data   string
	}{
		{"HTTP failure", http.StatusBadGateway, "upstream unavailable"},
		{"invalid rules", http.StatusOK, "<html>not a subscription</html>"},
		{"oversized rules", http.StatusOK, strings.Repeat("x", (2<<20)+1)},
	} {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(test.status)
				_, _ = io.WriteString(w, test.data)
			}))
			defer server.Close()
			config := Config{StateDir: filepath.Join(t.TempDir(), "state"), Rules: RulesConfig{GFWList: server.URL}}
			if err := EnsureRules(context.Background(), config, server.Client()); err == nil {
				t.Fatal("failed download was accepted")
			}
			if _, err := os.Stat(config.StateDir); !os.IsNotExist(err) {
				t.Fatal("failed download created persistent state:", err)
			}
		})
	}
}

func TestRuleDownloadHonorsCancellation(t *testing.T) {
	transport := http.DefaultTransport
	t.Cleanup(func() { http.DefaultTransport = transport })
	http.DefaultTransport = ruleTransportFunc(func(request *http.Request) (*http.Response, error) {
		return nil, request.Context().Err()
	})
	config, err := loadTestConfig(t, "tailscale:\n  exit_node: 100.64.0.10\nrules:\n  gfwlist: builtin\nstate_dir: ./state\n")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := CompileContext(ctx, config); !errors.Is(err, context.Canceled) {
		t.Fatal("initial rules download ignored cancellation:", err)
	}
}

func TestPreflightAndIgnoredRulesDoNotDownload(t *testing.T) {
	transport := http.DefaultTransport
	t.Cleanup(func() { http.DefaultTransport = transport })
	http.DefaultTransport = ruleTransportFunc(func(request *http.Request) (*http.Response, error) {
		t.Error("ignored rules triggered a download")
		return nil, errors.New("unexpected request")
	})
	for _, source := range []string{"builtin", "https://rules.example/subscription", "./missing-local-rules"} {
		config, err := loadTestConfig(t, "tailscale: {}\nrules:\n  gfwlist: "+source+"\nstate_dir: ./state\n")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := compilePreflight(config); err != nil {
			t.Fatalf("login/logout/discovery required split rules %q: %v", source, err)
		}
		for _, ignored := range []Config{
			{StateDir: config.StateDir, Rules: RulesConfig{Mode: RoutingGlobal, GFWList: source}},
			{StateDir: config.StateDir},
			{StateDir: config.StateDir, Rules: RulesConfig{GFWList: "./missing-local-rules"}},
		} {
			if err := EnsureRules(context.Background(), ignored, nil); err != nil {
				t.Fatal("ignored/local rules required a download:", err)
			}
		}
		if _, err := os.Stat(config.StateDir); !os.IsNotExist(err) {
			t.Fatal("preflight or ignored rules created persistent state:", err)
		}
	}
}

func TestStateLockPreventsTwoRunners(t *testing.T) {
	state := filepath.Join(t.TempDir(), "state")
	if err := privateState(state); err != nil {
		t.Fatal(err)
	}
	release, err := lockState(state)
	if err != nil {
		t.Fatal(err)
	}
	if other, err := lockState(state); err == nil {
		_ = other()
		t.Fatal("two runners acquired the same state")
	}
	if err := release(); err != nil {
		t.Fatal(err)
	}
	again, err := lockState(state)
	if err != nil {
		t.Fatal(err)
	}
	_ = again()
}
