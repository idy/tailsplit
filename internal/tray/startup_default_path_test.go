package tray

import (
	"bytes"
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNativeStartupWithReadOnlyDefaultConfigDirectory(t *testing.T) {
	for _, checked := range []bool{false, true} {
		t.Run(fmt.Sprintf("revision-checked=%v", checked), func(t *testing.T) {
			_, data, _, _ := readOnlyStartupConfig(t)
			configRoot, err := os.UserConfigDir()
			if err != nil {
				t.Fatal(err)
			}
			directory := filepath.Join(configRoot, "TailSplit")
			logs := filepath.Join(directory, "logs")
			if err := os.MkdirAll(logs, 0o700); err != nil {
				t.Fatal(err)
			}
			configPath := filepath.Join(directory, "config.yaml")
			rulePath := filepath.Join(directory, "rules.txt")
			if err := os.WriteFile(configPath, data, 0o400); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(rulePath, []byte("||example.com\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			if err := os.Chmod(directory, 0o500); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = os.Chmod(directory, 0o700) })
			fallback, err := connectionSnapshotFallbackPath()
			if err != nil {
				t.Fatal(err)
			}
			if _, err := os.Lstat(filepath.Dir(fallback)); !errors.Is(err, os.ErrNotExist) {
				t.Fatal("fixture must start without a preexisting fallback directory", err)
			}
			relative, err := filepath.Rel(directory, filepath.Dir(fallback))
			if err != nil || relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
				t.Fatal("fallback is still inside the read-only default configuration directory", relative, err)
			}
			stateDir := filepath.Join(configRoot, "state") // Captured YAML uses ../state.
			controller := startupTestController(t, configPath, stateDir, rulePath, "traffic", filepath.Join(logs, "tailsplit.log"))
			revision := fmt.Sprintf("%x", sha256.Sum256(data))
			if checked {
				err = controller.StartRevision(revision)
			} else {
				err = controller.Start()
			}
			if err != nil {
				t.Fatal("read-only default config directory prevented startup with writable logs/state", err)
			}
			if snapshot := waitState(t, controller, "running"); snapshot.ActiveRevision != revision {
				t.Fatal("default-path fallback lost the captured revision", snapshot)
			}
			helperPath := startupHelperConfigPath(t, controller)
			if filepath.Dir(helperPath) != filepath.Dir(fallback) || startupHelperConfigBase(t, controller) != directory {
				t.Fatal("helper did not use independent storage and the original config base", helperPath)
			}
			if current, err := os.ReadFile(configPath); err != nil || !bytes.Equal(current, data) {
				t.Fatal("startup changed the read-only source", err)
			}
			if info, err := os.Stat(directory); err != nil || info.Mode().Perm() != 0o500 {
				t.Fatal("startup changed source directory permissions", err)
			}
			if err := controller.Stop(); err != nil {
				t.Fatal(err)
			}
			waitStartupSnapshotRemoved(t, helperPath)
		})
	}
}
