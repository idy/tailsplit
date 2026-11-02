package tray

import (
	"errors"
	"os"
	"strings"

	"github.com/idy/tailsplit"

	"gopkg.in/yaml.v3"
)

type Preferences struct {
	DeviceName     string
	ExitNode       string
	ConnectOnStart bool
	DirectDNS      string
	RoutingMode    string
	DebugLogging   bool
}

func ReadPreferences(path string) (Preferences, error) {
	config, err := tailsplit.Load(path)
	if err != nil {
		return Preferences{}, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return Preferences{}, err
	}
	var raw struct {
		App       tailsplit.AppConfig `yaml:"app"`
		Tailscale *struct {
			Hostname string `yaml:"hostname"`
			ExitNode string `yaml:"exit_node"`
		} `yaml:"tailscale"`
	}
	if err := yaml.Unmarshal(data, &raw); err != nil {
		return Preferences{}, err
	}
	p := Preferences{
		ConnectOnStart: raw.App.ConnectOnStart, DirectDNS: config.DNS.Direct,
		RoutingMode:  config.Rules.Mode,
		DebugLogging: config.LogLevel == "debug" || config.LogLevel == "trace",
	}
	if raw.Tailscale != nil {
		p.DeviceName = raw.Tailscale.Hostname
		p.ExitNode = raw.Tailscale.ExitNode
	}
	return p, nil
}

func DeviceName(configured string) (string, error) { return tailsplit.DeviceHostname(configured) }

func SetDeviceName(path, value string) error {
	if _, err := DeviceName(value); err != nil {
		return err
	}
	return updatePreference(path, "tailscale", "hostname", strings.TrimSpace(value))
}

func SetExitNode(path, value string) error {
	if strings.TrimSpace(value) == "" {
		return errors.New("select an exit node")
	}
	return updatePreference(path, "tailscale", "exit_node", value)
}

func SetConnectOnStart(path string, enabled bool) error {
	return updatePreference(path, "app", "connect_on_start", enabled)
}

func SetDebugLogging(path string, enabled bool) error {
	level := "info"
	if enabled {
		level = "debug"
	}
	return updatePreference(path, "", "log_level", level)
}

func SetSystemDNS(path string) error {
	return updatePreference(path, "dns", "direct", "system")
}

func SetRoutingMode(path, mode string) error {
	if mode != tailsplit.RoutingSplit && mode != tailsplit.RoutingGlobal {
		return errors.New("routing mode must be split or global")
	}
	changes := []ConfigChange{{"rules", "mode", mode}}
	if mode == tailsplit.RoutingGlobal {
		changes = append(changes, ConfigChange{"tun", "enabled", true})
	}
	return updatePreferences(path, changes...)
}

func updatePreference(path, section, key string, value any) error {
	return updatePreferences(path, ConfigChange{section, key, value})
}

func updatePreferences(path string, changes ...ConfigChange) error {
	original, revision, err := ReadConfigDocument(path)
	if err != nil {
		return err
	}
	updated, err := PatchConfigDocument(original, changes...)
	if err != nil {
		return err
	}
	return SaveConfigDocument(path, revision, updated)
}
