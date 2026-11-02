//go:build windows

package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/idy/tailsplit/internal/privatefs"
)

func TestWindowsOfflineCommandsDoNotCreateRuntimeState(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, "config.yaml")
	state := filepath.Join(directory, "unused-state")
	file, err := privatefs.CreateFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.WriteString("version: 1\nstate_dir: ./unused-state\n"); err != nil {
		_ = file.Close()
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"version"}, {"--help"}, {"check", "-c", path}, {"render", "-c", path}} {
		var output, diagnostic bytes.Buffer
		if err := execute(args, &output, &diagnostic); err != nil {
			t.Fatalf("%v: %v, %s", args, err, diagnostic.String())
		}
		if args[0] == "check" && !strings.Contains(output.String(), "Configuration valid") {
			t.Fatal("offline validation did not succeed:", output.String())
		}
		if args[0] == "render" && !json.Valid(output.Bytes()) {
			t.Fatal("render did not produce JSON:", output.String())
		}
	}
	if _, err := os.Stat(state); !os.IsNotExist(err) {
		t.Fatal("offline commands created runtime state:", err)
	}
}
