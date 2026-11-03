//go:build windows

package main

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/idy/tailsplit/internal/appupdate"
	"golang.org/x/sys/windows"
)

// The test binary doubles as the fake Setup EXE. ShellExecuteEx passes this
// process's environment to the child, which selects the fixture branch.
const updateInstallerFixture = "TAILSPLIT_TEST_UPDATE_INSTALLER"

func TestMain(m *testing.M) {
	if record := os.Getenv(updateInstallerFixture); record != "" {
		if err := os.WriteFile(record, []byte(strings.Join(os.Args[1:], "\n")), 0o600); err != nil {
			os.Exit(3)
		}
		code, err := strconv.Atoi(os.Getenv(updateInstallerFixture + "_EXIT"))
		if err != nil {
			os.Exit(3)
		}
		os.Exit(code)
	}
	os.Exit(m.Run())
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
			record := filepath.Join(t.TempDir(), "arguments.txt")
			t.Setenv(updateInstallerFixture, record)
			t.Setenv(updateInstallerFixture+"_EXIT", strconv.Itoa(test.exit))
			err := launchWindowsUpdate(appupdate.Package{Path: executable, Directory: t.TempDir(), Version: "0.6.3"})
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
		})
	}
}
