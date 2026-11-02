package tray

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
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
		RoutingMode: config.Rules.Mode,
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

func SetSystemDNS(path string) error {
	return updatePreference(path, "dns", "direct", "system")
}

func SetRoutingMode(path, mode string) error {
	if mode != tailsplit.RoutingSplit && mode != tailsplit.RoutingGlobal {
		return errors.New("routing mode must be split or global")
	}
	changes := []preferenceChange{{"rules", "mode", mode}}
	if mode == tailsplit.RoutingGlobal {
		changes = append(changes, preferenceChange{"tun", "enabled", true})
	}
	return updatePreferences(path, changes...)
}

type preferenceChange struct {
	section, key string
	value        any
}

// Edit only the selected YAML scalar, preserving rules, comments and secrets.
// Validate the complete result before atomically replacing the user's file.
func updatePreference(path, section, key string, value any) error {
	return updatePreferences(path, preferenceChange{section, key, value})
}

func updatePreferences(path string, changes ...preferenceChange) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() || info.Size() > 1<<20 {
		return errors.New("configuration must be a regular file no larger than 1 MiB")
	}
	original, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var document yaml.Node
	decoder := yaml.NewDecoder(bytes.NewReader(original))
	if err := decoder.Decode(&document); err != nil {
		return err
	}
	var extra any
	if decoder.Decode(&extra) != io.EOF || len(document.Content) != 1 ||
		document.Content[0].Kind != yaml.MappingNode {
		return errors.New("configuration must contain one YAML mapping")
	}
	mapping := document.Content[0]
	for _, change := range changes {
		section, key, value := change.section, change.key, change.value
		var nested *yaml.Node
		for i := 0; i < len(mapping.Content); i += 2 {
			if mapping.Content[i].Value == section {
				nested = mapping.Content[i+1]
				break
			}
		}
		if nested == nil {
			nested = &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
			mapping.Content = append(mapping.Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: section}, nested)
		}
		if nested.Kind != yaml.MappingNode {
			return fmt.Errorf("%s must be a YAML mapping", section)
		}
		var scalar *yaml.Node
		for i := 0; i < len(nested.Content); i += 2 {
			if nested.Content[i].Value == key {
				scalar = nested.Content[i+1]
				break
			}
		}
		if scalar == nil {
			scalar = &yaml.Node{}
			nested.Content = append(nested.Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key}, scalar)
		}
		comment, lineComment, footComment := scalar.HeadComment, scalar.LineComment, scalar.FootComment
		if err := scalar.Encode(value); err != nil {
			return err
		}
		scalar.HeadComment, scalar.LineComment, scalar.FootComment = comment, lineComment, footComment
	}
	var output bytes.Buffer
	encoder := yaml.NewEncoder(&output)
	encoder.SetIndent(2)
	if err := encoder.Encode(&document); err != nil {
		return err
	}
	if err := encoder.Close(); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".tailsplit-config-")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err = tmp.Write(output.Bytes()); err != nil {
		tmp.Close()
		return err
	}
	if err = errors.Join(tmp.Sync(), tmp.Close()); err != nil {
		return err
	}
	if _, err = tailsplit.Load(tmp.Name()); err != nil {
		return err
	}
	current, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if !bytes.Equal(current, original) {
		return errors.New("configuration changed while saving; try again")
	}
	return os.Rename(tmp.Name(), path)
}
