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
	"github.com/idy/tailsplit/internal/privatefs"
)

func runtimeDirectory(base string, uid int) string {
	return runtimeUserDirectory(base, fmt.Sprint(uid))
}

func runtimeUserDirectory(base, user string) string { return filepath.Join(base, "users", user) }

func initialized(base string, uid int) bool { return runtimeInitialized(runtimeDirectory(base, uid)) }

func runtimeInitialized(directory string) bool {
	info, err := os.Lstat(filepath.Join(directory, ".initialized"))
	return err == nil && info.Mode().IsRegular()
}

// Older global markers also represented primary ownership, including logout.
// Version 2 separates that authority from cache initialization. Never reinterpret
// an old or unknown marker as permission to restore a possibly logged-out device.
const identityInitialization = "2\n"

func stateOwned(directory, id string) (bool, error) {
	paths := []string{filepath.Join(directory, "exits", id)}
	if id == tailsplit.MainExit {
		paths = []string{filepath.Join(directory, ".primary-initialized"), filepath.Join(directory, "tailscale")}
	}
	for _, path := range paths {
		if _, err := os.Lstat(path); err == nil {
			return true, nil
		} else if !os.IsNotExist(err) {
			return false, err
		}
	}
	if id == tailsplit.MainExit && runtimeInitialized(directory) {
		marker, err := os.Open(filepath.Join(directory, ".initialized"))
		if err != nil {
			return true, nil
		}
		defer marker.Close()
		data, err := io.ReadAll(io.LimitReader(marker, int64(len(identityInitialization)+1)))
		// Unknown/oversized old markers are conservative primary authority too.
		return err != nil || string(data) != identityInitialization, nil
	}
	return false, nil
}

func runtimeImports(directory string, ids []string) (ImportPlan, error) {
	if err := validateImportIDs(ids); err != nil {
		return ImportPlan{}, err
	}
	plan := ImportPlan{Cache: !runtimeInitialized(directory)}
	for _, id := range ids {
		owned, err := stateOwned(directory, id)
		if err != nil {
			return ImportPlan{}, err
		}
		if !owned {
			plan.IDs = append(plan.IDs, id)
		}
	}
	return plan, nil
}

func prepareRuntime(base string, uid int, files Files) (string, error) {
	return prepareRuntimeUser(base, fmt.Sprint(uid), files)
}

func prepareRuntimeUser(base, user string, files Files) (string, error) {
	return prepareRuntimeUserWithRules(base, user, files, true)
}

func validateExitInventory(count int) error {
	if count > maxExits {
		return fmt.Errorf("the network service supports at most %d additional exits", maxExits)
	}
	return nil
}

