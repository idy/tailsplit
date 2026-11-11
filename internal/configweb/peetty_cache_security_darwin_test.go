package configweb

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/idy/tailsplit/internal/privatefs"
)

func peettyCacheSecurityACL(t *testing.T, path, entry string) {
	t.Helper()
	if output, err := exec.Command("/bin/chmod", "+a", entry, path).CombinedOutput(); err != nil {
		t.Fatalf("install ACL fixture: %s: %v", output, err)
	}
	t.Cleanup(func() {
		if output, err := exec.Command("/bin/chmod", "-N", path).CombinedOutput(); err != nil {
			t.Errorf("remove ACL fixture: %s: %v", output, err)
		}
	})
}

func TestPeettyCacheSecurityRejectsDarwinDirectoryACLs(t *testing.T) {
	for _, tc := range []struct {
		name, entry string
		ancestor    bool
	}{
		{"cache-replacement", "everyone allow add_file,delete_child", false},
		{"ancestor-replacement", "everyone allow add_subdirectory,delete_child", true},
		{"cache-inherited-read", "everyone allow read,file_inherit,only_inherit", false},
		{"ancestor-inherited-replacement", "everyone allow delete_child,directory_inherit,only_inherit", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			config, u, original := peettyCacheSecurityFixture(t)
			directory := filepath.Dir(u.path)
			if tc.ancestor {
				directory = filepath.Dir(directory)
			}
			peettyCacheSecurityACL(t, directory, tc.entry)
			if info, err := os.Stat(directory); err != nil || info.Mode().Perm() != 0o700 {
				t.Fatal("fixture does not exercise ACL grants beyond 0700 mode bits", err)
			}
			// These are the old checks: the file stays 0600, while the directory
			// ACL lets another identity replace it or expose future payload bytes.
			if err := privatefs.EnsureDir(filepath.Dir(u.path)); err != nil {
				t.Fatal("fixture did not pass the old directory check", err)
			}
			if err := privatefs.CheckFile(u.path); err != nil {
				t.Fatal("fixture did not pass the old file check", err)
			}
			if newPeettyUpdates(config).snapshot() != nil {
				t.Fatal("directory ACL allowed executable offline cache reuse")
			}
			cdn := &peettyCDNFixture{versions: []string{"0.3.2"}, files: map[string]map[string][]byte{"v0.3.2": peettyTestFiles("blue", false)}}
			cdn.attach(t, u)
			if err := u.check(context.Background()); err == nil {
				t.Fatal("unsafe directory allowed a downloaded release to activate")
			}
			if u.snapshot().Version != "v0.3.1" {
				t.Fatal("failed publication changed active assets")
			}
			assertPeettyCacheUnchanged(t, u.path, original)
		})
	}
}

func TestPeettyCacheSecurityRejectsDarwinFileACL(t *testing.T) {
	config, u, original := peettyCacheSecurityFixture(t)
	peettyCacheSecurityACL(t, u.path, "everyone allow read")
	if info, err := os.Stat(u.path); err != nil || info.Mode().Perm() != 0o600 {
		t.Fatal("fixture does not exercise ACL grants beyond 0600 mode bits", err)
	}
	if newPeettyUpdates(config).snapshot() != nil {
		t.Fatal("public file ACL allowed executable offline cache reuse")
	}
	if err := u.save(peettyCacheSecurityRelease(t, "v0.3.2")); err == nil {
		t.Fatal("unsafe existing file was adopted as a publication destination")
	}
	assertPeettyCacheUnchanged(t, u.path, original)
}

func TestPeettyCacheSecurityDarwinTempPrivateBeforeWriteAndAfterPublish(t *testing.T) {
	parent, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	peettyCacheSecurityACL(t, parent, "everyone allow read,file_inherit,only_inherit")
	control, err := os.CreateTemp(parent, "control-*")
	if err != nil {
		t.Fatal(err)
	}
	if err := control.Close(); err != nil {
		t.Fatal(err)
	}
	if info, err := os.Stat(control.Name()); err != nil || info.Mode().Perm() != 0o600 {
		t.Fatal("control does not have the old staging file's 0600 mode", err)
	}
	if err := privatefs.CheckFile(control.Name()); err == nil {
		t.Fatal("control did not inherit the unsafe ACL")
	}
	file, err := createPeettyCacheTemp(parent)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	if info, err := file.Stat(); err != nil || info.Size() != 0 {
		t.Fatal("staging file already contains payload bytes", err)
	}
	if _, err := checkPeettyCacheFile(file.Name()); err != nil {
		t.Fatal("staging file was not private before writing", err)
	}
	payload := []byte("verified payload\n")
	if _, err := file.Write(payload); err != nil {
		t.Fatal(err)
	}
	if err := errors.Join(file.Sync(), file.Close()); err != nil {
		t.Fatal(err)
	}
	// Rename must preserve the creation-time private ACL rather than inherit
	// the destination parent's grants, including when replacing an old file.
	target := filepath.Join(parent, "current.json")
	old, err := privatefs.CreateFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if err := old.Close(); err != nil {
		t.Fatal(err)
	}
	if err := privatefs.Replace(file.Name(), target); err != nil {
		t.Fatal(err)
	}
	if _, err := checkPeettyCacheFile(target); err != nil {
		t.Fatal("atomic replacement lost the private ACL", err)
	}
	if _, err := os.Lstat(file.Name()); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("atomic replacement retained the staging file", err)
	}
	data, err := os.ReadFile(target)
	if err != nil || !bytes.Equal(data, payload) {
		t.Fatal("atomic replacement lost payload bytes", err)
	}
}
