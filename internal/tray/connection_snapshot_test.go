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
	// Simulate losing the owner without any deferred snapshot cleanup.
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
	if _, err := os.Lstat(orphans[0]); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("orphaned configuration survived the next launch", err)
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

func TestDesktopConnectionSnapshotOwnerParsingIsConservative(t *testing.T) {
	valid := connectionSnapshotPrefix + strconv.Itoa(os.Getpid()) + "-" + rand.Text()
	pid, ok := connectionSnapshotOwner(valid)
	if !ok || pid != os.Getpid() || snapshotProcessExited(pid) {
		t.Fatal("live owner was not retained")
	}
	for _, name := range []string{"config.yaml", ".tailsplit-config-" + rand.Text(), connectionSnapshotPrefix + "-1-" + rand.Text(), connectionSnapshotPrefix + "0-" + rand.Text(), connectionSnapshotPrefix + "2147483648-" + rand.Text(), connectionSnapshotPrefix + "123-short", valid + ".yaml"} {
		if _, ok := connectionSnapshotOwner(name); ok {
			t.Fatal("accepted an unrelated snapshot name", name)
		}
	}
}
