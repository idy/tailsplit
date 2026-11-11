package tray

import (
	"bytes"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/idy/tailsplit"
	"github.com/idy/tailsplit/internal/privatefs"

	"gopkg.in/yaml.v3"
)

// Native startup has historically followed a user-selected config symlink.
// Keep that read-only trust separate from ReadConfigDocument's editing guards.
func readStartupConfigDocument(path string) ([]byte, string, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, "", err
	}
	if !info.Mode().IsRegular() || info.Size() > MaxConfigBytes {
		return nil, "", errors.New("configuration must be a regular file no larger than 1 MiB")
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, "", err
	}
	defer file.Close()
	opened, err := file.Stat()
	if err != nil {
		return nil, "", err
	}
	if !os.SameFile(info, opened) {
		return nil, "", ErrConfigChanged
	}
	data, err := io.ReadAll(io.LimitReader(file, MaxConfigBytes+1))
	if err != nil {
		return nil, "", err
	}
	if len(data) > MaxConfigBytes {
		return nil, "", errors.New("configuration exceeds 1 MiB")
	}
	return data, fmt.Sprintf("%x", sha256.Sum256(data)), nil
}

// Decode the captured bytes rather than reopening a mutable source. Normalizing
// against its argument directory also preserves the base of a symlinked config.
func startupConfig(configPath string, data []byte) (tailsplit.Config, error) {
	if len(data) > MaxConfigBytes {
		return tailsplit.Config{}, errors.New("configuration exceeds 1 MiB")
	}
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	decoder.KnownFields(true)
	var config tailsplit.Config
	if err := decoder.Decode(&config); err != nil {
		return tailsplit.Config{}, err
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return tailsplit.Config{}, errors.New("config.yaml must contain exactly one YAML document")
	}
	base, err := filepath.Abs(filepath.Dir(configPath))
	if err != nil {
		return tailsplit.Config{}, err
	}
	return tailsplit.Normalize(config, base)
}

// Keep snapshot discovery independent of custom log paths and mutable YAML.
func connectionSnapshotFallbackPath() (string, error) {
	root, err := os.UserCacheDir()
	if err != nil {
		return "", err
	}
	root, err = filepath.Abs(root)
	if err != nil {
		return "", err
	}
	// Resolve OS/user path aliases only in the cache base, then use that exact
	// canonical pathname for creation and helper loading. App-owned descendants
	// are never followed through links. StableDir checks the entire resulting
	// ancestry before using or creating any of them.
	var missing []string
	for {
		resolved, err := filepath.EvalSymlinks(root)
		if err == nil {
			for i := len(missing) - 1; i >= 0; i-- {
				resolved = filepath.Join(resolved, missing[i])
			}
			return filepath.Join(resolved, "TailSplit", "connection-snapshots", "config.yaml"), nil
		}
		if !errors.Is(err, os.ErrNotExist) || filepath.Dir(root) == root {
			return "", err
		}
		missing = append(missing, filepath.Base(root))
		root = filepath.Dir(root)
	}
}

func cleanupStartupConnectionSnapshots(configPath string) error {
	err := cleanupConnectionSnapshots(configPath)
	fallbackPath, pathErr := connectionSnapshotFallbackPath()
	if pathErr != nil {
		return errors.Join(err, pathErr)
	}
	// Validate parents before inspecting descendants. Cleanup must neither create
	// missing metadata nor follow a replaceable root into someone else's files.
	if rootErr := privatefs.StableDir(filepath.Dir(fallbackPath), false); errors.Is(rootErr, os.ErrNotExist) {
		return err
	} else if rootErr != nil {
		return errors.Join(err, rootErr)
	}
	return errors.Join(err, cleanupConnectionSnapshotsAt(configPath, fallbackPath))
}
