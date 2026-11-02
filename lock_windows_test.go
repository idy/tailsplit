//go:build windows

package tailsplit

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestWindowsStateLockAcrossProcesses(t *testing.T) {
	directory := filepath.Join(t.TempDir(), "state")
	if err := privateState(directory); err != nil {
		t.Fatal(err)
	}
	release, err := lockState(directory)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	command := lockHelper(directory, "try")
	output, err := command.CombinedOutput()
	var exited *exec.ExitError
	if !errors.As(err, &exited) || exited.ExitCode() != 23 || !strings.Contains(string(output), "already in use") {
		t.Fatalf("second process did not report lock contention: %s, %v", output, err)
	}
	if err := release(); err != nil {
		t.Fatal(err)
	}
	if output, err := lockHelper(directory, "try").CombinedOutput(); err != nil {
		t.Fatalf("lock survived release: %s, %v", output, err)
	}
}

func TestWindowsStateLockReleasedWhenProcessExits(t *testing.T) {
	directory := filepath.Join(t.TempDir(), "state")
	if err := privateState(directory); err != nil {
		t.Fatal(err)
	}
	if output, err := lockHelper(directory, "exit").CombinedOutput(); err != nil {
		t.Fatalf("helper could not acquire lock: %s, %v", output, err)
	}
	release, err := lockState(directory)
	if err != nil {
		t.Fatal("process exit did not release the lock:", err)
	}
	if err := release(); err != nil {
		t.Fatal(err)
	}
}

func TestWindowsStateLockHelper(t *testing.T) {
	mode := os.Getenv("TAILSPLIT_LOCK_TEST_HELPER")
	if mode == "" {
		return
	}
	release, err := lockState(os.Getenv("TAILSPLIT_LOCK_TEST_DIRECTORY"))
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(23)
	}
	if mode == "exit" {
		// Deliberately do not unlock: the OS must release a dead process's lock.
		os.Exit(0)
	}
	if err := release(); err != nil {
		t.Fatal(err)
	}
}

func lockHelper(directory, mode string) *exec.Cmd {
	command := exec.Command(os.Args[0], "-test.run=^TestWindowsStateLockHelper$")
	command.Env = append(os.Environ(), "TAILSPLIT_LOCK_TEST_HELPER="+mode, "TAILSPLIT_LOCK_TEST_DIRECTORY="+directory)
	return command
}
