package privatefs

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestDarwinACLReadDoesNotDependOnPriorErrno(t *testing.T) {
	api, err := darwinACL()
	if err != nil {
		t.Fatal(err)
	}
	parent := t.TempDir()
	plain, private := filepath.Join(parent, "plain"), filepath.Join(parent, "private")
	if err := os.WriteFile(plain, []byte("control\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	file, err := CreateFile(private)
	if err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{plain, private} {
		for _, prior := range []int32{0, 2, 13, 260} {
			runtime.LockOSThread()
			*api.errno() = prior
			err := CheckFile(path)
			runtime.UnlockOSThread()
			if err != nil {
				t.Fatal("ACL lookup depended on unrelated prior native errno", prior, err)
			}
		}
	}
}
