//go:build darwin

package main

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestDarwinUpdateWorkerActivationAndRollback(t *testing.T) {
	for _, scenario := range []string{"activate", "no permission to proceed", "activation fails", "relaunch fails", "signature fails"} {
		t.Run(scenario, func(t *testing.T) {
			root := t.TempDir()
			work := filepath.Join(root, "private download")
			target := filepath.Join(root, "Applications", "TailSplit.app")
			source := filepath.Join(work, "extracted", "TailSplit.app")
			for _, app := range []string{target, source} {
				if err := os.MkdirAll(filepath.Join(app, "Contents"), 0o755); err != nil {
					t.Fatal(err)
				}
				plist := `<?xml version="1.0"?><plist version="1.0"><dict><key>CFBundleIdentifier</key><string>im.lbv.tailsplit</string><key>CFBundleShortVersionString</key><string>0.6.3</string></dict></plist>`
				if err := os.WriteFile(filepath.Join(app, "Contents", "Info.plist"), []byte(plist), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			if err := os.WriteFile(filepath.Join(target, "version"), []byte("old"), 0o644); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(source, "version"), []byte("new"), 0o644); err != nil {
				t.Fatal(err)
			}
			launchLog := filepath.Join(root, "launch.log")
			launcher := filepath.Join(root, "open-app")
			launchScript := "#!/bin/sh\nprintf '%s\\n' \"$@\" >> " + shellQuote(launchLog) + "\n"
			if scenario == "relaunch fails" {
				launchScript += "[ -f " + shellQuote(launchLog+".once") + " ] || { touch " + shellQuote(launchLog+".once") + "; exit 1; }\n"
			}
			launchScript += "exit 0\n"
			if err := os.WriteFile(launcher, []byte(launchScript), 0o700); err != nil {
				t.Fatal(err)
			}
			script := strings.NewReplacer("/usr/bin/codesign", "/usr/bin/true", "/usr/sbin/spctl", "/usr/bin/true", "/usr/bin/open", shellQuote(launcher), "$(/usr/bin/id -u)", "501", "/bin/sleep 1", "/bin/sleep 0.01").Replace(darwinUpdateScript)
			if scenario == "signature fails" {
				script = strings.ReplaceAll(script, "/usr/bin/true --verify", "/usr/bin/false --verify")
			}
			if scenario == "activation fails" {
				mover := filepath.Join(root, "move-app")
				moveScript := "#!/bin/sh\ncase \"$1\" in */.tailsplit-update.*/TailSplit.app) exit 1;; esac\nexec /bin/mv \"$@\"\n"
				if err := os.WriteFile(mover, []byte(moveScript), 0o700); err != nil {
					t.Fatal(err)
				}
				script = strings.ReplaceAll(script, "/bin/mv", shellQuote(mover))
			}
			parent := exec.Command("/bin/sleep", "30")
			if err := parent.Start(); err != nil {
				t.Fatal(err)
			}
			var stopOnce sync.Once
			stopParent := func() { stopOnce.Do(func() { _ = parent.Process.Kill(); _ = parent.Wait() }) }
			defer stopParent()
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, "/bin/sh", "-c", script, "fixture", work, target, strconv.Itoa(parent.Process.Pid), "501", "config with ' quotes.yaml", "logs/tailsplit.log", "fixture-requirement", "0.6.3")
			var output bytes.Buffer
			cmd.Stdout, cmd.Stderr = &output, &output
			if err := cmd.Start(); err != nil {
				t.Fatal(err)
			}
			done := make(chan error, 1)
			go func() { done <- cmd.Wait() }()
			if scenario != "signature fails" {
				ready := false
				deadline := time.Now().Add(5 * time.Second)
				for time.Now().Before(deadline) {
					if _, err := os.Stat(filepath.Join(work, "ready")); err == nil {
						ready = true
						break
					}
					time.Sleep(time.Millisecond)
				}
				if !ready {
					_ = cmd.Process.Kill()
					<-done
					t.Fatalf("worker did not stage app: %s", output.String())
				}
				if scenario != "no permission to proceed" {
					if err := os.WriteFile(filepath.Join(work, "proceed"), nil, 0o600); err != nil {
						t.Fatal(err)
					}
				}
			}
			stopParent()
			err := <-done
			if (err == nil) != (scenario == "activate") {
				t.Fatalf("unexpected worker result %v: %s", err, output.String())
			}
			value, readErr := os.ReadFile(filepath.Join(target, "version"))
			want := "old"
			if scenario == "activate" {
				want = "new"
			}
			if readErr != nil || string(value) != want {
				t.Fatalf("bundle was not activated/restored: %s %v, output=%s", value, readErr, output.String())
			}
			if _, err := os.Stat(work); !os.IsNotExist(err) {
				t.Fatal("worker left the private download directory")
			}
			stages, _ := filepath.Glob(filepath.Join(filepath.Dir(target), ".tailsplit-update.*"))
			if len(stages) != 0 {
				t.Fatal("worker left staging directories:", stages)
			}
			launches, _ := os.ReadFile(launchLog)
			if scenario == "activate" && (!bytes.Contains(launches, []byte("--no-connect")) || !bytes.Contains(launches, []byte("config with ' quotes.yaml"))) {
				t.Fatal("relaunch lost literal arguments:", string(launches))
			}
			if (scenario == "activation fails" || scenario == "relaunch fails") && !bytes.Contains(launches, []byte("--update-failed")) {
				t.Fatal("old app did not receive failure notification")
			}
		})
	}
}

func TestDarwinUpdateRequiresInstalledApp(t *testing.T) {
	for _, path := range []string{"/tmp/tailsplit-tray", "/tmp/TailSplit.app/tailsplit-tray", "/private/var/AppTranslocation/random/TailSplit.app/Contents/MacOS/tailsplit-tray"} {
		if _, err := installedDarwinApp(path); err == nil {
			t.Fatal("accepted noninstalled app:", path)
		}
	}
}
