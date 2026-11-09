package tray

import (
	"bytes"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/idy/tailsplit"
	"github.com/idy/tailsplit/internal/privatefs"

	"gopkg.in/yaml.v3"
)

const MaxConfigBytes = 1 << 20

var ErrConfigChanged = errors.New("configuration changed while saving; reload it before trying again")

type ConfigChange struct {
	Section string
	Key     string
	Value   any
}

func ReadConfigDocument(path string) ([]byte, string, error) {
	info, err := os.Lstat(path)
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

// detachScalarAliases keeps aliases bound to the old value when a scalar is
// edited. The first alias owns the preserved anchor; later aliases stay compact.
func detachScalarAliases(document, scalar *yaml.Node) {
	if scalar.Kind != yaml.ScalarNode || scalar.Anchor == "" {
		return
	}
	var aliases []*yaml.Node
	var visit func(*yaml.Node)
	visit = func(node *yaml.Node) {
		if node.Kind == yaml.AliasNode && node.Alias == scalar {
			aliases = append(aliases, node)
			return
		}
		for _, child := range node.Content {
			visit(child)
		}
	}
	visit(document)
	if len(aliases) == 0 {
		return
	}
	preserved := aliases[0]
	head, line, foot := preserved.HeadComment, preserved.LineComment, preserved.FootComment
	*preserved = *scalar
	preserved.HeadComment, preserved.LineComment, preserved.FootComment = head, line, foot
	scalar.Anchor = ""
	for _, alias := range aliases[1:] {
		alias.Alias = preserved
	}
}

// preserveAliases keeps surviving references valid when their anchored mapping
// or sequence is replaced. Scalar anchors retain the compact representation
// handled above; container references are expanded independently of the old tree.
func preserveAliases(document, replaced *yaml.Node) error {
	if replaced.Kind != yaml.MappingNode && replaced.Kind != yaml.SequenceNode {
		detachScalarAliases(document, replaced)
		return nil
	}
	removed := make(map[*yaml.Node]bool)
	var collect func(*yaml.Node)
	collect = func(node *yaml.Node) {
		removed[node] = true
		for _, child := range node.Content {
			collect(child)
		}
	}
	collect(replaced)
	var visit func(*yaml.Node) error
	visit = func(node *yaml.Node) error {
		if removed[node] {
			return nil
		}
		if node.Kind == yaml.AliasNode && removed[node.Alias] {
			preserved, err := expandYAMLAliases(node.Alias, make(map[*yaml.Node]bool))
			if err != nil {
				return err
			}
			head, line, foot := node.HeadComment, node.LineComment, node.FootComment
			*node = *preserved
			node.HeadComment, node.LineComment, node.FootComment = head, line, foot
			return nil
		}
		for _, child := range node.Content {
			if err := visit(child); err != nil {
				return err
			}
		}
		return nil
	}
	return visit(document)
}

func expandYAMLAliases(node *yaml.Node, visiting map[*yaml.Node]bool) (*yaml.Node, error) {
	if node == nil || visiting[node] {
		return nil, errors.New("cannot preserve a recursive YAML alias")
	}
	visiting[node] = true
	defer delete(visiting, node)
	if node.Kind == yaml.AliasNode {
		return expandYAMLAliases(node.Alias, visiting)
	}
	copy := *node
	copy.Anchor, copy.Alias, copy.Content = "", nil, nil
	for _, child := range node.Content {
		preserved, err := expandYAMLAliases(child, visiting)
		if err != nil {
			return nil, err
		}
		copy.Content = append(copy.Content, preserved)
	}
	return &copy, nil
}

// PatchConfigDocument preserves comments and unrelated values in the document.
func PatchConfigDocument(original []byte, changes ...ConfigChange) ([]byte, error) {
	if len(original) > MaxConfigBytes {
		return nil, errors.New("configuration exceeds 1 MiB")
	}
	var document yaml.Node
	decoder := yaml.NewDecoder(bytes.NewReader(original))
	if err := decoder.Decode(&document); err != nil {
		return nil, err
	}
	var extra any
	if decoder.Decode(&extra) != io.EOF || len(document.Content) != 1 || document.Content[0].Kind != yaml.MappingNode {
		return nil, errors.New("configuration must contain one YAML mapping")
	}
	mapping := document.Content[0]
	for _, change := range changes {
		nested := mapping
		if change.Section != "" {
			nested = nil
			for i := 0; i < len(mapping.Content); i += 2 {
				if mapping.Content[i].Value == change.Section {
					nested = mapping.Content[i+1]
					break
				}
			}
			if nested == nil {
				nested = &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
				mapping.Content = append(mapping.Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: change.Section}, nested)
			}
			if nested.Kind != yaml.MappingNode {
				return nil, fmt.Errorf("%s must be a YAML mapping", change.Section)
			}
		}
		var scalar *yaml.Node
		for i := 0; i < len(nested.Content); i += 2 {
			if nested.Content[i].Value == change.Key {
				scalar = nested.Content[i+1]
				break
			}
		}
		// Migrate the old source key only when the user edits this field,
		// retaining its position and comments instead of adding a second source.
		if scalar == nil && change.Section == "rules" && change.Key == "source" {
			for i := 0; i < len(nested.Content); i += 2 {
				if nested.Content[i].Value == "gfwlist" {
					nested.Content[i].Value = change.Key
					scalar = nested.Content[i+1]
					break
				}
			}
		}
		if change.Section == "rules" && change.Key == "source" {
			// Renaming a direct key can also reveal a legacy value inherited
			// through <<. Override it locally without changing shared anchors.
			var inherited struct {
				GFWList string `yaml:"gfwlist"`
			}
			if err := nested.Decode(&inherited); err != nil {
				return nil, err
			}
			if inherited.GFWList != "" {
				var legacy *yaml.Node
				for i := 0; i < len(nested.Content); i += 2 {
					if nested.Content[i].Value == "gfwlist" {
						legacy = nested.Content[i+1]
						break
					}
				}
				if legacy == nil {
					legacy = &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str"}
					nested.Content = append(nested.Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: "gfwlist"}, legacy)
				}
				if err := preserveAliases(&document, legacy); err != nil {
					return nil, err
				}
				// This changes only the local scalar, retaining its comments.
				legacy.Kind, legacy.Tag, legacy.Value, legacy.Alias = yaml.ScalarNode, "!!str", "", nil
			}
		}
		if scalar == nil {
			scalar = &yaml.Node{}
			nested.Content = append(nested.Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: change.Key}, scalar)
		}
		if err := preserveAliases(&document, scalar); err != nil {
			return nil, err
		}
		head, line, foot := scalar.HeadComment, scalar.LineComment, scalar.FootComment
		if err := scalar.Encode(change.Value); err != nil {
			return nil, err
		}
		scalar.HeadComment, scalar.LineComment, scalar.FootComment = head, line, foot
	}
	var output bytes.Buffer
	encoder := yaml.NewEncoder(&output)
	encoder.SetIndent(2)
	if err := encoder.Encode(&document); err != nil {
		return nil, err
	}
	if err := encoder.Close(); err != nil {
		return nil, err
	}
	return output.Bytes(), nil
}

