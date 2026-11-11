package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestRenderCapturedSourceResolvesPathsUsingExplicitBase(t *testing.T) {
	base, snapshots := t.TempDir(), t.TempDir()
	path := filepath.Join(snapshots, "captured.yaml")
	if err := os.WriteFile(path, []byte("state_dir: ./profile\nrules: {source: \"\", default_exit: \"-\", final: direct}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	var out, diagnostic bytes.Buffer
	if err := execute([]string{"render", "-c", path, "--config-base", base}, &out, &diagnostic); err != nil {
		t.Fatalf("render captured source: %v %s", err, diagnostic.String())
	}
	var compiled struct {
		Experimental struct {
			CacheFile struct {
				Path string `json:"path"`
			} `json:"cache_file"`
		} `json:"experimental"`
	}
	if err := json.Unmarshal(out.Bytes(), &compiled); err != nil {
		t.Fatal(err)
	}
	if compiled.Experimental.CacheFile.Path != filepath.Join(base, "profile", "cache.db") {
		t.Fatal("CLI ignored original config base", compiled.Experimental.CacheFile.Path)
	}
}

func TestInitRejectsConfigBaseBeforeCreatingFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	var out, diagnostic bytes.Buffer
	if err := execute([]string{"init", "-c", path, "--config-base", t.TempDir()}, &out, &diagnostic); err == nil {
		t.Fatal("init silently ignored a loading-only option")
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("rejected init created a file", err)
	}
}
