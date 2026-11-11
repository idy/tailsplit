package tray

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/idy/tailsplit"
)

func installUpdateRuleFixture(t *testing.T, c *Controller, source string) (string, string) {
	t.Helper()
	data := strings.Replace(string(connectionSnapshotConfig), "source: ''", "source: '"+source+"'", 1)
	if err := os.WriteFile(c.options.ConfigPath, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
	config, err := tailsplit.Load(c.options.ConfigPath)
	if err != nil {
		t.Fatal(err)
	}
	path := config.Rules.GFWList
	if source == "builtin" || strings.HasPrefix(source, "https://") {
		path = tailsplit.RulesCachePath(config)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("||protected.example^\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	rules, err := tailsplit.CurrentRulesRevision(config)
	if err != nil {
		t.Fatal(err)
	}
	return path, rules
}

func reportUpdateRules(c *Controller, revision string) {
	c.onEvent([]byte(fmt.Sprintf(`{"event":"ready","mode":"proxy","routing_mode":"split","rules_revision":%q}`, revision)))
	c.onEvent([]byte(`{"event":"status","backend_state":"Running","exit_online":true}`))
}

func TestUpdatePolicyUsesActualRuleProjectionForLocalAndCachedSources(t *testing.T) {
	for _, source := range []string{"./rules.txt", "builtin", "https://rules.example/list.txt"} {
		t.Run(source, func(t *testing.T) {
			c := testController(t, "normal")
			path, rules := installUpdateRuleFixture(t, c, source)
			if err := c.Start(); err != nil {
				t.Fatal(err)
			}
			waitState(t, c, "running")
			reportUpdateRules(c, rules)
			before := c.Snapshot()
			token, err := UpdateRestartRevision(c.options.ConfigPath, before)
			if err != nil || token == "" || token == before.ActiveRevision {
				t.Fatal("rule projection was not bound to restart", token, err)
			}
			if err := os.WriteFile(path, []byte("! comment-only change\n||protected.example^\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			if same, err := UpdateRestartRevision(c.options.ConfigPath, before); err != nil || same != token {
				t.Fatal("non-policy edit changed canonical identity", same, err)
			}
			if err := os.WriteFile(path, []byte("||still-proxied.example^\n@@||protected.example^\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := UpdateRestartRevision(c.options.ConfigPath, before); !errors.Is(err, tailsplit.ErrRulesChanged) {
				t.Fatal("changed active rules were silently accepted", err)
			}
			called := false
			if err := c.CloseForUpdate(token, func(bool) error { called = true; return nil }); err == nil || called || !c.Snapshot().Active {
				t.Fatal("changed rules stopped the live helper", err, called)
			}
			newToken, err := UpdateRestartRevision(c.options.ConfigPath, Snapshot{})
			if err != nil {
				t.Fatal(err)
			}
			if err := c.CloseForUpdate(newToken, func(bool) error { called = true; return nil }); !errors.Is(err, tailsplit.ErrRulesChanged) || called {
				t.Fatal("new saved rules overrode the old compiled projection", err)
			}
			if err := c.Stop(); err != nil {
				t.Fatal(err)
			}
			if c.Snapshot().ActiveRulesRevision != "" {
				t.Fatal("stopped helper retained a policy identity")
			}
			if err := c.StartUpdateRevision(token); !errors.Is(err, ErrConfigChanged) || c.Snapshot().Active {
				t.Fatal("restart silently applied changed rules", err)
			}
		})
	}
}

func TestUpdatePolicyUnknownCompiledRulesRequireExplicitReconnect(t *testing.T) {
	c := testController(t, "normal")
	installUpdateRuleFixture(t, c, "./rules.txt")
	if err := c.Start(); err != nil {
		t.Fatal(err)
	}
	before := waitState(t, c, "running")
	if _, err := UpdateRestartRevision(c.options.ConfigPath, before); !errors.Is(err, tailsplit.ErrRulesChanged) {
		t.Fatal("unknown helper projection was trusted", err)
	}
}

func TestUpdateRestartPassesExpectedRulesToHelper(t *testing.T) {
	c := testController(t, "normal")
	_, rules := installUpdateRuleFixture(t, c, "./rules.txt")
	token, err := UpdateRestartRevision(c.options.ConfigPath, Snapshot{})
	if err != nil {
		t.Fatal(err)
	}
	if err := c.StartUpdateRevision(token); err != nil {
		t.Fatal(err)
	}
	waitState(t, c, "running")
	c.mu.Lock()
	args := slices.Clone(c.command.Args)
	c.mu.Unlock()
	index := slices.Index(args, "--rules-revision")
	if index < 0 || index+1 >= len(args) || args[index+1] != rules {
		t.Fatal("helper lacks the final compiled-rule guard", args)
	}
}

func TestCloseForUpdateRuleChangeDuringCleanupCancelsRestart(t *testing.T) {
	c, listener := cancellationBarrierController(t, false)
	path, rules := installUpdateRuleFixture(t, c, "./rules.txt")
	if err := c.ForOperation("connection").Start(); err != nil {
		t.Fatal(err)
	}
	helper := acceptCancellationBarrier(t, listener, "running")
	release := sync.OnceFunc(func() { _, _ = helper.Write([]byte{1}) })
	t.Cleanup(release)
	reportUpdateRules(c, rules)
	token, err := UpdateRestartRevision(c.options.ConfigPath, c.Snapshot())
	if err != nil {
		t.Fatal(err)
	}
	result := make(chan error, 1)
	reconnect := true
	go func() { result <- c.CloseForUpdate(token, func(value bool) error { reconnect = value; return nil }) }()
	waitCancellationPhase(t, helper, "stopping")
	if err := os.WriteFile(path, []byte("||still-proxied.example^\n@@||protected.example^\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	release()
	if err := cancellationResult(t, result); err != nil || reconnect {
		t.Fatal("rules changed during cleanup but restart remained armed", reconnect, err)
	}
}
