package main

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/idy/tailsplit"
)

func TestNativePrimarySignInUsesPanelWhenPrimaryIsAbsent(t *testing.T) {
	for _, test := range []struct {
		name, text string
		panel      bool
	}{
		{"fresh install", string(tailsplit.DefaultYAML), true},
		{"primary removed", "tailscale: null\nexits:\n  - id: office\n    exit_node: 100.64.0.10\n", true},
		{"legacy primary", "tailscale: {}\n", false},
	} {
		t.Run(test.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "config.yaml")
			if err := os.WriteFile(path, []byte(test.text), 0o600); err != nil {
				t.Fatal(err)
			}
			panels, logins := 0, 0
			if err := nativePrimarySignIn(path, func() error { panels++; return nil }, func() error { logins++; return nil }); err != nil {
				t.Fatal(err)
			}
			if test.panel && (panels != 1 || logins != 0) || !test.panel && (panels != 0 || logins != 1) {
				t.Fatalf("wrong native onboarding panels=%d logins=%d", panels, logins)
			}
		})
	}
}

func TestNativePrimarySignInReturnsPanelFailure(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, tailsplit.DefaultYAML, 0o600); err != nil {
		t.Fatal(err)
	}
	failure := errors.New("panel unavailable")
	if err := nativePrimarySignIn(path, func() error { return failure }, func() error { t.Fatal("missing main started login"); return nil }); !errors.Is(err, failure) {
		t.Fatal("panel failure was hidden", err)
	}
}