func prepareRuntimeUserWithRules(base, user string, files Files, updateRules bool) (string, error) {
	if user == "" || user == "." || user == ".." || strings.ContainsAny(user, `/\\:`) {
		return "", errors.New("invalid registered service user")
	}
	if err := validateFiles(files); err != nil {
		return "", err
	}
	directory := runtimeUserDirectory(base, user)
	for _, path := range []string{base, filepath.Join(base, "users"), directory} {
		if err := privateDirectory(path); err != nil {
			return "", err
		}
	}
	first := !runtimeInitialized(directory)
	if len(files.TailscaleState) > 0 {
		owned, err := stateOwned(directory, tailsplit.MainExit)
		if err != nil {
			return "", err
		}
		if !owned {
			if err := publishState(directory, filepath.Join(directory, "tailscale"), "", files.TailscaleState); err != nil {
				return "", err
			}
		}
	}
	for id, state := range files.ExitStates {
		if len(state) == 0 {
			continue
		}
		if err := importExitState(directory, id, state); err != nil {
			return "", err
		}
	}
	if first {
		if len(files.Cache) > 0 {
			if err := replacePrivate(filepath.Join(directory, "cache.db"), files.Cache); err != nil {
				return "", err
			}
		}
		// Cache initialization no longer claims an unused primary device.
		if err := replacePrivate(filepath.Join(directory, ".initialized"), []byte(identityInitialization)); err != nil {
			return "", err
		}
	}
	if !updateRules {
		return directory, nil
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

// prepareNewExitRuntime runs under the service's active/lifetime leases. Fresh
// authentication never imports user files or changes the saved rules/cache. Its
// namespace claim remains authoritative even if the runner or publication fails.
func prepareNewExitRuntime(base, user string, config tailsplit.Config, files Files) error {
	if !validExitID(config.Focus) {
		return errors.New("new device authentication requires a configured additional exit")
	}
	if _, err := config.FocusExit(config.Focus); err != nil {
		return err
	}
	if len(files.TailscaleState) != 0 || len(files.ExitStates) != 0 || len(files.Cache) != 0 || len(files.Rules) != 0 {
		return errors.New("new device authentication cannot import saved files")
	}
	directory, err := prepareRuntimeUserWithRules(base, user, Files{}, false)
	if err != nil {
		return err
	}
	config.StateDir = directory
	return tailsplit.ReserveNewExitState(config)
}

// importExitState publishes an identity and its authority together. An existing
// exit directory belongs to the service, including directories made by older
// helpers that have no marker or whose logout removed the state file. Never
// replace those identities with a possibly stale ordinary-user copy.
func importExitState(directory, id string, state []byte) error {
	exits := filepath.Join(directory, "exits")
	if err := privateDirectory(exits); err != nil {
		return err
	}
	destination := filepath.Join(exits, id)
	if _, err := os.Lstat(destination); err == nil {
		return privateDirectory(destination)
	} else if !os.IsNotExist(err) {
		return err
	}
	return publishState(exits, destination, "tailscale", state)
}

// Publish the complete state directory by rename, so a failed import never
// exposes an empty directory that a later request would treat as authoritative.
func publishState(parent, destination, stateSubdir string, state []byte) error {
	staged, err := os.MkdirTemp(parent, ".import-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(staged)
	// Recreate the reserved name through the platform's private-directory
	// helper so Windows gives the published exit a protected root ACL too.
	if err := os.Remove(staged); err != nil {
		return err
	}
	if err := privateDirectory(staged); err != nil {
		return err
	}
	stateDirectory := filepath.Join(staged, stateSubdir)
	if err := privateDirectory(stateDirectory); err != nil {
		return err
	}
	if err := replacePrivate(filepath.Join(stateDirectory, "tailscaled.state"), state); err != nil {
		return err
	}
	if err := replacePrivate(filepath.Join(staged, ".initialized"), []byte("1\n")); err != nil {
		return err
	}
	return os.Rename(staged, destination)
}

// claimLogoutExit runs before the logout runner: it may remove its state file,
// return an error, or be cancelled without ever creating one. Retain authority
// outside the engine's tailscale directory even when no legacy state arrived.
// Primary authority also lives outside its engine-owned directory.
func claimLogoutExit(config tailsplit.Config) error {
	id := config.Focus
	if id == "" || id == tailsplit.MainExit {
		return replacePrivate(filepath.Join(config.StateDir, ".primary-initialized"), []byte("1\n"))
	}
	if !validExitID(id) {
		return errors.New("invalid logout exit")
	}
	directory := filepath.Join(config.StateDir, "exits", id)
	for _, path := range []string{filepath.Join(config.StateDir, "exits"), directory} {
		if err := privateDirectory(path); err != nil {
			return err
		}
	}
	return replacePrivate(filepath.Join(directory, ".initialized"), []byte("1\n"))
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
	if err = file.Sync(); err != nil {
		return err
	}
	if err = file.Close(); err != nil {
		return err
	}
	return privatefs.Replace(file.Name(), path)
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
	if err := validateExitInventory(len(config.Exits)); err != nil {
		return config, err
	}
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
		return config, errors.New("the desktop app lets the system choose its TUN interface name")
	}
	var err error
	config, err = tailsplit.Normalize(config, directory)
	if err != nil {
		return config, err
	}
	address, _ := netip.ParseAddrPort(config.Listen)
	if address.Port() < 1024 {
		return config, errors.New("app proxy port must be 1024 or above")
	}
	if config.Rules.GFWList != "" {
		// The app transfers rule contents. The service never downloads a URL or
		// falls back to a remote source when the transferred cache is missing.
		config.Rules.GFWList = filepath.Join(directory, "gfwlist.txt")
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

func userFiles(config *tailsplit.Config, plan ImportPlan) (files Files, err error) {
	// Validate the full inventory even when this operation uses only one device.
	if err := validateExitInventory(len(config.Exits)); err != nil {
		return Files{}, err
	}
	if err := validateImportIDs(plan.IDs); err != nil {
		return Files{}, err
	}
	if plan.Cache {
		files.Cache, err = readUserFile(filepath.Join(config.StateDir, "cache.db"), maxCache)
		if err != nil {
			return files, err
		}
	}
	for _, id := range plan.IDs {
		state, err := readUserFile(filepath.Join(config.ExitStateDir(id), "tailscaled.state"), maxState)
		if err != nil {
			return Files{}, err
		}
		if id == tailsplit.MainExit {
			files.TailscaleState = state
		} else if len(state) > 0 {
			if files.ExitStates == nil {
				files.ExitStates = map[string][]byte{}
			}
			files.ExitStates[id] = state
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
		rules = tailsplit.RulesCachePath(*config)
	}
	files.Rules, err = readUserFile(rules, maxRules)
	if err != nil {
		return files, err
	}
	if local {
		if len(files.Rules) == 0 {
			return files, errors.New("the local rule list is missing or empty")
		}
		// Resolve local rule files as the ordinary user, then send their bounded
		// contents. The privileged process sees only its own fixed cache path.
		config.Rules.GFWList = "builtin"
	}
	return files, err
}
