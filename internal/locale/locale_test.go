package locale

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

func TestPersistenceAndValidation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	s := NewStore(path)
	s.hostLanguage = "zh"
	if got := s.Get(); got != (State{"auto", "zh"}) {
		t.Fatal(got)
	}
	for _, preference := range []string{"en", "zh", "auto"} {
		if err := s.Set(preference); err != nil {
			t.Fatal(err)
		}
		if got := NewStore(path).Get(); got.Preference != preference {
			t.Fatal(got)
		}
	}
	before := s.Get()
	for _, invalid := range []string{"", "EN", "zh-CN", "fr", " auto"} {
		if s.Set(invalid) == nil || s.Get() != before {
			t.Fatalf("accepted %q", invalid)
		}
	}
	for input, want := range map[string]string{"zh": "zh", "zh_CN.UTF-8": "zh", "zh-Hant-TW": "zh", "en-GB": "en", "fr-FR": "en", "C": "en", "": "en"} {
		if got := normalize(input); got != want {
			t.Fatalf("%s: %s", input, got)
		}
	}
}

func TestFailedSaveAndUnsafeFiles(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	s := NewStore(path)
	if err := s.Set("en"); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(s.path); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(t.TempDir(), "target")
	if err := os.WriteFile(target, []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, s.path); err != nil {
		t.Skip(err)
	}
	if s.Set("zh") == nil || s.Get().Preference != "en" {
		t.Fatal("failed save published state")
	}
	if got := NewStore(path).Get(); got.Preference != "auto" {
		t.Fatal("read unsafe file", got)
	}
	data, _ := os.ReadFile(target)
	if string(data) != "keep" {
		t.Fatal("modified symlink target")
	}
	missing := NewStore(filepath.Join(t.TempDir(), "missing", "config.yaml"))
	before := missing.Get()
	if missing.Set("zh") == nil || missing.Get() != before {
		t.Fatal("missing parent save succeeded")
	}
}

func TestMalformedPreferencesFallback(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	for _, content := range []string{
		`{"preference":"fr"}`, `{"preference":null}`, `{"preference":"zh"} {}`,
		`{"preference":"zh","companion_enabled":null}`, `{"preference":"zh","companion_enabled":"false"}`,
		`{"preference":"zh","companion_enabled":0}`, `null`, `[]`, `bad JSON`,
	} {
		if err := os.WriteFile(path+".ui.json", []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
		store := NewStore(path)
		if got := store.Get(); got.Preference != "auto" || !store.CompanionEnabled() {
			t.Fatal(content, got, store.CompanionEnabled())
		}
	}
}

func TestCompanionPreferencePreservesLanguageAndOtherUISettings(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	if !NewStore(path).CompanionEnabled() {
		t.Fatal("companion must default to enabled")
	}
	// Existing language-only files keep their selection and enable the companion.
	if err := os.WriteFile(path+".ui.json", []byte(`{"preference":"zh","theme":{"mode":"dark"}}`), 0600); err != nil {
		t.Fatal(err)
	}
	store := NewStore(path)
	if store.Get().Preference != "zh" || !store.CompanionEnabled() {
		t.Fatal("legacy language preference did not load")
	}
	if err := store.SetCompanionEnabled(false); err != nil {
		t.Fatal(err)
	}
	store = NewStore(path)
	if store.Get().Preference != "zh" || store.CompanionEnabled() {
		t.Fatal("companion change lost language or did not persist")
	}
	if err := store.Set("en"); err != nil {
		t.Fatal(err)
	}
	store = NewStore(path)
	if store.Get().Preference != "en" || store.CompanionEnabled() {
		t.Fatal("language change lost companion preference")
	}
	for _, enabled := range []bool{true, false} {
		if err := store.SetCompanionEnabled(enabled); err != nil {
			t.Fatal(err)
		}
		store = NewStore(path)
		if store.CompanionEnabled() != enabled || store.Get().Preference != "en" {
			t.Fatal("companion preference did not survive restart")
		}
	}
	data, err := os.ReadFile(path + ".ui.json")
	if err != nil {
		t.Fatal(err)
	}
	var saved map[string]json.RawMessage
	if err := json.Unmarshal(data, &saved); err != nil {
		t.Fatal(err)
	}
	if string(saved["theme"]) != `{"mode":"dark"}` || string(saved["companion_enabled"]) != "false" {
		t.Fatal("other UI preferences were discarded", string(data))
	}
	if info, err := os.Stat(path + ".ui.json"); err != nil || info.Mode().Perm() != 0600 {
		t.Fatal("UI preferences are not private", info, err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("UI preference wrote network YAML", err)
	}
}

func TestCompanionFailedSaveDoesNotPublishState(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	store := NewStore(path)
	if err := store.Set("zh"); err != nil {
		t.Fatal(err)
	}
	if err := store.SetCompanionEnabled(false); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(store.path); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(t.TempDir(), "target")
	if err := os.WriteFile(target, []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, store.path); err != nil {
		t.Skip(err)
	}
	if store.SetCompanionEnabled(true) == nil || store.CompanionEnabled() || store.Get().Preference != "zh" {
		t.Fatal("failed save changed UI preferences")
	}
	data, _ := os.ReadFile(target)
	if string(data) != "keep" {
		t.Fatal("modified symlink target")
	}
	if !NewStore(path).CompanionEnabled() {
		t.Fatal("unsafe UI preferences did not fall back to enabled")
	}
}

func TestMissingDirectoriesUnderExistingAlias(t *testing.T) {
	root := t.TempDir()
	actual := filepath.Join(root, "actual")
	alias := filepath.Join(root, "alias")
	if err := os.Mkdir(actual, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(actual, alias); err != nil {
		t.Skip(err)
	}
	path := filepath.Join(alias, "new-dir", "nested", "config.yaml")
	store := NewStore(path)
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := store.Set("zh"); err != nil {
		t.Fatal("save after config initialization:", err)
	}
	if got := NewStore(path).Get(); got != (State{Preference: "zh", Language: "zh"}) {
		t.Fatal("preferences did not survive relaunch", got)
	}

	// A directory absent at construction must not become a redirect later.
	redirectedPath := filepath.Join(alias, "later", "config.yaml")
	redirected := NewStore(redirectedPath)
	target := t.TempDir()
	if err := os.Symlink(target, filepath.Dir(redirectedPath)); err != nil {
		t.Fatal(err)
	}
	before := redirected.Get()
	if redirected.Set("en") == nil || redirected.Get() != before {
		t.Fatal("accepted post-start directory symlink")
	}
	if _, err := os.Stat(filepath.Join(target, "config.yaml.ui.json")); !os.IsNotExist(err) {
		t.Fatal("wrote through post-start symlink", err)
	}
}

func TestConcurrentAccess(t *testing.T) {
	s := NewStore(filepath.Join(t.TempDir(), "config.yaml"))
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 20; j++ {
				preference := []string{"en", "zh", "auto"}[j%3]
				if err := s.Set(preference); err != nil {
					t.Error(err)
					return
				}
				if err := s.SetCompanionEnabled(j%2 == 0); err != nil {
					t.Error(err)
					return
				}
				_ = s.CompanionEnabled()
				state := s.Get()
				if !valid(state.Preference) || state.Language != "en" && state.Language != "zh" {
					t.Error(state)
				}
			}
		}()
	}
	wg.Wait()
}