// The temporary file remains beside the real config so relative paths retain
// their meaning. Load applies the same schema checks as the native menu.
func configCandidate(path string, data []byte) (string, tailsplit.Config, error) {
	if len(data) > MaxConfigBytes {
		return "", tailsplit.Config{}, errors.New("configuration exceeds 1 MiB")
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".tailsplit-config-")
	if err != nil {
		return "", tailsplit.Config{}, err
	}
	keep := false
	defer func() {
		if !keep {
			_ = tmp.Close()
			_ = os.Remove(tmp.Name())
		}
	}()
	if _, err := tmp.Write(data); err != nil {
		return "", tailsplit.Config{}, err
	}
	if err := errors.Join(tmp.Sync(), tmp.Close()); err != nil {
		return "", tailsplit.Config{}, err
	}
	config, err := tailsplit.LoadDraft(tmp.Name())
	if err != nil {
		return "", tailsplit.Config{}, err
	}
	origin, _ := tailsplit.Load(path)
	config = config.WithRulesCacheOrigin(origin)
	keep = true
	return tmp.Name(), config, nil
}

func ValidateConfigDocument(path string, data []byte) (tailsplit.Config, error) {
	candidate, config, err := configCandidate(path, data)
	if candidate != "" {
		_ = os.Remove(candidate)
	}
	return config, err
}

func SaveConfigDocument(path, revision string, data []byte) error {
	original, currentRevision, err := ReadConfigDocument(path)
	if err != nil {
		return err
	}
	if currentRevision != revision {
		return ErrConfigChanged
	}
	candidate, _, err := configCandidate(path, data)
	if err != nil {
		return err
	}
	defer os.Remove(candidate)
	current, _, err := ReadConfigDocument(path)
	if err != nil {
		return err
	}
	if !bytes.Equal(original, current) {
		return ErrConfigChanged
	}
	return privatefs.Replace(candidate, path)
}

