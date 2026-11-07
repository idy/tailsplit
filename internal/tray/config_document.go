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
		if scalar == nil {
			scalar = &yaml.Node{}
			nested.Content = append(nested.Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: change.Key}, scalar)
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
