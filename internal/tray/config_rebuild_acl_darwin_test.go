package tray

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/idy/tailsplit"
	"github.com/idy/tailsplit/internal/privatefs"
)

func TestConfigRecoveryBackupsArePrivateUnderInheritedDarwinACL(t *testing.T) {
	repair := strings.Replace(string(tailsplit.DefaultYAML), "dns:\n", "dns:\n  listen: 127.0.0.1:15353\n", 1)
	if repair == string(tailsplit.DefaultYAML) {
		t.Fatal("repair fixture did not add the obsolete listener")
	}
	for _, tc := range []struct {
		name string
		data []byte
		run  func(string, string) (string, error)
	}{
		{"rebuild", []byte("# preserve exact backup bytes\ndns: [\n"), RebuildConfigDocument},
		{"repair", []byte(repair), RepairConfigDocument},
	} {
		t.Run(tc.name, func(t *testing.T) {
			directory := t.TempDir()
			path := filepath.Join(directory, "config.yaml")
			revision := writeRebuildDocument(t, path, tc.data)
			if output, err := exec.Command("/bin/chmod", "+a", "everyone allow read,file_inherit,only_inherit", directory).CombinedOutput(); err != nil {
				t.Fatalf("install inherited ACL: %s: %v", output, err)
			}
			t.Cleanup(func() { _ = exec.Command("/bin/chmod", "-N", directory).Run() })
			backup, err := tc.run(path, revision)
			if err != nil {
				t.Fatal(err)
			}
			requireRebuildBytes(t, backup, tc.data)
			for _, name := range []string{backup, path} {
				if err := privatefs.CheckFile(name); err != nil {
					t.Fatal("recovery retained an inherited public ACL", name, err)
				}
			}
			if _, err := tailsplit.LoadDraft(path); err != nil {
				t.Fatal("recovery did not leave a valid configuration", err)
			}
			// Recovery must not change the user's parent ACL to obtain privacy.
			control := filepath.Join(directory, "control")
			if err := os.WriteFile(control, []byte("nonsensitive control\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			if err := privatefs.CheckFile(control); err == nil {
				t.Fatal("recovery modified the inherited parent policy")
			}
		})
	}
}
