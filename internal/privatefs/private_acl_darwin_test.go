package privatefs

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestDarwinPrivateFileClearsInheritedACLBeforeWrite(t *testing.T) {
	parent := t.TempDir()
	command := exec.Command("/bin/chmod", "+a", "everyone allow read,file_inherit,only_inherit", parent)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("install inherited ACL: %s: %v", output, err)
	}
	t.Cleanup(func() { _ = exec.Command("/bin/chmod", "-N", parent).Run() })

	// Prove that this OS-created mode-0600 control inherits an effective read
	// grant; checking mode bits alone would accept it.
	control := filepath.Join(parent, "control")
	if err := os.WriteFile(control, []byte("nonsensitive control\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if info, err := os.Stat(control); err != nil || info.Mode().Perm() != 0o600 {
		t.Fatal("control does not exercise inherited ACL access beyond mode bits", err)
	}
	if err := CheckFile(control); err == nil {
		t.Fatal("mode-0600 control with an inherited public read ACL was accepted")
	}

	path := filepath.Join(parent, "private")
	file, err := CreateFile(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	if info, err := file.Stat(); err != nil || info.Size() != 0 {
		t.Fatal("private creation already wrote payload bytes", err)
	}
	if err := CheckFile(path); err != nil {
		t.Fatal("creation did not establish privacy before payload writing", err)
	}
	if _, err := file.WriteString("private fixture\n"); err != nil {
		t.Fatal(err)
	}
	if err := file.Sync(); err != nil {
		t.Fatal(err)
	}
	if err := CheckFile(path); err != nil {
		t.Fatal("private file lost privacy after writing", err)
	}
	if output, err := exec.Command("/bin/chmod", "+a", "everyone allow read", path).CombinedOutput(); err != nil {
		t.Fatalf("install direct ACL: %s: %v", output, err)
	}
	if err := CheckFile(path); err == nil {
		t.Fatal("private-file check ignored a later public read ACL")
	}
}
