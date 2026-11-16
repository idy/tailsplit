//go:build darwin

package appservice

import (
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Execute the actual postinstall with only root/launchd operations replaced.
// Syntax-only sh checks accept an unterminated heredoc, so they cannot prove
// that a package displaying "success" actually registered the service.
func TestInstallerProducesAValidJobAndBootstrapsIt(t *testing.T) {
	directory := t.TempDir()
	if output, err := runInstallerScript(t, directory, "postinstall", "exit 0\n"); err != nil {
		t.Fatalf("postinstall failed: %v: %s", err, output)
	}
	plist := filepath.Join(directory, "Library", "LaunchDaemons", Label+".plist")
	if output, err := exec.Command("/usr/bin/plutil", "-lint", plist).CombinedOutput(); err != nil {
		t.Fatalf("Installer would claim success with an invalid launchd job: %v: %s", err, output)
	}
	info, err := os.Stat(plist)
	if err != nil || info.Mode().Perm() != 0o644 {
		t.Fatal("the installed job did not receive its final permissions")
	}
	recorded, err := os.ReadFile(filepath.Join(directory, "commands"))
	if err != nil || !strings.Contains(string(recorded), "bootstrap system "+plist) {
		t.Fatal("Installer returned success without registering its launchd job")
	}
}

func runInstallerScript(t *testing.T, directory, name, launchctl string, readiness ...string) (string, error) {
	t.Helper()
	source, err := os.ReadFile("../../packaging/macos/service-install/" + name)
	if err != nil {
		t.Fatal(err)
	}
	library := filepath.Join(directory, "Library")
	if err := os.MkdirAll(filepath.Join(directory, "run"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"PrivilegedHelperTools", "LaunchDaemons"} {
		if err := os.MkdirAll(filepath.Join(library, name), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	helper := filepath.Join(library, "PrivilegedHelperTools", Label)
	helperScript := "#!/bin/sh\nprintf 'helper %s\\n' \"$*\" >> \"$TAILSPLIT_INSTALL_TEST_COMMANDS\"\n"
	if len(readiness) > 0 {
		helperScript += readiness[0]
	} else {
		helperScript += "exit 0\n"
	}
	if err := os.WriteFile(helper, []byte(helperScript), 0o755); err != nil {
		t.Fatal(err)
	}
	mocks := map[string]string{
		"/usr/bin/stat": "#!/bin/sh\n" +
			"case \"$2:$3\" in %u:/dev/console) echo 501;; %u:*) echo 0;; %g:*) echo 20;; %Lp:*) echo 700;; *) exit 1;; esac\n",
		"/usr/sbin/chown": "#!/bin/sh\nexit 0\n",
		"/bin/sleep":      "#!/bin/sh\nexit 0\n",
		"/bin/launchctl": "#!/bin/sh\n" +
			"printf '%s\\n' \"$*\" >> \"$TAILSPLIT_INSTALL_TEST_COMMANDS\"\n" + launchctl,
	}
	script := strings.NewReplacer("/Library/", library+"/", "/var/run/", directory+"/run/").Replace(string(source))
	for original, content := range mocks {
		path := filepath.Join(directory, filepath.Base(original))
		if err := os.WriteFile(path, []byte(content), 0o700); err != nil {
			t.Fatal(err)
		}
		script = strings.ReplaceAll(script, original, `"`+path+`"`)
	}
	commands := filepath.Join(directory, "commands")
	command := exec.Command("/bin/sh", "-c", script)
	command.Env = append(os.Environ(), "TAILSPLIT_INSTALL_TEST_COMMANDS="+commands,
		"TAILSPLIT_INSTALL_TEST_DIR="+directory)
	output, err := command.CombinedOutput()
	return string(output), err
}

func TestInstallerWaitsForAsynchronousBootoutBeforeReplacingCode(t *testing.T) {
	directory := t.TempDir()
	// Model launchd's real behavior: bootout succeeds immediately, while
	// print still sees the terminating job for several subsequent polls.
	launchctl := `
case "$1" in
  bootout) touch "$TAILSPLIT_INSTALL_TEST_DIR/stopping"; exit 0;;
  print)
    test -e "$TAILSPLIT_INSTALL_TEST_DIR/stopping" || exit 0
    count=0
    test ! -e "$TAILSPLIT_INSTALL_TEST_DIR/polls" || count=$(cat "$TAILSPLIT_INSTALL_TEST_DIR/polls")
    count=$((count + 1))
    echo "$count" > "$TAILSPLIT_INSTALL_TEST_DIR/polls"
    test "$count" -ge 4 || exit 0
    touch "$TAILSPLIT_INSTALL_TEST_DIR/stopped"
    exit 1;;
esac
exit 1
`
	if output, err := runInstallerScript(t, directory, "preinstall", launchctl); err != nil {
		t.Fatalf("preinstall failed: %v: %s", err, output)
	}
	if _, err := os.Stat(filepath.Join(directory, "stopped")); err != nil {
		t.Fatal("Installer can replace the executable while the old job is still stopping")
	}
}

func TestInstallerDoesNotReplaceCodeWhenOldServiceNeverStops(t *testing.T) {
	directory := t.TempDir()
	output, err := runInstallerScript(t, directory, "preinstall", "exit 0\n")
	if err == nil || !strings.Contains(output, "has not been replaced") {
		t.Fatal("preinstall succeeded while launchd still owned the old executable:", output, err)
	}
}

func TestInstallerRemovesStaleControlSocketOnlyAfterTheOldJobStops(t *testing.T) {
	for _, oldJobStops := range []bool{true, false} {
		t.Run(map[bool]string{true: "stopped", false: "still-stopping"}[oldJobStops], func(t *testing.T) {
			directory := installerSocketDirectory(t)
			socket := installerSocketFixture(t, directory)
			launchctl := "exit 0\n"
			if oldJobStops {
				launchctl = `
case "$1" in
  bootout) touch "$TAILSPLIT_INSTALL_TEST_DIR/stopping"; exit 0;;
  print)
    test ! -e "$TAILSPLIT_INSTALL_TEST_DIR/stopping" || exit 1
    test -S "$TAILSPLIT_INSTALL_TEST_DIR/run/im.lbv.tailsplit.sock" || exit 2
    exit 0;;
esac
exit 1
`
			}
			output, err := runInstallerScript(t, directory, "preinstall", launchctl)
			_, socketErr := os.Lstat(socket)
			if oldJobStops {
				if err != nil || !os.IsNotExist(socketErr) {
					t.Fatalf("stale socket prevents a new user's service activation: %v, %v: %s", err, socketErr, output)
				}
			} else if err == nil || socketErr != nil {
				t.Fatalf("removed a socket before the old service stopped: %v, %v: %s", err, socketErr, output)
			}
		})
	}
}

func installerSocketDirectory(t *testing.T) string {
	t.Helper()
	// Darwin Unix socket paths are limited to 104 bytes. t.TempDir includes
	// the test name and can exceed that before the control filename is added.
	directory, err := os.MkdirTemp("/tmp", "tailsplit-installer-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(directory) })
	return directory
}

func installerSocketFixture(t *testing.T, directory string) string {
	t.Helper()
	run := filepath.Join(directory, "run")
	if err := os.MkdirAll(run, 0o755); err != nil {
		t.Fatal(err)
	}
	socket := filepath.Join(run, "im.lbv.tailsplit.sock")
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: socket, Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	listener.SetUnlinkOnClose(false)
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	return socket
}

func TestInstallerDoesNotRemoveUnexpectedControlSocketPaths(t *testing.T) {
	for _, kind := range []string{"file", "directory", "file-link", "socket-link", "dangling-link"} {
		t.Run(kind, func(t *testing.T) {
			directory := installerSocketDirectory(t)
			socket := installerSocketFixture(t, directory)
			if err := os.Remove(socket); err != nil {
				t.Fatal(err)
			}
			var err error
			switch kind {
			case "file":
				err = os.WriteFile(socket, []byte("unrelated data"), 0o600)
			case "directory":
				err = os.Mkdir(socket, 0o700)
			case "file-link":
				target := filepath.Join(directory, "unrelated")
				if err := os.WriteFile(target, []byte("unrelated data"), 0o600); err != nil {
					t.Fatal(err)
				}
				err = os.Symlink(target, socket)
			case "socket-link":
				target := installerSocketFixture(t, installerSocketDirectory(t))
				err = os.Symlink(target, socket)
			case "dangling-link":
				err = os.Symlink(filepath.Join(directory, "missing"), socket)
			}
			if err != nil {
				t.Fatal(err)
			}
			before, err := os.Lstat(socket)
			if err != nil {
				t.Fatal(err)
			}
			output, err := runInstallerScript(t, directory, "preinstall", "exit 1\n")
			if err == nil || !strings.Contains(output, "not a Unix socket") {
				t.Fatalf("unexpected path did not stop installation: %v: %s", err, output)
			}
			after, err := os.Lstat(socket)
			if err != nil || !os.SameFile(before, after) {
				t.Fatalf("installer removed or replaced an unexpected %s: %v", kind, err)
			}
		})
	}
}

func TestInstallerCanRepeatInstallationAfterRemovingALeftoverSocket(t *testing.T) {
	directory := installerSocketDirectory(t)
	for installation := 0; installation < 2; installation++ {
		socket := installerSocketFixture(t, directory)
		if output, err := runInstallerScript(t, directory, "preinstall", "exit 1\n"); err != nil {
			t.Fatalf("installation %d could not clear its leftover socket: %v: %s", installation, err, output)
		}
		if _, err := os.Lstat(socket); !os.IsNotExist(err) {
			t.Fatal("leftover socket would block launchd activation", err)
		}
		if output, err := runInstallerScript(t, directory, "postinstall", "exit 0\n"); err != nil {
			t.Fatalf("installation %d failed to register and check readiness: %v: %s", installation, err, output)
		}
	}
}

func TestInstallerAcceptsFirstInstallationWithoutAControlSocket(t *testing.T) {
	if output, err := runInstallerScript(t, t.TempDir(), "preinstall", "exit 1\n"); err != nil {
		t.Fatalf("first installation without an old service failed: %v: %s", err, output)
	}
}

func TestInstallerRetriesTransientRegistrationAndChecksTheJob(t *testing.T) {
	directory := t.TempDir()
	launchctl := `
case "$1" in
  enable) exit 0;;
  bootstrap)
    count=0
    test ! -e "$TAILSPLIT_INSTALL_TEST_DIR/attempts" || count=$(cat "$TAILSPLIT_INSTALL_TEST_DIR/attempts")
    count=$((count + 1))
    echo "$count" > "$TAILSPLIT_INSTALL_TEST_DIR/attempts"
    test "$count" -ge 3 || { echo "Operation already in progress" >&2; exit 5; }
    touch "$TAILSPLIT_INSTALL_TEST_DIR/registered"
    exit 0;;
  print) test -e "$TAILSPLIT_INSTALL_TEST_DIR/registered"; exit $?;;
esac
exit 1
`
	if output, err := runInstallerScript(t, directory, "postinstall", launchctl); err != nil {
		t.Fatalf("registration did not recover: %v: %s", err, output)
	}
	if _, err := os.Stat(filepath.Join(directory, "registered")); err != nil {
		t.Fatal("Installer claimed success without a registered service")
	}
}

func TestInstallerRequiresReadyServiceAfterSuccessfulRegistration(t *testing.T) {
	for _, becomesReady := range []bool{true, false} {
		t.Run(map[bool]string{true: "ready-after-retry", false: "never-ready"}[becomesReady], func(t *testing.T) {
			directory := t.TempDir()
			readiness := `
count=0
counter="$TAILSPLIT_INSTALL_TEST_DIR/readiness-attempts"
test ! -e "$counter" || count=$(cat "$counter")
count=$((count + 1))
echo "$count" > "$counter"
`
			if becomesReady {
				readiness += "test \"$count\" -ge 3\n"
			} else {
				readiness += "exit 1\n"
			}
			output, err := runInstallerScript(t, directory, "postinstall", "exit 0\n", readiness)
			if becomesReady && err != nil {
				t.Fatalf("ready service was rejected: %v: %s", err, output)
			}
			if !becomesReady && (err == nil || !strings.Contains(output, "did not become ready")) {
				t.Fatalf("registration hid service startup failure: %v: %s", err, output)
			}
			attempts, err := os.ReadFile(filepath.Join(directory, "readiness-attempts"))
			wantAttempts := "10\n"
			if becomesReady {
				wantAttempts = "3\n"
			}
			if err != nil || string(attempts) != wantAttempts {
				t.Fatalf("unexpected readiness retry budget: %q: %v", attempts, err)
			}
			commands, err := os.ReadFile(filepath.Join(directory, "commands"))
			if err != nil || !strings.Contains(string(commands), "helper service-check --uid 501 --gid 20") {
				t.Fatalf("readiness did not use the registered desktop user: %s: %v", commands, err)
			}
		})
	}
}

func TestInstallerReportsPersistentRegistrationFailure(t *testing.T) {
	directory := t.TempDir()
	launchctl := "test \"$1\" != bootstrap || exit 5\nexit 0\n"
	output, err := runInstallerScript(t, directory, "postinstall", launchctl)
	if err == nil || !strings.Contains(output, "could not be registered") {
		t.Fatal("Installer hid a persistent bootstrap failure:", output, err)
	}
}

func TestInstallerCannotSucceedWithoutARegisteredJob(t *testing.T) {
	directory := t.TempDir()
	launchctl := "test \"$1\" != print || exit 1\nexit 0\n"
	if output, err := runInstallerScript(t, directory, "postinstall", launchctl); err == nil {
		t.Fatal("Installer claimed success while its launchd job was missing:", output)
	}
}
