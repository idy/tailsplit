package tray

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/idy/tailsplit"
	"github.com/idy/tailsplit/internal/privatefs"

	"gopkg.in/yaml.v3"
)

func TestStartupConfigHelperProcess(t *testing.T) {
	mode := os.Getenv("TAILSPLIT_STARTUP_HELPER")
	if mode == "" {
		return
	}
	fail := func(message string, err error) {
		fmt.Fprintln(os.Stderr, message, err)
		os.Exit(81)
	}
	index := slices.Index(os.Args, "-c")
	if index < 0 || index+1 >= len(os.Args) {
		fail("missing configuration argument", nil)
	}
	var config tailsplit.Config
	var err error
	if baseIndex := slices.Index(os.Args, "--config-base"); baseIndex >= 0 && baseIndex+1 < len(os.Args) {
		config, err = tailsplit.LoadDraftWithBase(os.Args[index+1], os.Args[baseIndex+1])
	} else {
		config, err = tailsplit.LoadDraft(os.Args[index+1])
	}
	if err != nil {
		fail("could not load helper configuration", err)
	}
	if config.StateDir != os.Getenv("TAILSPLIT_STARTUP_STATE") || config.Rules.GFWList != os.Getenv("TAILSPLIT_STARTUP_RULES") {
		fail("configuration relative paths changed", nil)
	}
	current, currentErr := os.Stat(".")
	base, baseErr := os.Stat(os.Getenv("TAILSPLIT_STARTUP_BASE"))
	if currentErr != nil || baseErr != nil || !os.SameFile(current, base) {
		fail("helper working directory changed", errors.Join(currentErr, baseErr))
	}
	command := "run"
	if mode == "discovery" {
		command = "discover"
	}
	if runtime.GOOS == "darwin" || runtime.GOOS == "windows" {
		command = "app-run"
		if mode == "login" {
			command = "app-login"
		} else if mode == "discovery" {
			command = "app-discover"
		}
	}
	if !slices.Contains(os.Args, command) {
		fail("wrong startup command", nil)
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	unlock, err := tailsplit.LockSavedState(config.StateDir)
	if err != nil {
		fail("could not acquire saved state lease", err)
	}
	fmt.Println(`{"event":"ready"}`)
	exit := ""
	if mode == "traffic" && config.TrafficExitID() != tailsplit.MainExit {
		exit = config.TrafficExitID()
	}
	fmt.Printf("{\"event\":\"status\",\"exit\":%q,\"backend_state\":\"Running\",\"exit_online\":true}\n", exit)
	if mode == "login" {
		fmt.Println(`{"event":"authenticated","mode":"login"}`)
	}
	<-ctx.Done()
	if err := unlock(); err != nil {
		fail("could not release saved state lease", err)
	}
	os.Exit(0)
}

func startupTestController(t *testing.T, configPath, stateDir, rulePath, mode string) *Controller {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("production-argument fixture uses a POSIX executable wrapper")
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	directory := t.TempDir()
	helper := filepath.Join(directory, "startup helper")
	quoted := "'" + strings.ReplaceAll(executable, "'", "'\\''") + "'"
	if err := os.WriteFile(helper, []byte("#!/bin/sh\nexec "+quoted+" -test.run=^TestStartupConfigHelperProcess$ -- \"$@\"\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	controller, err := New(Options{
		Helper: helper, ConfigPath: configPath, LogPath: filepath.Join(directory, "logs", "core.log"),
		ExtraEnv: []string{"TAILSPLIT_STARTUP_HELPER=" + mode,
			"TAILSPLIT_STARTUP_STATE=" + stateDir, "TAILSPLIT_STARTUP_RULES=" + rulePath,
			"TAILSPLIT_STARTUP_BASE=" + filepath.Dir(configPath)},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = controller.Close() })
	return controller
}

func startupHelperConfigPath(t *testing.T, controller *Controller) string {
	t.Helper()
	controller.mu.Lock()
	defer controller.mu.Unlock()
	if controller.command == nil {
		t.Fatal("startup helper has already exited")
	}
	index := slices.Index(controller.command.Args, "-c")
	if index < 0 || index+1 >= len(controller.command.Args) {
		t.Fatal("startup helper has no configuration argument", controller.command.Args)
	}
	return controller.command.Args[index+1]
}

func startupHelperConfigBase(t *testing.T, controller *Controller) string {
	t.Helper()
	controller.mu.Lock()
	defer controller.mu.Unlock()
	if controller.command == nil {
		t.Fatal("startup helper has already exited")
	}
	index := slices.Index(controller.command.Args, "--config-base")
	if index < 0 {
		return ""
	}
	if index+1 >= len(controller.command.Args) {
		t.Fatal("startup helper has no configuration base argument", controller.command.Args)
	}
	return controller.command.Args[index+1]
}

func waitStartupSnapshotRemoved(t *testing.T, path string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, err := os.Lstat(path); errors.Is(err, os.ErrNotExist) {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("startup snapshot survived helper cleanup", path)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestNativeStartupFollowsConfigSymlinkWithoutRelaxingEditing(t *testing.T) {
	for _, mode := range []string{"traffic", "login", "discovery"} {
		t.Run(mode, func(t *testing.T) {
			base, targetDir := t.TempDir(), t.TempDir()
			configPath, target := filepath.Join(base, "config.yaml"), filepath.Join(targetDir, "managed.yaml")
			original := bytes.Replace(connectionSnapshotConfig, []byte("source: ''"), []byte("source: ./rules.txt"), 1)
			if err := os.WriteFile(target, original, 0o600); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(base, "rules.txt"), nil, 0o600); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(target, configPath); err != nil {
				t.Skip("config symlinks unavailable", err)
			}
			controller := startupTestController(t, configPath, filepath.Join(base, "state"), filepath.Join(base, "rules.txt"), mode)
			start, state := controller.Start, "running"
			if mode == "login" {
				start, state = controller.StartLogin, "authenticated"
			} else if mode == "discovery" {
				start, state = controller.RefreshExitNodes, "discovering"
			}
			if err := start(); err != nil {
				t.Fatal("trusted startup rejected a readable config symlink", err)
			}
			snapshot := waitState(t, controller, state)
			helperPath := startupHelperConfigPath(t, controller)
			if base := startupHelperConfigBase(t, controller); base != "" {
				t.Fatal("sibling snapshot or direct source received a relocated configuration base", base)
			}
			revision := fmt.Sprintf("%x", sha256.Sum256(original))
			if mode == "traffic" {
				data, helperRevision, err := ReadConfigDocument(helperPath)
				if err != nil || !bytes.Equal(data, original) || helperRevision != revision || snapshot.ActiveRevision != revision || helperPath == configPath {
					t.Fatal("symlink startup lost its immutable source document/revision", snapshot, err)
				}
				if err := privatefs.CheckFile(helperPath); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(target, append(bytes.Clone(original), "# external edit\n"...), 0o600); err != nil {
					t.Fatal(err)
				}
				data, _, err = ReadConfigDocument(helperPath)
				if err != nil || !bytes.Equal(data, original) || controller.Snapshot().ActiveRevision != revision {
					t.Fatal("editing the symlink target changed the running policy", err)
				}
			} else if helperPath != configPath || snapshot.ActiveRevision != "" {
				t.Fatal("non-traffic startup changed source or exposed a traffic revision", snapshot)
			}
			if _, _, err := ReadConfigDocument(configPath); err == nil {
				t.Fatal("web editing reader accepted a config symlink")
			}
			if err := SaveConfigDocument(configPath, revision, original); err == nil {
				t.Fatal("web editing followed a config symlink")
			}
			if err := controller.Stop(); err != nil {
				t.Fatal(err)
			}
			if err := controller.StartRevision(revision); err == nil {
				t.Fatal("revision-based web admission followed a symlink")
			}
			if mode == "traffic" {
				waitStartupSnapshotRemoved(t, helperPath)
			}
			if info, err := os.Lstat(configPath); err != nil || info.Mode()&os.ModeSymlink == 0 {
				t.Fatal("startup or cleanup replaced the config symlink", err)
			}
		})
	}
}

func readOnlyStartupConfig(t *testing.T) (configPath string, data []byte, stateDir, rulePath string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("read-only directory fixture uses POSIX permissions")
	}
	base := t.TempDir()
	source := filepath.Join(base, "read-only")
	if err := os.Mkdir(source, 0o700); err != nil {
		t.Fatal(err)
	}
	configPath, rulePath, stateDir = filepath.Join(source, "config.yaml"), filepath.Join(source, "rules.txt"), filepath.Join(base, "state")
	data = bytes.Replace(connectionSnapshotConfig, []byte("state_dir: ./state"), []byte("state_dir: ../state"), 1)
	data = bytes.Replace(data, []byte("source: ''"), []byte("source: ./rules.txt"), 1)
	data = append(data, "# retain this exact source revision\n"...)
	if err := os.WriteFile(configPath, data, 0o400); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(rulePath, nil, 0o400); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(source, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(source, 0o700) })
	probe := filepath.Join(source, "write-probe")
	if err := os.WriteFile(probe, nil, 0o600); err == nil {
		_ = os.Remove(probe)
		t.Skip("process can write to a read-only directory")
	} else if !errors.Is(err, os.ErrPermission) {
		t.Fatal(err)
	}
	return configPath, data, stateDir, rulePath
}

