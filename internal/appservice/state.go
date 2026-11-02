package appservice

import (
	"errors"
	"fmt"
	"io"
	"net/netip"
	"os"
	"path/filepath"
	"strings"

	"github.com/idy/tailsplit"
)

func runtimeDirectory(base string, uid int) string {
	return filepath.Join(base, "users", fmt.Sprint(uid))
}

func initialized(base string, uid int) bool {
	info, err := os.Lstat(filepath.Join(runtimeDirectory(base, uid), ".initialized"))
	return err == nil && info.Mode().IsRegular()
}

func prepareRuntime(base string, uid int, files Files) (string, error) {
	for _, directory := range []string{base, filepath.Join(base, "users"), runtimeDirectory(base, uid)} {
		if err := privateDirectory(directory); err != nil {
			return "", err
		}
	}
	directory := runtimeDirectory(base, uid)
	if !initialized(base, uid) {
		if len(files.TailscaleState) > 0 {
			if err := privateDirectory(filepath.Join(directory, "tailscale")); err != nil {
				return "", err
			}
			if err := replacePrivate(filepath.Join(directory, "tailscale", "tailscaled.state"), files.TailscaleState); err != nil {
				return "", err
			}
		}
		if len(files.Cache) > 0 {
			if err := replacePrivate(filepath.Join(directory, "cache.db"), files.Cache); err != nil {
				return "", err
			}
		}
		// Keep this marker through logout/reinstall so an old user-state copy
		// cannot silently log the device back in on its next start.
		if err := replacePrivate(filepath.Join(directory, ".initialized"), []byte("1\n")); err != nil {
			return "", err
		}
	}
	if len(files.Rules) > 0 {
		if err := replacePrivate(filepath.Join(directory, "gfwlist.txt"), files.Rules); err != nil {
			return "", err
		}
	} else if err := os.Remove(filepath.Join(directory, "gfwlist.txt")); err != nil && !os.IsNotExist(err) {
		// An old service copy must not override a policy whose ordinary-user
		// source now has no cache (for example, switching back to builtin).
		return "", err
	}
	return directory, nil
}

func replacePrivate(path string, data []byte) (err error) {
	file, err := os.CreateTemp(filepath.Dir(path), ".update-")
	if err != nil {
		return err
	}
	defer func() {
		_ = file.Close()
		_ = os.Remove(file.Name())
	}()
	if _, err = file.Write(data); err != nil {
		return err
	}
	if err = file.Close(); err != nil {
		return err
	}
	return os.Rename(file.Name(), path)
}

// ValidateAppConfig checks the service policy without contacting the service or
// opening saved login state. Local rules are transferred as contents by the app.
func ValidateAppConfig(config tailsplit.Config) error {
	if config.Rules.Mode == tailsplit.RoutingGlobal {
		config.Rules.GFWList = ""
	} else if config.Rules.GFWList != "" && config.Rules.GFWList != "builtin" &&
		!strings.HasPrefix(config.Rules.GFWList, "https://") {
		config.Rules.GFWList = "builtin"
	}
	_, err := serviceConfig(config, config.StateDir)
	return err
}

func serviceConfig(config tailsplit.Config, directory string) (tailsplit.Config, error) {
	// All on-disk engine paths come from this root-owned directory. Config
	// contains no raw sing-box options, hooks, executable paths or plugins.
	config.StateDir = directory
	if config.Tailscale != nil && config.Tailscale.AuthKeyEnv != "" {
		return config, errors.New("the app uses interactive or saved Tailscale login, not service environment keys")
	}
	if config.Rules.GFWList != "" && config.Rules.GFWList != "builtin" &&
		!strings.HasPrefix(config.Rules.GFWList, "https://") {
		return config, errors.New("the service accepts rule contents, not a local rules pathname")
	}
	if config.TUN.Name != "" {
		return config, errors.New("the macOS app lets the system choose its TUN interface name")
	}
	var err error
	config, err = tailsplit.Normalize(config, directory)
	if err != nil {
		return config, err
	}
	for _, value := range []string{config.Listen, config.DNS.Listen} {
		if value == "" {
			continue
		}
		address, _ := netip.ParseAddrPort(value)
		if address.Port() < 1024 {
			return config, errors.New("app proxy and legacy dns.listen ports must be 1024 or above")
		}
	}
	return config, nil
}

func readUserFile(path string, maximum int) ([]byte, error) {
	// This function runs only as the ordinary app user. Do not give the service
	// this pathname, even after checking it.
	file, err := os.Open(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Size() > int64(maximum) {
		return nil, errors.New("saved app state is not a bounded regular file")
	}
	data, err := io.ReadAll(io.LimitReader(file, int64(maximum)+1))
	if len(data) > maximum {
		return nil, errors.New("saved app state exceeds its size limit")
	}
	return data, err
}

func userFiles(config *tailsplit.Config, seed bool) (files Files, err error) {
	if seed {
		files.TailscaleState, err = readUserFile(filepath.Join(config.StateDir, "tailscale", "tailscaled.state"), maxState)
		if err != nil {
			return files, err
		}
		files.Cache, err = readUserFile(filepath.Join(config.StateDir, "cache.db"), maxCache)
		if err != nil {
			return files, err
		}
	}
	if config.Rules.Mode == tailsplit.RoutingGlobal {
		// Global mode never opens or transfers an ignored split-rule source.
		// This changes only the in-memory request, not the user's YAML.
		config.Rules.GFWList = ""
		return files, nil
	}
	rules := config.Rules.GFWList
	if rules == "" {
		return files, nil
	}
	local := rules != "builtin" && !strings.HasPrefix(rules, "https://")
	if !local {
		rules = filepath.Join(config.StateDir, "gfwlist.txt")
	}
	files.Rules, err = readUserFile(rules, maxRules)
	if err != nil {
		return files, err
	}
	if local {
		if len(files.Rules) == 0 {
			return files, errors.New("the local GFWList file is missing or empty")
		}
		// Resolve local rule files as the ordinary user, then send their bounded
		// contents. The privileged process sees only its own fixed cache path.
		config.Rules.GFWList = "builtin"
	}
	return files, err
}
