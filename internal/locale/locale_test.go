package locale

import (
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
	for _, content := range []string{`{"preference":"fr"}`, `{"preference":"zh"} {}`, `{"preference":"zh","unknown":true}`, `null`, `bad JSON`} {
		if err := os.WriteFile(path+".ui.json", []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
		if got := NewStore(path).Get(); got.Preference != "auto" {
			t.Fatal(content, got)
		}
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
				state := s.Get()
				if !valid(state.Preference) || state.Language != "en" && state.Language != "zh" {
					t.Error(state)
				}
			}
		}()
	}
	wg.Wait()
}
