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

func stateConnectionSnapshotPath(config tailsplit.Config) string {
	return filepath.Join(config.StateDir, ".tailsplit-connection-snapshots", "config.yaml")
}

func cleanupStartupConnectionSnapshots(configPath string) error {
	err := cleanupConnectionSnapshots(configPath)
	data, _, readErr := readStartupConfigDocument(configPath)
	if readErr != nil {
		return err // Recovery still opens when the saved document cannot be loaded.
	}
	config, configErr := startupConfig(configPath, data)
	if configErr != nil {
		return err
	}
	return errors.Join(err, cleanupConnectionSnapshotsAt(configPath, stateConnectionSnapshotPath(config)))
}
