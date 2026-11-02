//go:build windows

package tailsplit

import (
	"os"
	"path/filepath"
	"testing"
)

func TestWindowsRuleReplacementPreservesCacheOnFailure(t *testing.T) {
	directory := filepath.Join(t.TempDir(), "state")
	if err := privateState(directory); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(directory, "gfwlist.txt")
	source := filepath.Join(directory, ".new-rules")
	if err := os.WriteFile(target, []byte("last good cache"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(source, []byte("validated replacement"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(target, 0o400); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(target, 0o600) })
	installed, err := commitRuleCache(source, target)
	if err == nil || installed {
		t.Fatal("replacement must report failure for a read-only target")
	}
	if data, err := os.ReadFile(target); err != nil || string(data) != "last good cache" {
		t.Fatal("failed replacement changed the last good cache:", string(data), err)
	}
	if data, err := os.ReadFile(source); err != nil || string(data) != "validated replacement" {
		t.Fatal("failed replacement removed the candidate:", string(data), err)
	}
	if err := os.Chmod(target, 0o600); err != nil {
		t.Fatal(err)
	}
	if installed, err := commitRuleCache(source, target); err != nil || !installed {
		t.Fatal("write-through replacement failed:", installed, err)
	}
	if data, err := os.ReadFile(target); err != nil || string(data) != "validated replacement" {
		t.Fatal("replacement bytes were not installed:", string(data), err)
	}
}
