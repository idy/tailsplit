// Package locale persists the app's UI language independently of network configuration.
package locale

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/idy/tailsplit/internal/privatefs"
)

type State struct {
	Preference string `json:"preference"`
	Language   string `json:"language"`
}

type Store struct {
	mu           sync.RWMutex
	path         string
	preference   string
	hostLanguage string
}

func NewStore(configPath string) *Store {
	// Resolve existing parent aliases once, including when initialization has
	// not yet created the config directory. Never resolve the preference file.
	path := resolveParentAliases(configPath + ".ui.json")
	s := &Store{path: path, preference: "auto", hostLanguage: detectLanguage()}
	if safePath(s.path) != nil {
		return s
	}
	f, err := os.Open(s.path)
	if err != nil {
		return s
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() > 1024 {
		return s
	}
	var saved struct {
		Preference string `json:"preference"`
	}
	d := json.NewDecoder(io.LimitReader(f, 1025))
	d.DisallowUnknownFields()
	var extra any
	if d.Decode(&saved) == nil && d.Decode(&extra) == io.EOF && valid(saved.Preference) {
		s.preference = saved.Preference
	}
	return s
}

// resolveParentAliases canonicalizes the nearest existing ancestor and retains
// missing trailing directories. Later-created symlinks still fail safePath.
func resolveParentAliases(path string) string {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return path
	}
	parent := filepath.Dir(absolute)
	suffix := []string{filepath.Base(absolute)}
	for {
		_, err := os.Lstat(parent)
		if err == nil {
			resolved, err := filepath.EvalSymlinks(parent)
			if err != nil {
				return absolute
			}
			for i := len(suffix) - 1; i >= 0; i-- {
				resolved = filepath.Join(resolved, suffix[i])
			}
			return resolved
		}
		if !errors.Is(err, os.ErrNotExist) || filepath.Dir(parent) == parent {
			return absolute
		}
		suffix = append(suffix, filepath.Base(parent))
		parent = filepath.Dir(parent)
	}
}

func (s *Store) Get() State {
	s.mu.RLock()
	defer s.mu.RUnlock()
	language := s.preference
	if language == "auto" {
		language = s.hostLanguage
	}
	return State{Preference: s.preference, Language: language}
}

func valid(preference string) bool {
	return preference == "auto" || preference == "en" || preference == "zh"
}

// safePath rejects symlinks in the preference path and its ancestors. The
// preferences are public UI settings, but must never redirect writes elsewhere.
func safePath(path string) error {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	for current := absolute; ; current = filepath.Dir(current) {
		info, err := os.Lstat(current)
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		if err == nil && (info.Mode()&os.ModeSymlink != 0 || current == absolute && !info.Mode().IsRegular()) {
			return errors.New("UI preferences path must not contain symlinks or non-regular files")
		}
		if filepath.Dir(current) == current {
			return nil
		}
	}
}

func (s *Store) Set(preference string) error {
	if !valid(preference) {
		return errors.New("language preference must be auto, en, or zh")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := safePath(s.path); err != nil {
		return err
	}
	data, err := json.Marshal(struct {
		Preference string `json:"preference"`
	}{preference})
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(s.path), ".tailsplit-ui-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err = f.Write(append(data, '\n')); err != nil {
		f.Close()
		return err
	}
	if err = f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	if err = safePath(s.path); err != nil {
		return err
	}
	if err = privatefs.Replace(f.Name(), s.path); err != nil {
		return err
	}
	s.preference = preference
	return nil
}

func normalize(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	value = strings.ReplaceAll(value, "_", "-")
	if value == "zh" || strings.HasPrefix(value, "zh-") || strings.HasPrefix(value, "zh.") {
		return "zh"
	}
	return "en"
}

func detectLanguage() string {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	switch runtime.GOOS {
	case "darwin":
		if output, err := exec.CommandContext(ctx, "/usr/bin/defaults", "read", "-g", "AppleLanguages").Output(); err == nil {
			// defaults prints an ordered property-list array; use its first entry.
			value := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(string(output)), "("))
			value = strings.TrimSpace(strings.SplitN(value, ",", 2)[0])
			value = strings.Trim(strings.TrimSpace(strings.TrimSuffix(value, ")")), "\" \n\t")
			if value != "" {
				return normalize(value)
			}
		}
	}
	if language := nativeUILanguage(); language != "" {
		return language
	}
	for _, key := range []string{"LC_ALL", "LC_MESSAGES", "LANGUAGE", "LANG"} {
		if value := os.Getenv(key); value != "" {
			return normalize(strings.SplitN(value, ":", 2)[0])
		}
	}
	return "en"
}
