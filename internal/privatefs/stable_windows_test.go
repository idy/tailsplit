//go:build windows

package privatefs

import (
	"path/filepath"
	"testing"

	"golang.org/x/sys/windows"
)

func TestWindowsStableDirCreationAndCleanup(t *testing.T) {
	root := filepath.Join(t.TempDir(), "private", "snapshots")
	if err := StableDir(root, true); err != nil {
		t.Fatal(err)
	}
	if err := StableDir(root, false); err != nil {
		t.Fatal(err)
	}
	setTestDACL(t, filepath.Dir(root), "D:P(A;OICI;FA;;;"+mustStableUser(t).String()+")(A;;0x40;;;WD)")
	if err := StableDir(root, false); err == nil {
		t.Fatal("accepted a private child whose parent grants Everyone DELETE_CHILD")
	}
}

func mustStableUser(t *testing.T) *windows.SID {
	t.Helper()
	user, err := currentUserSID()
	if err != nil {
		t.Fatal(err)
	}
	return user
}

func TestWindowsStableDescriptorAllowsReadAndRejectsReplacement(t *testing.T) {
	user := mustStableUser(t)
	for _, rights := range []string{"FR", "0x4", "0x40", "SD", "WD", "WO", "FA"} {
		t.Run(rights, func(t *testing.T) {
			descriptor, err := windows.SecurityDescriptorFromString("O:" + user.String() +
				"D:P(A;;FA;;;" + user.String() + ")(A;;" + rights + ";;;WD)")
			if err != nil {
				t.Fatal(err)
			}
			err = checkStableDescriptor(descriptor, user)
			allowed := rights == "FR" || rights == "0x4" // Read and add-subdirectory only.
			if (err == nil) != allowed {
				t.Fatal("unexpected ancestor ACL decision", rights, err)
			}
		})
	}
}
