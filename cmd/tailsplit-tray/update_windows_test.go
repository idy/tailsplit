//go:build windows

package main

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/idy/tailsplit/internal/appupdate"
	"golang.org/x/sys/windows"
)

// The test binary doubles as the fake Setup EXE. ShellExecuteEx passes this
// process's environment to the child, which selects the fixture branch.
const updateInstallerFixture = "TAILSPLIT_TEST_UPDATE_INSTALLER"

func TestMain(m *testing.M) {
	if record := os.Getenv(updateInstallerFixture); record != "" {
		os.Exit(fakeUpdateInstaller(record))
	}
	os.Exit(m.Run())
}

func fakeUpdateInstaller(record string) int {
	if err := os.WriteFile(record, []byte(strings.Join(os.Args[1:], "\n")), 0o600); err != nil {
		return 3
	}
	// Stay alive while the detached cleanup starts: it must not remove the
	// verified download until Setup has exited.
	time.Sleep(2 * time.Second)
	if _, err := os.Stat(os.Getenv(updateInstallerFixture + "_MARKER")); err != nil {
		return 4
	}
	code, err := strconv.Atoi(os.Getenv(updateInstallerFixture + "_EXIT"))
	if err != nil {
		return 3
	}
	return code
}

func TestWindowsUpdateInstallerHandoff(t *testing.T) {
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name string
		exit int
	}{{"installed", 0}, {"installer failed", 5}} {
		t.Run(test.name, func(t *testing.T) {
			// Spaces and a quote exercise the cleanup command's path literal.
			directory := filepath.Join(t.TempDir(), "user's update")
			if err := os.Mkdir(directory, 0o700); err != nil {
				t.Fatal(err)
			}
			marker := filepath.Join(directory, "package.exe")
			if err := os.WriteFile(marker, []byte("verified fixture"), 0o600); err != nil {
				t.Fatal(err)
			}
			record := filepath.Join(t.TempDir(), "arguments.txt")
			t.Setenv(updateInstallerFixture, record)
			t.Setenv(updateInstallerFixture+"_MARKER", marker)
			t.Setenv(updateInstallerFixture+"_EXIT", strconv.Itoa(test.exit))
			err := launchWindowsUpdate(appupdate.Package{Path: executable, Directory: directory, Version: "0.6.3"})
			if test.exit == 0 && err != nil {
				t.Fatal("successful installer was reported as failed:", err)
			}
			if test.exit != 0 && (err == nil || !strings.Contains(err.Error(), "exited with code 5")) {
				t.Fatal("installer failure was not reported:", err)
			}
			arguments, err := os.ReadFile(record)
			want := "/SP-\n/NORESTART\n/UserSID=" + user.User.Sid.String()
			if err != nil || string(arguments) != want {
				t.Fatalf("installer arguments = %q (%v), want %q", arguments, err, want)
			}
			// Setup quits this app before replacing it, so the detached process,
			// not a deferred cleanup, owns removing the download afterwards.
			deadline := time.Now().Add(time.Minute)
			for {
				if _, err := os.Stat(directory); os.IsNotExist(err) {
					break
				}
				if time.Now().After(deadline) {
					t.Fatal("the verified download was not removed after the installer exited")
				}
				time.Sleep(100 * time.Millisecond)
			}
		})
	}
}
