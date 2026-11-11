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

func connectionSnapshotFallbackPath(logPath string) (string, error) {
	path, err := filepath.Abs(logPath)
	if err != nil {
		return "", err
	}
	return filepath.Join(filepath.Dir(path), ".tailsplit-connection-snapshots", "config.yaml"), nil
}

func cleanupStartupConnectionSnapshots(configPath, logPath string) error {
	err := cleanupConnectionSnapshots(configPath)
	fallbackPath, pathErr := connectionSnapshotFallbackPath(logPath)
	if pathErr != nil {
		return errors.Join(err, pathErr)
	}
	directory := filepath.Dir(fallbackPath)
	if _, statErr := os.Lstat(directory); errors.Is(statErr, os.ErrNotExist) {
		return err // Do not create snapshot metadata before the first fallback.
	} else if statErr != nil {
		return errors.Join(err, statErr)
	}
	// Check the private root itself; cleanup individually checks its files and
	// must preserve unrelated entries and links rather than recursively adopt them.
	if rootErr := privatefs.EnsureDir(directory, directory); rootErr != nil {
		return errors.Join(err, rootErr)
	}
	return errors.Join(err, cleanupConnectionSnapshotsAt(configPath, fallbackPath))
}
