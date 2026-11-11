//go:build darwin || linux

package configweb

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

type peettyCacheSecurityInfo struct {
	os.FileInfo
	metadata any
}

func (info peettyCacheSecurityInfo) Sys() any { return info.metadata }

func TestPeettyCacheSecurityRequiresCurrentPOSIXOwner(t *testing.T) {
	_, u, _ := peettyCacheSecurityFixture(t)
	info, err := os.Lstat(u.path)
	if err != nil {
		t.Fatal(err)
	}
	if !peettyCacheOwnedByUser(info) {
		t.Fatal("rejected a real current-user cache file")
	}
	// Vary real stat metadata without chown privileges. In particular, even a
	// root-owned 0600 cache is not evidence that this user verified its payload.
	foreign := uint32(os.Geteuid()) + 1
	for name, metadata := range map[string]any{
		"another user": &syscall.Stat_t{Uid: foreign},
		"absent":       nil,
		"typed nil":    (*syscall.Stat_t)(nil),
		"unknown":      struct{}{},
		"wrong type":   struct{ Uid string }{Uid: "0"},
	} {
		t.Run(name, func(t *testing.T) {
			if peettyCacheOwnedByUser(peettyCacheSecurityInfo{FileInfo: info, metadata: metadata}) {
				t.Fatal("accepted foreign or unverifiable ownership")
			}
		})
	}
	if os.Geteuid() != 0 && peettyCacheOwnedByUser(peettyCacheSecurityInfo{FileInfo: info, metadata: &syscall.Stat_t{Uid: 0}}) {
		t.Fatal("accepted a different trusted ancestor owner as the cache file owner")
	}
}

func TestPeettyCacheSecurityRejectsWritableDirectories(t *testing.T) {
	for _, target := range []string{"cache", "ancestor"} {
		for _, mode := range []os.FileMode{0o770, 0o777} {
			t.Run(target+"-"+mode.String(), func(t *testing.T) {
				config, u, original := peettyCacheSecurityFixture(t)
				directory := filepath.Dir(u.path)
				if target == "ancestor" {
					directory = filepath.Dir(directory)
				}
				if err := os.Chmod(directory, mode); err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = os.Chmod(directory, 0o700) })
				if newPeettyUpdates(config).snapshot() != nil {
					t.Fatal("unsafe directory allowed executable offline cache reuse")
				}
				if err := u.save(peettyCacheSecurityRelease(t, "v0.3.2")); err == nil {
					t.Fatal("unsafe directory allowed publication")
				}
				assertPeettyCacheUnchanged(t, u.path, original)
				if info, err := os.Stat(directory); err != nil || info.Mode().Perm() != mode {
					t.Fatal("cache validation changed an existing directory's permissions", err)
				}
			})
		}
	}
}
