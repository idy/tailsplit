//go:build windows

package privatefs

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/windows"
)

func TestWindowsPrivateStateProtectsInheritedFiles(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "state")
	if err := EnsureDir(path); err != nil {
		t.Fatal(err)
	}
	child := filepath.Join(path, "tailscale")
	if err := os.Mkdir(child, 0o700); err != nil {
		t.Fatal(err)
	}
	stateFile := filepath.Join(child, "tailscaled.state")
	if err := os.WriteFile(stateFile, []byte("saved device state"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := CheckFile(stateFile); err != nil {
		t.Fatal("child did not inherit the private DACL:", err)
	}
	if err := EnsureDir(path); err != nil {
		t.Fatal("private state could not be reopened:", err)
	}
	// A public child remains directly accessible with Windows bypass-traverse
	// privilege even when its parent is private. It must block startup.
	user, err := currentUserSID()
	if err != nil {
		t.Fatal(err)
	}
	setTestDACL(t, stateFile, "D:P(A;;FA;;;"+user.String()+")(A;;FR;;;WD)")
	if err := EnsureDir(path); err == nil {
		t.Fatal("accepted a public saved device-state file")
	}
}

func TestWindowsPrivateStateRejectsPublicDirectory(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state")
	if err := EnsureDir(path); err != nil {
		t.Fatal(err)
	}
	user, err := currentUserSID()
	if err != nil {
		t.Fatal(err)
	}
	setTestDACL(t, path, "D:P(A;OICI;FA;;;"+user.String()+")(A;OICI;FR;;;WD)")
	if err := EnsureDir(path); err == nil {
		t.Fatal("accepted a state directory readable by Everyone")
	}
}

func TestWindowsPrivateConfigCreationIsExclusive(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	file, err := CreateFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.WriteString("version: 1\n"); err != nil {
		_ = file.Close()
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	if err := CheckFile(path); err != nil {
		t.Fatal("configuration DACL is not private:", err)
	}
	if other, err := CreateFile(path); err == nil {
		_ = other.Close()
		t.Fatal("overwrote existing configuration")
	} else if !errors.Is(err, os.ErrExist) {
		t.Fatal("creation did not retain the existing-file error:", err)
	}
}

func TestWindowsPrivateStateRejectsFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state")
	file, err := CreateFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	if err := EnsureDir(path); err == nil {
		t.Fatal("accepted a file as state_dir")
	}
}

func TestWindowsPrivateDescriptorRejectsUnsafeACLs(t *testing.T) {
	user, err := currentUserSID()
	if err != nil {
		t.Fatal(err)
	}
	owner := "O:" + user.String()
	grant := "(A;OICI;FA;;;" + user.String() + ")"
	for name, sddl := range map[string]string{
		"wrong owner":           "O:WDD:P" + grant,
		"inherited root":        owner + "D:" + grant,
		"no DACL":               owner,
		"empty DACL":            owner + "D:P",
		"public child grant":    owner + "D:P" + grant + "(A;OICIIO;FR;;;WD)",
		"noninheritable user":   owner + "D:P(A;;FA;;;" + user.String() + ")",
		"one-level inheritance": owner + "D:P(A;OICINP;FA;;;" + user.String() + ")",
	} {
		t.Run(name, func(t *testing.T) {
			descriptor, err := windows.SecurityDescriptorFromString(sddl)
			if err != nil {
				t.Fatal(err)
			}
			if err := checkDescriptor(descriptor, user, true, true); err == nil {
				t.Fatal("accepted unsafe state-directory permissions")
			}
		})
	}
	t.Run("null DACL", func(t *testing.T) {
		descriptor, err := privateDescriptor(user, true)
		if err != nil {
			t.Fatal(err)
		}
		absolute, err := descriptor.ToAbsolute()
		if err != nil {
			t.Fatal(err)
		}
		if err := absolute.SetDACL(nil, true, false); err != nil {
			t.Fatal(err)
		}
		if err := checkDescriptor(absolute, user, true, true); err == nil {
			t.Fatal("accepted a fully permissive null DACL")
		}
	})
}

func setTestDACL(t *testing.T, path, sddl string) {
	t.Helper()
	descriptor, err := windows.SecurityDescriptorFromString(sddl)
	if err != nil {
		t.Fatal(err)
	}
	dacl, _, err := descriptor.DACL()
	if err != nil {
		t.Fatal(err)
	}
	if err := windows.SetNamedSecurityInfo(path, windows.SE_FILE_OBJECT,
		windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION, nil, nil, dacl, nil); err != nil {
		t.Fatal(err)
	}
}
