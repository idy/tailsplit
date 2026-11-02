//go:build darwin

package appservice

import (
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

func runInstallerScript(t *testing.T, directory, name, launchctl string) (string, error) {
	t.Helper()
	source, err := os.ReadFile("../../packaging/macos/service-install/" + name)
	if err != nil {
		t.Fatal(err)
	}
	library := filepath.Join(directory, "Library")
	for _, name := range []string{"PrivilegedHelperTools", "LaunchDaemons"} {
		if err := os.MkdirAll(filepath.Join(library, name), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	helper := filepath.Join(library, "PrivilegedHelperTools", Label)
	if err := os.WriteFile(helper, []byte("fixture helper"), 0o755); err != nil {
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
	script := strings.ReplaceAll(string(source), "/Library/", library+"/")
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
