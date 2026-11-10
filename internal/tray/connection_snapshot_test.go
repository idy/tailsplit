package tray

import (
	"crypto/rand"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/idy/tailsplit/internal/privatefs"
)

func TestConnectionSnapshotOwnerProcess(t *testing.T) {
	if path := os.Getenv("TAILSPLIT_SNAPSHOT_READER"); path != "" {
		if _, err := os.ReadFile(path); err != nil {
			os.Exit(78)
		}
		os.Exit(0)
	}
	directory := os.Getenv("TAILSPLIT_SNAPSHOT_OWNER_DIR")
	if directory == "" {
		return
	}
	path := filepath.Join(directory, fmt.Sprintf("%s%d-%s", connectionSnapshotPrefix, os.Getpid(), rand.Text()))
	file, err := privatefs.CreateFile(path)
	if err != nil {
		os.Exit(74)
	}
	if _, err := file.WriteString("subscription: https://fixture.invalid/rules?token=private-fixture\n"); err != nil {
		os.Exit(75)
	}
	if file.Close() != nil {
		os.Exit(76)
	}
	lease, err := lockConnectionSnapshot(path)
	if err != nil {
		os.Exit(77)
	}
	defer unlockConnectionSnapshot(lease)
	// Abrupt exit bypasses cleanup, but the OS must release the lifetime lock.
	os.Exit(73)
}

func TestDesktopConnectionSnapshotsReclaimedAfterOwnerExits(t *testing.T) {
	directory := t.TempDir()
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	command := exec.Command(executable, "-test.run=^TestConnectionSnapshotOwnerProcess$")
	command.Env = append(os.Environ(), "TAILSPLIT_SNAPSHOT_OWNER_DIR="+directory)
	var exit *exec.ExitError
	if err := command.Run(); !errors.As(err, &exit) || exit.ExitCode() != 73 {
		t.Fatal("owner did not exit abruptly as expected", err)
	}
	deadPID := command.Process.Pid
	orphans, err := filepath.Glob(filepath.Join(directory, connectionSnapshotPrefix+strconv.Itoa(deadPID)+"-*"))
	if err != nil || len(orphans) != 1 {
		t.Fatal("owner did not leave its snapshot", orphans, err)
	}
	create := func(name string) string {
		t.Helper()
		path := filepath.Join(directory, name)
		file, err := privatefs.CreateFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := file.WriteString("fixture retained\n"); err != nil {
			t.Fatal(err)
		}
		if err := file.Close(); err != nil {
			t.Fatal(err)
		}
		return path
	}
	live := create(fmt.Sprintf("%s%d-%s", connectionSnapshotPrefix, os.Getpid(), rand.Text()))
	lease, err := lockConnectionSnapshot(live)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = unlockConnectionSnapshot(lease) })
	// The bytes remain readable while held, including Windows mandatory locks.
	if _, err := os.ReadFile(live); err != nil {
		t.Fatal("lease blocked the helper's config read", err)
	}
	reader := exec.Command(executable, "-test.run=^TestConnectionSnapshotOwnerProcess$")
	reader.Env = append(os.Environ(), "TAILSPLIT_SNAPSHOT_READER="+live)
	if err := reader.Run(); err != nil {
		t.Fatal("lease blocked another process reading configuration", err)
	}
	// A recycled PID is simulated by an unlocked orphan naming this live process.
	recycled := create(fmt.Sprintf("%s%d-%s", connectionSnapshotPrefix, os.Getpid(), rand.Text()))
	validation := create(".tailsplit-config-" + rand.Text())
	ordinary := create("saved-config.yaml")
	malformed := create(connectionSnapshotPrefix + "0-" + rand.Text())
	symlink := filepath.Join(directory, fmt.Sprintf("%s%d-%s", connectionSnapshotPrefix, deadPID, rand.Text()))
	linked := os.Symlink(ordinary, symlink) == nil
	controller, err := New(Options{Helper: executable, ConfigPath: filepath.Join(directory, "config.yaml"), LogPath: filepath.Join(directory, "logs", "core.log")})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = controller.Close() })
	for _, path := range []string{orphans[0], recycled} {
		if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
			t.Fatal("orphaned configuration survived the next launch", path, err)
		}
	}
	for _, path := range []string{live, validation, ordinary, malformed} {
		if _, err := os.Lstat(path); err != nil {
			t.Fatal("cleanup removed a live or unrelated file", path, err)
		}
	}
	if linked {
		if info, err := os.Lstat(symlink); err != nil || info.Mode()&os.ModeSymlink == 0 {
			t.Fatal("cleanup followed or removed a link", err)
		}
	}
	// Even a user-selected source in the reserved namespace is never deleted.
	selected := create(fmt.Sprintf("%s%d-%s", connectionSnapshotPrefix, deadPID, rand.Text()))
	if err := cleanupConnectionSnapshots(selected); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(selected); err != nil {
		t.Fatal("cleanup removed the configured source", err)
	}
}

func TestDesktopConnectionSnapshotPublicationExcludesCleanup(t *testing.T) {
	directory := t.TempDir()
	configPath := filepath.Join(directory, "config.yaml")
	unlock, err := lockConnectionSnapshotDirectory(configPath)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(directory, fmt.Sprintf("%s%d-%s", connectionSnapshotPrefix, os.Getpid(), rand.Text()))
	file, err := privatefs.CreateFile(path)
	if err != nil {
		_ = unlock()
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		_ = unlock()
		t.Fatal(err)
	}
	// This file is visible, but its lifetime lock has not yet been acquired.
	err = cleanupConnectionSnapshots(configPath)
	_, statErr := os.Stat(path)
	_ = unlock()
	if err == nil || statErr != nil {
		t.Fatal("cleanup raced a snapshot being published", err, statErr)
	}
	if err := cleanupConnectionSnapshots(configPath); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("unlocked orphan was not reclaimed", err)
	}
}

func TestDesktopConnectionSnapshotOwnerParsingIsConservative(t *testing.T) {
	valid := connectionSnapshotPrefix + strconv.Itoa(os.Getpid()) + "-" + rand.Text()
	pid, ok := connectionSnapshotOwner(valid)
	if !ok || pid != os.Getpid() {
		t.Fatal("live owner was not retained")
	}
	for _, name := range []string{"config.yaml", ".tailsplit-config-" + rand.Text(), connectionSnapshotPrefix + "-1-" + rand.Text(), connectionSnapshotPrefix + "0-" + rand.Text(), connectionSnapshotPrefix + "4294967296-" + rand.Text(), connectionSnapshotPrefix + "123-short", valid + ".yaml"} {
		if _, ok := connectionSnapshotOwner(name); ok {
			t.Fatal("accepted an unrelated snapshot name", name)
		}
	}
}
