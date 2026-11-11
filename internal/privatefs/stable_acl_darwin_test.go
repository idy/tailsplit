package privatefs

import (
	"errors"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"testing"
)

func TestStableDirChecksDarwinACLGrantsBeyondModeBits(t *testing.T) {
	current, err := user.Current()
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, entry string
		private     bool
		allowed     bool
	}{
		{"replace-child", "everyone allow delete_child,add_subdirectory", false, false},
		{"change-security", "everyone allow writesecurity", false, false},
		{"inherited-replacement", "everyone allow delete_child,directory_inherit,only_inherit", false, false},
		{"deny-delete", "everyone deny delete", false, true},
		{"read-ancestor", "everyone allow readattr,readsecurity", false, true},
		{"private-read-grant", "everyone allow readattr,readsecurity", true, false},
		{"private-inherited-read", "everyone allow read,file_inherit,only_inherit", true, false},
		{"trusted-owner", "user:" + current.Username + " allow delete_child", false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			parent := filepath.Join(stableTestRoot(t), "parent")
			root := filepath.Join(parent, "private")
			if err := StableDir(root, true); err != nil {
				t.Fatal(err)
			}
			target := parent
			if tc.private {
				target = root
			}
			command := exec.Command("/bin/chmod", "+a", tc.entry, target)
			if output, err := command.CombinedOutput(); err != nil {
				t.Fatalf("could not install ACL fixture: %s: %v", output, err)
			}
			t.Cleanup(func() {
				if output, err := exec.Command("/bin/chmod", "-N", target).CombinedOutput(); err != nil {
					t.Errorf("could not remove fixture ACL: %s: %v", output, err)
				}
			})
			if info, err := os.Stat(target); err != nil || info.Mode().Perm() != 0o700 {
				t.Fatal("ACL fixture changed mode bits instead of exercising extended permissions", err)
			}
			if err := StableDir(root, false); (err == nil) != tc.allowed {
				t.Fatal("unexpected extended ACL decision", tc.entry, err)
			}
		})
	}
}

func TestStableDirDarwinMissingACLPathIsNotAnEmptyACL(t *testing.T) {
	path := filepath.Join(stableTestRoot(t), "missing")
	if err := checkDirectoryACL(path, false); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("missing directory was mistaken for an existing directory without an ACL", err)
	}
}