// CheckExitRemoval validates the request without constructing a discarded patch.
// The controller repeats these checks under its operation gate before logout.
func CheckExitRemoval(path, id, revision string) error {
	_, _, err := readExitRemoval(path, id, revision)
	return err
}

func readExitRemoval(path, id, revision string) ([]byte, tailsplit.Config, error) {
	original, current, err := ReadConfigDocument(path)
	if err != nil {
		return nil, tailsplit.Config{}, err
	}
	if revision == "" || current != revision {
		return nil, tailsplit.Config{}, ErrConfigChanged
	}
	config, err := ValidateConfigDocument(path, original)
	if err != nil {
		return nil, tailsplit.Config{}, err
	}
	if !slices.Contains(config.ExitIDs(), id) {
		return nil, tailsplit.Config{}, errors.New("the selected exit is not configured")
	}
	return original, config, nil
}

// PrepareExitRemoval checks the saved revision before any logout and builds a
// local-only removal. Stable IDs and remaining device state paths never change.
func PrepareExitRemoval(path, id, revision string) ([]byte, error) {
	original, config, err := readExitRemoval(path, id, revision)
	if err != nil {
		return nil, err
	}
	// Patch raw values so normalization does not rewrite unrelated policy,
	// relative paths or automatically generated hostnames in the saved file.
	var raw tailsplit.Config
	if err := yaml.Unmarshal(original, &raw); err != nil {
		return nil, err
	}
	var changes []ConfigChange
	var remaining []string
	if id == tailsplit.MainExit {
		changes = append(changes, ConfigChange{Key: "tailscale", Value: nil})
		// These suffixes belong to the primary device. Keeping them without
		// that client would send its MagicDNS names to an unrelated resolver.
		if len(raw.DNS.MagicDomains) > 0 {
			changes = append(changes, ConfigChange{Section: "dns", Key: "magic_domains", Value: []string{}})
		}
	} else if raw.Tailscale != nil {
		remaining = append(remaining, tailsplit.MainExit)
	}
	var exits []tailsplit.ExitConfig
	for _, exit := range raw.Exits {
		if strings.TrimSpace(exit.ID) != id {
			exits = append(exits, exit)
			remaining = append(remaining, strings.TrimSpace(exit.ID))
		}
	}
	if id != tailsplit.MainExit {
		changes = append(changes, ConfigChange{Key: "exits", Value: exits})
	}
	// Removing a selected device requires an explicit new choice before traffic.
	fallback := tailsplit.UnsetExit
	if config.Rules.DefaultExit == id || !slices.Contains(remaining, config.Rules.DefaultExit) {
		changes = append(changes, ConfigChange{Section: "rules", Key: "default_exit", Value: fallback})
	}
	if config.Rules.GlobalExit == id || !slices.Contains(remaining, config.Rules.GlobalExit) {
		changes = append(changes, ConfigChange{Section: "rules", Key: "global_exit", Value: fallback})
	}
	if strings.TrimSpace(raw.Rules.Final) == id {
		changes = append(changes, ConfigChange{Section: "rules", Key: "final", Value: "direct"})
	}
	var routes []tailsplit.RouteConfig
	for _, route := range raw.Rules.Routes {
		if strings.TrimSpace(route.Exit) != id {
			routes = append(routes, route)
		}
	}
	if len(routes) != len(raw.Rules.Routes) {
		changes = append(changes, ConfigChange{Section: "rules", Key: "routes", Value: routes})
	}
	updated, err := PatchConfigDocument(original, changes...)
	if err != nil {
		return nil, err
	}
	if _, err := ValidateConfigDocument(path, updated); err != nil {
		return nil, err
	}
	return updated, nil
}

// RemoveExitDocument signs out only the target local client, then checks the
// revision again before saving. A failed logout/save leaves the config visible
// for retry. It never removes a remote Tailnet device record or reconnects.
func RemoveExitDocument(path, id, revision string, logout func(string) error) error {
	updated, err := PrepareExitRemoval(path, id, revision)
	if err != nil {
		return err
	}
	if err := logout(id); err != nil {
		return err
	}
	return SaveConfigDocument(path, revision, updated)
}
