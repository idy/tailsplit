package tray

import (
	"errors"

	"github.com/idy/tailsplit"
	"gopkg.in/yaml.v3"
)

// PrepareExitAddition checks the saved revision and builds an additional device
// without saving it or opening authentication state. The returned revision binds
// later publication to this document; an empty expectedRevision accepts its
// current revision. Relative paths, comments, aliases and retained policy survive.
func PrepareExitAddition(path, id, expectedRevision string) (candidate []byte, revision string, err error) {
	if !tailsplit.ValidExitID(id) {
		return nil, "", errors.New("invalid new exit ID")
	}
	original, revision, err := ReadConfigDocument(path)
	if err != nil {
		return nil, "", err
	}
	// Do this before validation creates even a private staging file.
	if expectedRevision != "" && revision != expectedRevision {
		return nil, "", ErrConfigChanged
	}
	config, err := ValidateConfigDocument(path, original)
	if err != nil {
		return nil, "", err
	}
	if len(config.Exits) >= 8 {
		return nil, "", errors.New("the maximum number of additional exits has been reached")
	}
	for _, exit := range config.Exits {
		if exit.ID == id {
			return nil, "", errors.New("the exit already exists")
		}
	}
	if config.Rules.Final == id || config.Rules.DefaultExit == id || config.Rules.GlobalExit == id {
		return nil, "", errors.New("new exit ID is reserved by retained routing policy")
	}
	for _, route := range config.Rules.Routes {
		if route.Exit == id {
			return nil, "", errors.New("new exit ID is reserved by retained routing policy")
		}
	}
	// Decode the original list without normalizing implicit hostnames into
	// explicit values. PatchConfigDocument retains the original YAML nodes.
	var raw struct {
		Exits []tailsplit.ExitConfig `yaml:"exits"`
	}
	if err := yaml.Unmarshal(original, &raw); err != nil {
		return nil, "", err
	}
	changes := []ConfigChange{{Key: "exits", Value: append(raw.Exits, tailsplit.ExitConfig{ID: id, AutoName: true})}}
	if len(config.ExitIDs()) == 0 {
		changes = append(changes,
			ConfigChange{Section: "rules", Key: "default_exit", Value: tailsplit.UnsetExit},
			ConfigChange{Section: "rules", Key: "global_exit", Value: tailsplit.UnsetExit})
	}
	candidate, err = PatchConfigDocument(original, changes...)
	if err != nil {
		return nil, "", err
	}
	if _, err := ValidateConfigDocument(path, candidate); err != nil {
		return nil, "", err
	}
	return candidate, revision, nil
}
