package privatefs

import (
	"errors"
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

func TestDarwinPrivateFileRejectsNULWithoutOpeningPrefix(t *testing.T) {
	prefix := filepath.Join(t.TempDir(), "must-not-exist")
	file, err := CreateFile(prefix + "\x00ignored")
	if file != nil {
		_ = file.Close()
		t.Fatal("invalid native pathname returned an open file")
	}
	if !errors.Is(err, syscall.EINVAL) {
		t.Fatal("invalid pathname did not retain os.OpenFile semantics", err)
	}
	if _, err := os.Lstat(prefix); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("native API truncated the pathname and created its prefix", err)
	}
}
