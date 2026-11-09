package tray

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/idy/tailsplit"
	"gopkg.in/yaml.v3"
)

func TestPrimaryRemovalPreservesSurvivingContainerAndNestedAliases(t *testing.T) {
	for name, original := range map[string]string{
		"mapping merge":        "tailscale: &device {exit_node: 100.64.0.10}\nexits:\n  - <<: *device # Keep surviving alias comment\n    id: office\n",
		"nested shared anchor": "tailscale: &device {exit_node: &gateway 100.64.0.10}\nexits:\n  - <<: *device\n    id: office\n  - id: lab\n    exit_node: *gateway # Keep surviving alias comment\n",
	} {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "config.yaml")
			if err := os.WriteFile(path, []byte(original), 0o600); err != nil {
				t.Fatal(err)
			}
			before, err := tailsplit.Load(path)
			if err != nil {
				t.Fatal("invalid fixture", err)
			}
			_, revision, _ := ReadConfigDocument(path)
			var target string
			if err := RemoveExitDocument(path, tailsplit.MainExit, revision, func(id string) error { target = id; return nil }); err != nil {
				t.Fatal(err)
			}
			after, err := tailsplit.Load(path)
			if err != nil || target != tailsplit.MainExit || after.Tailscale != nil || len(after.Exits) != len(before.Exits) {
				t.Fatal("primary removal lost surviving profiles", after, err)
			}
			for index, exit := range after.Exits {
				if exit.ExitNode != before.Exits[index].ExitNode || exit.ID != before.Exits[index].ID {
					t.Fatal("surviving alias values changed", after.Exits)
				}
			}
			data, _, _ := ReadConfigDocument(path)
			if !strings.Contains(string(data), "# Keep surviving alias comment") {
				t.Fatal("alias comment was lost", string(data))
			}
		})
	}
}

func TestContainerReplacementPreservesExternalSequenceAliases(t *testing.T) {
	original := []byte("items: &list [one, &shared two]\nretained: *list\nvalue: *shared\n")
	updated, err := PatchConfigDocument(original, ConfigChange{Key: "items", Value: nil})
	if err != nil {
		t.Fatal(err)
	}
	var values struct {
		Retained []string
		Value    string
	}
	if err := yaml.Unmarshal(updated, &values); err != nil {
		t.Fatal(err)
	}
	if len(values.Retained) != 2 || values.Retained[1] != "two" || values.Value != "two" {
		t.Fatal("sequence or nested reference changed", values)
	}
}
