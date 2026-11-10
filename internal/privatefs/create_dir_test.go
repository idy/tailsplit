package privatefs

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestCreateDirIsPrivateExclusiveAndUsableAsStorageRoot(t *testing.T) {
	parent := filepath.Join(t.TempDir(), "private")
	if err := EnsureDir(parent); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(parent, "new-device")
	if err := CreateDir(path); err != nil {
		t.Fatal(err)
	}
	// The namespace must also pass the stricter root check used by later
	// saved-device logout/import, including protected Windows ACL ownership.
	if err := EnsureDir(path); err != nil {
		t.Fatal("exclusive creation did not establish a private storage root", err)
	}
	marker := filepath.Join(path, ".initialized")
	file, err := CreateFile(marker)
	if err != nil {
		t.Fatal(err)
	}
	_, writeErr := file.WriteString("existing authority\n")
	if err := errors.Join(writeErr, file.Close()); err != nil {
		t.Fatal(err)
	}
	before, err := os.Lstat(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := CreateDir(path); !errors.Is(err, os.ErrExist) {
		t.Fatal("exclusive directory creation accepted an existing namespace", err)
	}
	after, err := os.Lstat(path)
	if err != nil || !os.SameFile(before, after) {
		t.Fatal("exclusive creation replaced an existing namespace", err)
	}
	if data, err := os.ReadFile(marker); err != nil || string(data) != "existing authority\n" {
		t.Fatal("exclusive creation changed an authority marker", err)
	}
	if err := CheckFile(marker); err != nil {
		t.Fatal("namespace did not keep its marker private", err)
	}
}

func TestCreateDirRejectsExistingEntriesWithoutFollowingSymlinks(t *testing.T) {
	for _, kind := range []string{"empty-directory", "file", "symlink", "dangling-symlink"} {
		t.Run(kind, func(t *testing.T) {
			parent := filepath.Join(t.TempDir(), "private")
			if err := EnsureDir(parent); err != nil {
				t.Fatal(err)
			}
			path, target := filepath.Join(parent, "existing"), filepath.Join(parent, "target")
			if err := EnsureDir(target); err != nil {
				t.Fatal(err)
			}
			switch kind {
			case "empty-directory":
				if err := CreateDir(path); err != nil {
					t.Fatal(err)
				}
			case "file":
				file, err := CreateFile(path)
				if err != nil {
					t.Fatal(err)
				}
				_ = file.Close()
			default:
				if kind == "dangling-symlink" {
					target = filepath.Join(parent, "missing")
				}
				if err := os.Symlink(target, path); err != nil {
					t.Skip("fixture symlink unavailable", err)
				}
			}
			before, err := os.Lstat(path)
			if err != nil {
				t.Fatal(err)
			}
			if err := CreateDir(path); err == nil {
				t.Fatal("exclusive creation adopted an existing entry", kind)
			}
			after, err := os.Lstat(path)
			if err != nil || !os.SameFile(before, after) {
				t.Fatal("exclusive creation changed an existing entry", kind, err)
			}
			if kind == "dangling-symlink" {
				if _, err := os.Stat(target); !os.IsNotExist(err) {
					t.Fatal("exclusive creation followed a dangling symlink", err)
				}
			} else if entries, err := os.ReadDir(target); err != nil || len(entries) != 0 {
				t.Fatal("exclusive creation touched an unrelated directory", err)
			}
		})
	}
}
