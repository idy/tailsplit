package tailsplit

import (
	"encoding/base64"
	"encoding/hex"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/idy/tailsplit/internal/domainrules"
)

func TestRulesRevisionUsesCanonicalActiveProjection(t *testing.T) {
	config, err := loadTestConfig(t, "state_dir: ./state\ntailscale:\n  exit_node: 100.64.0.10\nrules:\n  source: ./rules.txt\n")
	if err != nil {
		t.Fatal(err)
	}
	body := "||proxy.example\n||second.example\n|https://exact.example/\n||*.wild.example\n@@||safe.example\n"
	var revision string
	for _, data := range []string{
		"[AutoProxy 0.2.9]\n" + body,
		"# changed metadata\n@@||safe.example\n||*.wild.example\n|https://exact.example/\n||second.example\n||proxy.example\n||proxy.example\n||ignored.example/private/path\n",
		base64.StdEncoding.EncodeToString([]byte(strings.ReplaceAll(body, "\n", "\r\n"))),
	} {
		if err := os.WriteFile(config.Rules.GFWList, []byte(data), 0o600); err != nil {
			t.Fatal(err)
		}
		compiled, err := Compile(config)
		if err != nil {
			t.Fatal(err)
		}
		current, err := CurrentRulesRevision(config)
		if err != nil || current != compiled.RulesRevision {
			t.Fatal("current and compiled projections differ", current, compiled.RulesRevision, err)
		}
		if decoded, err := hex.DecodeString(current); err != nil || len(decoded) != 32 {
			t.Fatal("revision is not a SHA-256 digest", current, err)
		}
		if revision == "" {
			revision = current
		} else if current != revision {
			t.Fatal("formatting, ordering, duplicates or diagnostics changed the projection identity")
		}
	}
	upstream, err := domainrules.ProjectRules([]byte(body))
	if err != nil {
		t.Fatal(err)
	}
	if rulesProjectionRevision(&upstream) == revision {
		t.Fatal("compiled revision omitted the active supplemental rules")
	}
	for name, changed := range map[string]string{
		"proxy suffix":  strings.ReplaceAll(body, "proxy.example", "changed.example"),
		"direct suffix": strings.ReplaceAll(body, "safe.example", "changed.example"),
		"exact":         strings.ReplaceAll(body, "exact.example", "changed.example"),
		"regex":         strings.ReplaceAll(body, "wild.example", "changed.example"),
	} {
		t.Run(name, func(t *testing.T) {
			if err := os.WriteFile(config.Rules.GFWList, []byte(changed), 0o600); err != nil {
				t.Fatal(err)
			}
			current, err := CurrentRulesRevision(config)
			if err != nil || current == revision {
				t.Fatal("effective rule change kept the old identity", err)
			}
		})
	}
}

