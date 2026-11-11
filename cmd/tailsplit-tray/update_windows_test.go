//go:build windows

package main

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/idy/tailsplit/internal/appupdate"
	"github.com/tailscale/go-winio"
	"golang.org/x/sys/windows"
)

// The test binary doubles as the fake Setup EXE. ShellExecuteEx passes this
// process's environment to the child, which selects the fixture branch.
const updateInstallerFixture = "TAILSPLIT_TEST_UPDATE_INSTALLER"
const desktopQuitFixture = "TAILSPLIT_TEST_DESKTOP_QUIT"

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
	if pipe := os.Getenv(desktopQuitFixture); pipe != "" {
		os.Exit(runDesktopQuitFixture(pipe))
	}
	os.Exit(m.Run())
}

func runDesktopQuitFixture(pipe string) int {
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return 3
	}
	listener, err := winio.ListenPipe(pipe, &winio.PipeConfig{SecurityDescriptor: desktopPipeDescriptor(user.User.Sid.String())})
	if err != nil {
		return 3
	}
	defer listener.Close()
	fmt.Println("ready")
	connection, err := listener.Accept()
	if err != nil {
		return 3
	}
	defer connection.Close()
	_ = connection.SetDeadline(time.Now().Add(5 * time.Second))
	command, err := bufio.NewReader(connection).ReadString('\n')
	if err != nil || command != "quit-update\n" {
		return 3
	}
	if _, err := io.WriteString(connection, os.Getenv(desktopQuitFixture+"_RESPONSE")); err != nil {
		return 3
	}
	return 0
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
			want := "/SP-\n/NORESTART\n/RestoreConnection=1\n/UserSID=" + user.User.Sid.String()
			if err != nil || string(arguments) != want {
				t.Fatalf("installer arguments = %q (%v), want %q", arguments, err, want)
			}
		})
	}
}

func TestWindowsDesktopUpdateQuitStateProtocol(t *testing.T) {
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name, response string
		reconnect      bool
		failure        bool
	}{
		{"connected", "ok-start\n", true, false},
		{"disconnected", "ok\n", false, false},
		{"cleanup failed", "failed\n", false, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			pipe := `\\.\pipe\tailsplit-update-quit-` + filepath.Base(t.TempDir())
			command := exec.CommandContext(ctx, executable)
			command.Env = append(os.Environ(), desktopQuitFixture+"="+pipe, desktopQuitFixture+"_RESPONSE="+test.response)
			output, err := command.StdoutPipe()
			if err != nil {
				t.Fatal(err)
			}
			if err := command.Start(); err != nil {
				t.Fatal(err)
			}
			defer func() { _ = command.Process.Kill(); _ = command.Wait() }()
			if ready, err := bufio.NewReader(output).ReadString('\n'); err != nil || ready != "ready\n" {
				t.Fatal("desktop fixture did not listen:", ready, err)
			}
			reconnect, err := desktopRequest(ctx, pipe, "quit-update", executable, user.User.Sid.String())
			if (err != nil) != test.failure || reconnect != test.reconnect {
				t.Fatalf("reconnect=%t error=%v", reconnect, err)
			}
			if err := command.Wait(); err != nil {
				t.Fatal("desktop fixture failed:", err)
			}
		})
	}
}
