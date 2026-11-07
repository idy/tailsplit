package main

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestDesktopLogTailRedactsHelperAuthenticationMaterial(t *testing.T) {
	path := filepath.Join(t.TempDir(), "private.log")
	data := "diagnostic before\nhttps://login.tailscale.com/a/private-login?token=private tskey-auth-private\ndiagnostic after\n"
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
	text, err := desktopLogTail(path)
	if err != nil {
		t.Fatal(err)
	}
	if text != "diagnostic before\n[redacted] [redacted]\ndiagnostic after\n" {
		t.Fatalf("unexpected redacted diagnostic text: %q", text)
	}
}

func TestDesktopLogTailBoundsHistoryAndOmitsPartialSecrets(t *testing.T) {
	for _, prefix := range []string{"https://login.tailscale.com/a/", "tskey-auth-"} {
		t.Run(prefix, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "private.log")
			data := "old entry\n" + prefix + strings.Repeat("private", desktopLogLimit) + "\nlatest entry\n"
			if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
				t.Fatal(err)
			}
			text, err := desktopLogTail(path)
			if err != nil || text != "latest entry\n" {
				t.Fatalf("a truncated log entry leaked past its missing secret prefix: %q, %v", text, err)
			}
		})
	}
	path := filepath.Join(t.TempDir(), "private.log")
	data := "oldest entry\n" + strings.Repeat("diagnostic entry\n", desktopLogLimit/4) + "latest entry\n"
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
	text, err := desktopLogTail(path)
	if err != nil || len(text) > desktopLogLimit || strings.Contains(text, "oldest entry") || !strings.HasSuffix(text, "latest entry\n") {
		t.Fatalf("log history was not bounded to its latest entries: length %d, %v", len(text), err)
	}
}

func TestDesktopLogTailRemainsBoundedAfterUTF8AndRedactionExpansion(t *testing.T) {
	for _, test := range []struct {
		name string
		data string
	}{
		{"invalid-utf8", strings.Repeat("\xff", desktopLogLimit-1) + "\n"},
		{"redaction-expansion", strings.Repeat("tskey-a ", desktopLogLimit/8)},
		{"empty", ""},
		{"oversized-entry", strings.Repeat("x", desktopLogLimit+1)},
	} {
		t.Run(test.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "private.log")
			if err := os.WriteFile(path, []byte(test.data), 0o600); err != nil {
				t.Fatal(err)
			}
			text, err := desktopLogTail(path)
			if err != nil || len(text) > desktopLogLimit || !utf8.ValidString(text) || strings.Contains(text, "tskey-") {
				t.Fatalf("invalid log output after sanitizing: length %d, %v", len(text), err)
			}
			if test.name == "empty" || test.name == "oversized-entry" {
				if text != "" {
					t.Fatal("expected no complete log entries")
				}
			} else if text == "" {
				t.Fatal("lost complete log entries")
			}
		})
	}
}

func TestDesktopLogTailReportsMissingFile(t *testing.T) {
	text, err := desktopLogTail(filepath.Join(t.TempDir(), "missing.log"))
	if text != "" || !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("missing log result: %q, %v", text, err)
	}
}