func TestNativeStartupUsesPrivateFallbackSnapshotWithReadOnlyConfigDirectory(t *testing.T) {
	for _, checked := range []bool{false, true} {
		t.Run(fmt.Sprintf("revision-checked=%v", checked), func(t *testing.T) {
			configPath, original, stateDir, rulePath := readOnlyStartupConfig(t)
			controller := startupTestController(t, configPath, stateDir, rulePath, "traffic")
			revision := fmt.Sprintf("%x", sha256.Sum256(original))
			if err := controller.StartRevision("stale"); !errors.Is(err, ErrConfigChanged) {
				t.Fatal("stale admission reached fallback startup", err)
			}
			if _, err := os.Stat(stateDir); !errors.Is(err, os.ErrNotExist) {
				t.Fatal("stale admission created snapshot state", err)
			}
			var err error
			if checked {
				err = controller.StartRevision(revision)
			} else {
				err = controller.Start()
			}
			if err != nil {
				t.Fatal("read-only config directory blocked startup", err)
			}
			if snapshot := waitState(t, controller, "running"); snapshot.ActiveRevision != revision {
				t.Fatal("relocated snapshot lost the exact source revision", snapshot)
			}
			helperPath := startupHelperConfigPath(t, controller)
			fallbackPath, err := connectionSnapshotFallbackPath(controller.options.LogPath)
			if err != nil || filepath.Dir(helperPath) != filepath.Dir(fallbackPath) {
				t.Fatal("snapshot was not placed in the stable private fallback root", helperPath, err)
			}
			if base := startupHelperConfigBase(t, controller); base != filepath.Dir(configPath) {
				t.Fatal("relocated helper lost the original configuration base", base)
			}
			if err := privatefs.CheckFile(helperPath); err != nil {
				t.Fatal("relocated snapshot is not private", err)
			}
			for _, directory := range []string{stateDir, filepath.Dir(helperPath), filepath.Join(filepath.Dir(helperPath), ".tailsplit-connection-lock")} {
				info, err := os.Lstat(directory)
				if err != nil || !info.IsDir() || info.Mode().Perm() != 0o700 {
					t.Fatal("snapshot state/lock directory is not private", directory, err)
				}
			}
			want, wantErr := tailsplit.LoadDraft(configPath)
			got, gotErr := tailsplit.LoadDraftWithBase(helperPath, filepath.Dir(configPath))
			wantYAML, wantEncodeErr := yaml.Marshal(want)
			gotYAML, gotEncodeErr := yaml.Marshal(got)
			if err := errors.Join(wantErr, gotErr, wantEncodeErr, gotEncodeErr); err != nil || !bytes.Equal(gotYAML, wantYAML) {
				t.Fatal("relocating the snapshot changed normalized configuration", err)
			}
			captured, helperRevision, err := ReadConfigDocument(helperPath)
			if err != nil || !bytes.Equal(captured, original) || helperRevision != revision {
				t.Fatal("relocated snapshot changed captured bytes or source revision", err)
			}
			if unlock, err := tailsplit.LockSavedState(stateDir); err == nil {
				_ = unlock()
				t.Fatal("helper did not retain the saved-state lease")
			}
			if err := cleanupStartupConnectionSnapshots(configPath, controller.options.LogPath); err != nil {
				t.Fatal(err)
			}
			if _, err := os.Stat(helperPath); err != nil {
				t.Fatal("cleanup removed a live relocated snapshot", err)
			}
			if err := os.Chmod(configPath, 0o600); err != nil {
				t.Fatal(err)
			}
			changed := append(bytes.Clone(original), "# changed after dispatch\n"...)
			if err := os.WriteFile(configPath, changed, 0o600); err != nil {
				t.Fatal(err)
			}
			current, _, err := ReadConfigDocument(helperPath)
			if err != nil || !bytes.Equal(current, captured) || controller.Snapshot().ActiveRevision != revision {
				t.Fatal("source edit changed relocated helper bytes or revision", err)
			}
			if err := controller.Stop(); err != nil {
				t.Fatal(err)
			}
			waitStartupSnapshotRemoved(t, helperPath)
			if snapshot := controller.Snapshot(); snapshot.Active || snapshot.ActiveRevision != "" {
				t.Fatal("stopped helper retained an active revision", snapshot)
			}
			unlock, err := tailsplit.LockSavedState(stateDir)
			if err != nil {
				t.Fatal("stopped helper retained its saved-state lease", err)
			}
			_ = unlock()
			entries, err := os.ReadDir(filepath.Dir(configPath))
			if err != nil || len(entries) != 2 {
				t.Fatal("startup wrote snapshot metadata beside a read-only source", entries, err)
			}
		})
	}
}