func TestRulesRevisionTracksLocalAndCachedChanges(t *testing.T) {
	for _, source := range []string{"local", "builtin", "https://rules.example/list"} {
		t.Run(source, func(t *testing.T) {
			directory := t.TempDir()
			path := filepath.Join(directory, "rules.txt")
			if source == "local" {
				source = path
			}
			config, err := Normalize(Config{
				StateDir: directory, Tailscale: &TailscaleConfig{ExitNode: "100.64.0.10"},
				Rules: RulesConfig{GFWList: source},
			}, directory)
			if err != nil {
				t.Fatal(err)
			}
			if source != path {
				path = RulesCachePath(config)
			}
			write := func(data string) {
				t.Helper()
				if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			write("||first.example\n@@||safe.first.example\n")
			compiled, err := Compile(config)
			if err != nil {
				t.Fatal(err)
			}
			before, err := CurrentRulesRevision(config)
			if err != nil || before != compiled.RulesRevision {
				t.Fatal("disk identity differs from the actual compiled rules", err)
			}
			write("||second.example\n@@||safe.second.example\n")
			after, err := CurrentRulesRevision(config)
			if err != nil || after == before || compiled.RulesRevision != before {
				t.Fatal("rule replacement was missed or mutated an already compiled identity", err)
			}
			recompiled, err := Compile(config)
			if err != nil || recompiled.RulesRevision != after {
				t.Fatal("new compilation did not capture the replacement projection", err)
			}
			for _, data := range []string{"", "<html>\n||second.example\n</html>", strings.Repeat("x", (2<<20)+1)} {
				write(data)
				if revision, err := CurrentRulesRevision(config); err == nil || revision != "" {
					t.Fatal("invalid rule file received a usable revision", revision, err)
				}
			}
			if err := os.Remove(path); err != nil {
				t.Fatal(err)
			}
			if revision, err := CurrentRulesRevision(config); !os.IsNotExist(err) || revision != "" {
				t.Fatal("missing rule file received a usable revision", revision, err)
			}
		})
	}
}

func TestCurrentRulesRevisionNeverDownloadsOrMigratesCache(t *testing.T) {
	transport := http.DefaultTransport
	t.Cleanup(func() { http.DefaultTransport = transport })
	http.DefaultTransport = ruleTransportFunc(func(*http.Request) (*http.Response, error) {
		t.Fatal("reading a rules revision attempted a network request")
		return nil, nil
	})
	for _, source := range []string{"builtin", "https://rules.example/list"} {
		directory := t.TempDir()
		config := Config{StateDir: directory, Rules: RulesConfig{GFWList: source},
			rulesCacheSource: rulesSubscriptionSource(source), rulesCacheStateDir: directory}
		legacy := filepath.Join(directory, "gfwlist.txt")
		if err := os.WriteFile(legacy, []byte("||legacy.example\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		if revision, err := CurrentRulesRevision(config); !os.IsNotExist(err) || revision != "" {
			t.Fatal("missing source-specific cache should fail locally", revision, err)
		}
		if _, err := os.Stat(legacy); err != nil {
			t.Fatal("revision lookup moved the legacy cache", err)
		}
		if _, err := os.Stat(RulesCachePath(config)); !os.IsNotExist(err) {
			t.Fatal("revision lookup created a cache", err)
		}
	}
}

func TestRulesRevisionIgnoresFilesForEmptySourceAndGlobal(t *testing.T) {
	var emptyRevision string
	for _, source := range []string{"", "missing.txt", "invalid.txt", "builtin", "https://rules.example/list"} {
		t.Run(source, func(t *testing.T) {
			directory := t.TempDir()
			mode := RoutingGlobal
			if source == "" {
				mode = RoutingSplit
			}
			config, err := Normalize(Config{
				StateDir: filepath.Join(directory, "state"), Tailscale: &TailscaleConfig{ExitNode: "100.64.0.10"},
				Rules: RulesConfig{GFWList: source, Mode: mode}, TUN: TUNConfig{Enabled: true},
			}, directory)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(directory, "invalid.txt"), []byte("not a rule list"), 0o600); err != nil {
				t.Fatal(err)
			}
			current, err := CurrentRulesRevision(config)
			if err != nil {
				t.Fatal("ignored source prevented a revision lookup", err)
			}
			compiled, err := Compile(config)
			if err != nil || current != compiled.RulesRevision {
				t.Fatal("ignored input affected the compiled revision", err)
			}
			if emptyRevision == "" {
				emptyRevision = current
			}
			if current == "" || current != emptyRevision {
				t.Fatal("ignored sources did not share the empty projection identity")
			}
			if _, err := os.Stat(config.StateDir); !os.IsNotExist(err) {
				t.Fatal("revision lookup or compilation created runtime state", err)
			}
		})
	}
}

func TestRulesRevisionRecordsPreparationRecompilation(t *testing.T) {
	config := focusedExitTestConfig(t)
	config.Rules.Final, config.Rules.Routes = "direct", nil
	config.Rules.GFWList = filepath.Join(t.TempDir(), "rules.txt")
	if err := os.WriteFile(config.Rules.GFWList, []byte("||before.example\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	compiled, err := Compile(config)
	if err != nil {
		t.Fatal(err)
	}
	before := compiled.RulesRevision
	config.ExpectedRulesRevision = before
	if err := os.WriteFile(config.Rules.GFWList, []byte("||after.example\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	// This is the same recompilation path used by PrepareConnection when an
	// optional device is signed out and its private namespace must be blocked.
	if err := isolateOptionalExits(t.Context(), config, &compiled,
		[]engineExit{{id: "office", tailnet: "office.ts.net"}},
		map[string]Status{"office": {BackendState: "NeedsLogin"}}, nil); err != nil {
		t.Fatal(err)
	}
	current, err := CurrentRulesRevision(config)
	if err != nil || current != compiled.RulesRevision || compiled.RulesRevision == before {
		t.Fatal("preparation retained the initial compilation's stale identity", err)
	}
	if !strings.Contains(string(compiled.JSON), "after.example") || strings.Contains(string(compiled.JSON), "before.example") {
		t.Fatal("recorded revision did not describe the rules actually recompiled")
	}
}
