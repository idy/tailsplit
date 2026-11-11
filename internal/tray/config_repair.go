package tray

import (
	"bytes"
	"errors"
	"io"

	"gopkg.in/yaml.v3"
)

var ErrConfigNotRepairable = errors.New("configuration cannot be automatically repaired; edit the YAML or rebuild it")

// PrepareConfigRepair removes only obsolete fields with a known replacement
// behavior. Never discard arbitrary unknown fields or guess routing policy.
func PrepareConfigRepair(path string, original []byte) ([]byte, error) {
	if len(original) > MaxConfigBytes {
		return nil, ErrConfigNotRepairable
	}
	var document yaml.Node
	decoder := yaml.NewDecoder(bytes.NewReader(original))
	if err := decoder.Decode(&document); err != nil {
		return nil, ErrConfigNotRepairable
	}
	var extra any
	if decoder.Decode(&extra) != io.EOF || len(document.Content) != 1 || document.Content[0].Kind != yaml.MappingNode {
		return nil, ErrConfigNotRepairable
	}
	budget := new(yamlPatchBudget)
	if err := budget.tree(&document, 0, func(*yaml.Node) {}); err != nil {
		return nil, ErrConfigNotRepairable
	}
	// Keep shared mappings and inherited keys untouched. Such documents remain
	// editable manually rather than silently changing another alias's meaning.
	var dns *yaml.Node
	root := document.Content[0]
	for i := 0; i < len(root.Content); i += 2 {
		if root.Content[i].Value == "dns" {
			if dns != nil {
				return nil, ErrConfigNotRepairable
			}
			dns = root.Content[i+1]
		}
	}
	if dns == nil || dns.Kind != yaml.MappingNode {
		return nil, ErrConfigNotRepairable
	}
	removed := false
	for i := 0; i < len(dns.Content); i += 2 {
		key, value := dns.Content[i], dns.Content[i+1]
		if key.Value != "listen" {
			continue
		}
		if removed {
			return nil, ErrConfigNotRepairable
		}
		if err := preserveAliases(&document, key, &yaml.Node{}, budget); err != nil {
			return nil, ErrConfigNotRepairable
		}
		if err := preserveAliases(&document, value, &yaml.Node{}, budget); err != nil {
			return nil, ErrConfigNotRepairable
		}
		dns.Content = append(dns.Content[:i], dns.Content[i+2:]...)
		removed = true
		i -= 2
	}
	if !removed {
		return nil, ErrConfigNotRepairable
	}
	if err := orderYAMLAliases(&document, make(map[string]*yaml.Node), budget, 0); err != nil {
		return nil, ErrConfigNotRepairable
	}
	var output configYAMLBuffer
	encoder := yaml.NewEncoder(&output)
	encoder.SetIndent(2)
	if err := encoder.Encode(&document); err != nil {
		return nil, ErrConfigNotRepairable
	}
	if err := encoder.Close(); err != nil {
		return nil, ErrConfigNotRepairable
	}
	// Strictly validate the entire result, including untouched unknown keys,
	// malformed values and invalid identities, before offering any repair.
	if _, err := ValidateConfigDocument(path, output.Bytes()); err != nil {
		return nil, ErrConfigNotRepairable
	}
	return output.Bytes(), nil
}

func RepairConfigDocument(path, revision string) (string, error) {
	original, current, err := ReadConfigDocument(path)
	if err != nil {
		return "", err
	}
	if revision == "" || revision != current {
		return "", ErrConfigChanged
	}
	candidate, err := PrepareConfigRepair(path, original)
	if err != nil {
		return "", err
	}
	return backupAndSaveConfigDocument(path, revision, candidate)
}
