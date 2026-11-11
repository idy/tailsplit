package main

import (
	"path/filepath"
	"strings"
	"testing"
	"unicode"

	"github.com/idy/tailsplit/internal/locale"
	"github.com/idy/tailsplit/internal/tray"
)

func TestNativeLanguageUsesSharedStore(t *testing.T) {
	old := uiLanguage
	t.Cleanup(func() { uiLanguage = old })
	uiLanguage = locale.NewStore(filepath.Join(t.TempDir(), "config.yaml"))
	var flow tray.LoginFlow
	flow.Request()
	for _, language := range []string{"en", "zh", "en"} {
		if err := uiLanguage.Set(language); err != nil {
			t.Fatal(err)
		}
		if currentLanguage() != language {
			t.Fatalf("language did not follow store: %s", currentLanguage())
		}
		want := "Connect"
		if language == "zh" {
			want = "连接"
		}
		if tr("连接") != want {
			t.Fatalf("translation = %q, want %q", tr("连接"), want)
		}
		if !flow.ShowMessage() {
			t.Fatal("changing language lost pending sign-in")
		}
	}
	if link := flow.Next(tray.Snapshot{LoginURL: "https://login.tailscale.com/a/test"}); link == "" {
		t.Fatal("pending sign-in did not survive language change")
	}
}

func TestConnectionPresentationBothLanguages(t *testing.T) {
	states := []string{"running", "blocked", "discovering", "login", "authenticating", "stopping", "authorizing", "waiting-exit", "missing-exit", "logging-out", "starting", "failed"}
	for _, state := range states {
		for _, active := range []bool{false, true} {
			for _, mode := range []string{"proxy", "tun"} {
				snapshot := tray.Snapshot{State: state, Active: active, Mode: mode}
				zh := connectionPresentationForLanguage(snapshot, "zh")
				en := connectionPresentationForLanguage(snapshot, "en")
				if zh != connectionPresentation(snapshot) {
					t.Fatal("Chinese presentation changed")
				}
				if zh == en || strings.ContainsFunc(en.Summary+en.Action, func(r rune) bool { return unicode.Is(unicode.Han, r) }) {
					t.Fatalf("untranslated %s: %+v", state, en)
				}
			}
		}
	}
}
