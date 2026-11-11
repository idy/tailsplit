package configweb

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/idy/tailsplit/internal/privatefs"
)

func peettyCacheSecurityRelease(t *testing.T, version string) *peettyRelease {
	t.Helper()
	release := &peettyRelease{
		Format: peettyCacheFormat, Commit: peettyTestCommit(version), Version: version,
		Files: peettyTestFiles(version, false), Hashes: make(map[string]string),
	}
	for name, body := range release.Files {
		digest := sha256.Sum256(body)
		release.Hashes[name] = base64.StdEncoding.EncodeToString(digest[:])
	}
	if err := preparePeettyRelease(release); err != nil {
		t.Fatal(err)
	}
	return release
}

func peettyCacheSecurityFixture(t *testing.T) (string, *peettyUpdates, []byte) {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	config := filepath.Join(root, "config", "config.yaml")
	u := newPeettyUpdates(config)
	if err := u.save(peettyCacheSecurityRelease(t, "v0.3.1")); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(u.path)
	if err != nil {
		t.Fatal(err)
	}
	u = newPeettyUpdates(config)
	if u.snapshot() == nil || u.snapshot().Version != "v0.3.1" {
		t.Fatal("safe fixture could not be restored offline")
	}
	return config, u, data
}

func assertPeettyCacheUnchanged(t *testing.T, path string, original []byte) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(data, original) {
		t.Fatal("failed save changed the existing cache", err)
	}
	entries, err := os.ReadDir(filepath.Dir(path))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != "current.json" {
		t.Fatalf("save leaked staging files: %v", entries)
	}
}

func TestPeettyCacheSecurityPrivateAtomicReplacementAndBounds(t *testing.T) {
	config, u, original := peettyCacheSecurityFixture(t)
	before, err := os.Lstat(u.path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := readPeettyFile(u.path, int64(len(original)-1)); err == nil {
		t.Fatal("offline cache read ignored the byte limit")
	}
	next := peettyCacheSecurityRelease(t, "v0.3.2")
	if err := u.save(next); err != nil {
		t.Fatal(err)
	}
	after, err := os.Lstat(u.path)
	if err != nil {
		t.Fatal(err)
	}
	if os.SameFile(before, after) {
		t.Fatal("save modified the old cache inode instead of atomically replacing it")
	}
	if err := privatefs.CheckFile(u.path); err != nil {
		t.Fatal("published cache is not private", err)
	}
	cached := newPeettyUpdates(config).snapshot()
	if cached == nil || cached.Version != next.Version {
		t.Fatal("safe replacement was not usable offline")
	}
	published, err := json.Marshal(next)
	if err != nil {
		t.Fatal(err)
	}
	assertPeettyCacheUnchanged(t, u.path, published)
	next.Files["cdn/peetty.global.js"] = []byte(strings.Repeat("x", peettyCacheLimit))
	if err := u.save(next); err == nil {
		t.Fatal("oversized cache was published")
	}
	assertPeettyCacheUnchanged(t, u.path, published)
}

func TestPeettyCacheSecurityOfflineReadDoesNotCreateStorage(t *testing.T) {
	config := filepath.Join(t.TempDir(), "missing", "config.yaml")
	u := newPeettyUpdates(config)
	if u.snapshot() != nil {
		t.Fatal("missing cache became active")
	}
	if _, err := os.Lstat(filepath.Dir(u.path)); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("offline read created cache storage", err)
	}
}

func TestPeettyCacheSecurityRejectsSymlinkedDestination(t *testing.T) {
	config, u, original := peettyCacheSecurityFixture(t)
	target := filepath.Join(filepath.Dir(filepath.Dir(u.path)), "other.json")
	file, err := privatefs.CreateFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.Write(original); err != nil {
		file.Close()
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(u.path); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, u.path); err != nil {
		t.Skip("file symlinks unavailable:", err)
	}
	if newPeettyUpdates(config).snapshot() != nil {
		t.Fatal("symlinked executable cache was accepted")
	}
	if err := u.save(peettyCacheSecurityRelease(t, "v0.3.2")); err == nil {
		t.Fatal("symlinked cache destination was replaced")
	}
	if info, err := os.Lstat(u.path); err != nil || info.Mode()&os.ModeSymlink == 0 {
		t.Fatal("failed save changed the destination symlink", err)
	}
	data, err := os.ReadFile(target)
	if err != nil || !bytes.Equal(data, original) {
		t.Fatal("failed save changed the symlink target", err)
	}
}

func TestPeettyCacheSecurityRejectsAncestorSwap(t *testing.T) {
	_, u, original := peettyCacheSecurityFixture(t)
	parent := filepath.Dir(filepath.Dir(u.path))
	moved := parent + "-moved"
	if err := os.Rename(parent, moved); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(moved, parent); err != nil {
		t.Skip("directory symlinks unavailable:", err)
	}
	// The updater has already pinned its canonical path. Replacing an ancestor
	// with an alias must not let later reads or publication follow that alias.
	if _, err := readPeettyFile(u.path, peettyCacheLimit); err == nil {
		t.Fatal("offline read followed a swapped storage ancestor")
	}
	if err := u.save(peettyCacheSecurityRelease(t, "v0.3.2")); err == nil {
		t.Fatal("save followed a swapped storage ancestor")
	}
	assertPeettyCacheUnchanged(t, filepath.Join(moved, filepath.Base(filepath.Dir(u.path)), "current.json"), original)
}