func TestNativeStartupFallbackPreservesCompactCapturedYAML(t *testing.T) {
	configPath, original, stateDir, rulePath := readOnlyStartupConfig(t)
	domain := strings.Repeat(strings.Repeat("a", 50)+".", 4) + "example.com"
	original = append(original, []byte("  proxy_domains: [&domain "+domain+strings.Repeat(", *domain", 6000)+"]\n")...)
	if len(original) > MaxConfigBytes {
		t.Fatal("compact source exceeds the existing input bound")
	}
	if err := os.Chmod(configPath, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(configPath, original, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(configPath, 0o400); err != nil {
		t.Fatal(err)
	}
	config, err := tailsplit.LoadDraft(configPath)
	if err != nil {
		t.Fatal("compact aliased source is not valid", err)
	}
	expanded, err := yaml.Marshal(config)
	if err != nil || len(expanded) <= MaxConfigBytes {
		t.Fatal("fixture does not exercise normalization expansion beyond the byte bound", len(expanded), err)
	}
	controller := startupTestController(t, configPath, stateDir, rulePath, "traffic")
	revision := fmt.Sprintf("%x", sha256.Sum256(original))
	if err := controller.StartRevision(revision); err != nil {
		t.Fatal("valid compact source could not start through the read-only fallback", err)
	}
	if snapshot := waitState(t, controller, "running"); snapshot.ActiveRevision != revision {
		t.Fatal("compact fallback source lost its exact active revision", snapshot)
	}
	helperPath := startupHelperConfigPath(t, controller)
	captured, helperRevision, err := ReadConfigDocument(helperPath)
	if err != nil || !bytes.Equal(captured, original) || helperRevision != revision {
		t.Fatal("fallback expanded or rewrote compact captured YAML/aliases", len(captured), err)
	}
	if err := controller.Stop(); err != nil {
		t.Fatal(err)
	}
	waitStartupSnapshotRemoved(t, helperPath)
}

func TestStartupFallbackKeepsOriginalByteLimit(t *testing.T) {
	configPath, data, _, _ := readOnlyStartupConfig(t)
	data = append(data, []byte("#"+strings.Repeat("x", MaxConfigBytes-len(data)-2)+"\n")...)
	if len(data) != MaxConfigBytes {
		t.Fatal("fixture does not exactly fill the original byte budget", len(data))
	}
	logPath := filepath.Join(t.TempDir(), "logs", "core.log")
	path, base, cleanup, err := createConnectionSnapshot(configPath, logPath, data)
	if err != nil {
		t.Fatal("valid source at the byte limit was rejected", err)
	}
	defer cleanup()
	if _, err := tailsplit.LoadDraftWithBase(path, base); err != nil {
		t.Fatal("helper could not load an exact-limit captured snapshot", err)
	}
	if captured, _, err := ReadConfigDocument(path); err != nil || !bytes.Equal(captured, data) {
		t.Fatal("exact-limit captured source was expanded or truncated", len(captured), err)
	}
	if _, _, extraCleanup, err := createConnectionSnapshot(configPath, logPath, append(bytes.Clone(data), '\n')); err == nil || extraCleanup != nil {
		if extraCleanup != nil {
			extraCleanup()
		}
		t.Fatal("snapshot accepted bytes beyond the existing input/helper limit", err)
	}
}

func TestNativeStartupRejectsUnavailablePrivateFallbackRoot(t *testing.T) {
	for _, kind := range []string{"public-directory", "symlink", "file"} {
		t.Run(kind, func(t *testing.T) {
			configPath, _, stateDir, rulePath := readOnlyStartupConfig(t)
			controller := startupTestController(t, configPath, stateDir, rulePath, "traffic")
			fallbackPath, err := connectionSnapshotFallbackPath(controller.options.LogPath)
			if err != nil {
				t.Fatal(err)
			}
			directory := filepath.Dir(fallbackPath)
			switch kind {
			case "public-directory":
				if err := os.Mkdir(directory, 0o700); err != nil {
					t.Fatal(err)
				}
				if err := os.Chmod(directory, 0o755); err != nil {
					t.Fatal(err)
				}
			case "symlink":
				if err := privatefs.EnsureDir(stateDir); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(stateDir, directory); err != nil {
					t.Skip("symlinks unavailable", err)
				}
			case "file":
				if err := os.WriteFile(directory, []byte("retain this foreign file\n"), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			if err := controller.Start(); err == nil {
				t.Fatal("startup adopted an unsafe/unavailable private fallback root")
			}
			if snapshot := controller.Snapshot(); snapshot.Active || snapshot.ActiveRevision != "" {
				t.Fatal("rejected fallback exposed an active connection", snapshot)
			}
			if paths, err := filepath.Glob(filepath.Join(stateDir, connectionSnapshotPrefix+"*")); err != nil || len(paths) != 0 {
				t.Fatal("startup silently published an unindexed state snapshot", paths, err)
			}
			if paths, err := filepath.Glob(filepath.Join(stateDir, ".tailsplit-connection-snapshots", connectionSnapshotPrefix+"*")); err != nil || len(paths) != 0 {
				t.Fatal("startup silently used the mutable state directory as fallback", paths, err)
			}
		})
	}
}

func TestReadOnlyStartupSnapshotCleansRejectedAdmissionAndFailedLaunch(t *testing.T) {
	for _, stale := range []bool{false, true} {
		t.Run(fmt.Sprintf("stale=%v", stale), func(t *testing.T) {
			configPath, original, stateDir, rulePath := readOnlyStartupConfig(t)
			controller := startupTestController(t, configPath, stateDir, rulePath, "traffic")
			revision := fmt.Sprintf("%x", sha256.Sum256(original))
			if stale {
				if err := os.Chmod(configPath, 0o600); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(configPath, append(bytes.Clone(original), "# edit before admission\n"...), 0o600); err != nil {
					t.Fatal(err)
				}
			} else {
				controller.options.Helper = filepath.Join(t.TempDir(), "missing-helper")
			}
			controller.mu.Lock()
			err := controller.startConnectionLocked("proxy", "", original, revision)
			controller.mu.Unlock()
			if err == nil || stale && !errors.Is(err, ErrConfigChanged) {
				t.Fatal("rejected startup was admitted", err)
			}
			fallbackPath, pathErr := connectionSnapshotFallbackPath(controller.options.LogPath)
			if pathErr != nil {
				t.Fatal(pathErr)
			}
			paths, globErr := filepath.Glob(filepath.Join(filepath.Dir(fallbackPath), connectionSnapshotPrefix+"*"))
			if globErr != nil || len(paths) != 0 || controller.Snapshot().Active || controller.Snapshot().ActiveRevision != "" {
				t.Fatal("rejected startup leaked an immutable snapshot or active revision", paths, globErr)
			}
		})
	}
}

func TestStartupCleanupPreservesSelectedFallbackSnapshot(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "logs", "core.log")
	fallbackPath, err := connectionSnapshotFallbackPath(logPath)
	if err != nil {
		t.Fatal(err)
	}
	snapshotDir := filepath.Dir(fallbackPath)
	if err := privatefs.EnsureDir(snapshotDir); err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(snapshotDir, fmt.Sprintf("%s%d-%s", connectionSnapshotPrefix, os.Getpid(), rand.Text()))
	data := []byte("invalid YAML source must still be protected: [\n")
	file, err := privatefs.CreateFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.Write(data); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	if err := cleanupStartupConnectionSnapshots(configPath, logPath); err != nil {
		t.Fatal(err)
	}
	if current, err := os.ReadFile(configPath); err != nil || !bytes.Equal(current, data) {
		t.Fatal("cleanup adopted the configured source as an orphaned fallback snapshot", err)
	}
}

func TestStartupReclaimsFallbackSnapshotsIndependentOfConfig(t *testing.T) {
	for _, change := range []string{"state-dir-changed", "invalid-yaml", "source-missing"} {
		t.Run(change, func(t *testing.T) {
			configPath, original, _, _ := readOnlyStartupConfig(t)
			logPath := filepath.Join(t.TempDir(), "logs", "core.log")
			fallbackPath, err := connectionSnapshotFallbackPath(logPath)
			if err != nil {
				t.Fatal(err)
			}
			directory := filepath.Dir(fallbackPath)
			if err := privatefs.EnsureDir(directory); err != nil {
				t.Fatal(err)
			}
			executable, err := os.Executable()
			if err != nil {
				t.Fatal(err)
			}
			owner := exec.Command(executable, "-test.run=^TestConnectionSnapshotOwnerProcess$")
			owner.Env = append(os.Environ(), "TAILSPLIT_SNAPSHOT_OWNER_DIR="+directory)
			var exit *exec.ExitError
			if err := owner.Run(); !errors.As(err, &exit) || exit.ExitCode() != 73 {
				t.Fatal("snapshot creator did not exit abruptly", err)
			}
			orphans, err := filepath.Glob(filepath.Join(directory, connectionSnapshotPrefix+"*"))
			if err != nil || len(orphans) != 1 {
				t.Fatal("snapshot creator did not leave an orphan", orphans, err)
			}
			create := func(path string) {
				t.Helper()
				file, err := privatefs.CreateFile(path)
				if err != nil {
					t.Fatal(err)
				}
				if _, err := file.WriteString("private fixture\n"); err != nil {
					t.Fatal(err)
				}
				if err := file.Close(); err != nil {
					t.Fatal(err)
				}
			}
			live := filepath.Join(directory, fmt.Sprintf("%s%d-%s", connectionSnapshotPrefix, os.Getpid(), rand.Text()))
			create(live)
			lease, err := lockConnectionSnapshot(live)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = unlockConnectionSnapshot(lease) })
			unrelated := filepath.Join(directory, "saved-config.yaml")
			create(unrelated)
			outside := filepath.Join(filepath.Dir(directory), fmt.Sprintf("%s%d-%s", connectionSnapshotPrefix, os.Getpid(), rand.Text()))
			create(outside)
			link := filepath.Join(directory, fmt.Sprintf("%s%d-%s", connectionSnapshotPrefix, os.Getpid(), rand.Text()))
			linked := os.Symlink(unrelated, link) == nil
			if err := os.Chmod(configPath, 0o600); err != nil {
				t.Fatal(err)
			}
			if change == "source-missing" {
				if err := os.Chmod(filepath.Dir(configPath), 0o700); err != nil {
					t.Fatal(err)
				}
				if err := os.Remove(configPath); err != nil {
					t.Fatal(err)
				}
			} else {
				updated := bytes.Replace(original, []byte("state_dir: ../state"), []byte("state_dir: ../replacement-state"), 1)
				if change == "invalid-yaml" {
					updated = []byte("invalid YAML: [\n")
				}
				if err := os.WriteFile(configPath, updated, 0o600); err != nil {
					t.Fatal(err)
				}
			}
			controller, err := New(Options{Helper: executable, ConfigPath: configPath, LogPath: logPath})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = controller.Close() })
			if _, err := os.Lstat(orphans[0]); !errors.Is(err, os.ErrNotExist) {
				t.Fatal("next launch lost orphan discovery after source configuration changed", err)
			}
			for _, path := range []string{live, unrelated, outside} {
				if _, err := os.Lstat(path); err != nil {
					t.Fatal("fallback cleanup removed a live or unrelated file", path, err)
				}
			}
			if linked {
				if info, err := os.Lstat(link); err != nil || info.Mode()&os.ModeSymlink == 0 {
					t.Fatal("fallback cleanup followed or removed an unrelated symlink", err)
				}
			}
		})
	}
}
