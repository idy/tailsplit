package tray

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

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

func TestExtraRemovalPreservesOmissionsAliasesAndUnresolvedPolicy(t *testing.T) {
	const original = `tailscale: null
exits:
  - id: office
    name: &label Office
  - id: lab # Surviving device
    name: *label # Surviving label
    exit_node: 100.101.0.10
rules:
  default_exit: lab
  global_exit: office
  final: office # Explicit final policy
  routes:
    - exit: office # Explicit group policy
      domains: [private.example]
`
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(original), 0o600); err != nil {
		t.Fatal(err)
	}
	_, revision, _ := ReadConfigDocument(path)
	if err := RemoveExitDocument(path, "office", revision, func(string) error { return nil }); err != nil {
		t.Fatal(err)
	}
	config, err := tailsplit.LoadDraft(path)
	if err != nil || len(config.Exits) != 1 || config.Exits[0].ID != "lab" || config.Exits[0].Name != "Office" ||
		config.Rules.DefaultExit != "lab" || config.Rules.GlobalExit != tailsplit.UnsetExit || config.Rules.Final != "office" || len(config.Rules.Routes) != 1 || config.Rules.Routes[0].Exit != "office" {
		t.Fatalf("removal changed surviving state or unresolved policy: %+v, %v", config, err)
	}
	if err := tailsplit.ValidateTrafficExit(config); err == nil {
		t.Fatal("a remaining selected gateway hid unresolved Split policy")
	}
	data, _, _ := ReadConfigDocument(path)
	for _, retained := range []string{"&label", "# Surviving device", "# Surviving label", "# Explicit final policy", "# Explicit group policy"} {
		if !strings.Contains(string(data), retained) {
			t.Fatalf("removal lost %q:\n%s", retained, data)
		}
	}
	if strings.Count(string(data), "exit_node:") != 1 || strings.Contains(string(data), "hostname:") {
		t.Fatalf("removal serialized defaults into the surviving profile:\n%s", data)
	}
}

func TestExitRemovalRetainsRulesInheritedFromTopLevelMerge(t *testing.T) {
	const original = `<<: &policy
  rules: &routing
    default_exit: office # Reset selected exit
    global_exit: lab # Keep other selection
    source: ./rules.txt # Keep source
    final: office # Keep unresolved final
    proxy_domains: &proxy_domains ['proxy.example']
    routes:
      - exit: office # Keep unresolved group
        domains: ['corp.example']
      - exit: office
        domains: *proxy_domains # Keep shared policy alias
      - exit: lab
        ips: [192.0.2.0/24]
tailscale: null
exits:
  - id: office
    exit_node: 100.101.0.11
  - id: lab
    exit_node: 100.102.0.12
`
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(original), 0o600); err != nil {
		t.Fatal(err)
	}
	_, revision, _ := ReadConfigDocument(path)
	if err := RemoveExitDocument(path, "office", revision, func(string) error { return nil }); err != nil {
		t.Fatal(err)
	}
	config, err := tailsplit.LoadDraft(path)
	if err != nil || config.Rules.DefaultExit != tailsplit.UnsetExit || config.Rules.GlobalExit != "lab" ||
		config.Rules.Final != "office" || len(config.Rules.ProxyDomains) != 1 || len(config.Rules.Routes) != 3 ||
		config.Rules.Routes[0].Exit != "office" || len(config.Rules.Routes[0].Domains) != 1 ||
		config.Rules.Routes[1].Exit != "office" || len(config.Rules.Routes[1].Domains) != 1 || config.Rules.Routes[1].Domains[0] != "proxy.example" ||
		config.Rules.Routes[2].Exit != "lab" || len(config.Rules.Routes[2].IPs) != 1 {
		t.Fatalf("removal replaced inherited Split policy: %+v, %v", config.Rules, err)
	}
	if err := tailsplit.ValidateTrafficExit(config); !errors.Is(err, tailsplit.ErrUnresolvedSplitExit) {
		t.Fatal("inherited policy no longer blocks traffic after removal:", err)
	}
	data, _, _ := ReadConfigDocument(path)
	for _, text := range []string{"<<: &policy", "&routing", "&proxy_domains", "*proxy_domains", "# Keep shared policy alias", "# Reset selected exit", "# Keep other selection", "source: ./rules.txt # Keep source", "final: office # Keep unresolved final", "# Keep unresolved group", "domains: ['corp.example']"} {
		if !strings.Contains(string(data), text) {
			t.Fatalf("removal lost %q:\n%s", text, data)
		}
	}
	if strings.Contains(string(data), "mode:") || strings.Contains(string(data), "direct_domains:") {
		t.Fatalf("removal serialized omitted defaults:\n%s", data)
	}
}

func TestPrimaryRemovalRetainsUnresolvedPrivateSuffixOwnership(t *testing.T) {
	for _, policy := range []string{"magic", "custom", "both"} {
		t.Run(policy, func(t *testing.T) {
			original := "tailscale:\n  exit_node: 100.64.0.10\n"
			if policy != "magic" {
				original += "  domains: ['service.example'] # Keep custom primary policy\n"
			}
			if policy != "custom" {
				original += "dns:\n  magic_domains: ['primary.ts.net'] # Keep private primary policy\n"
			}
			original += `exits:
  - id: lab
    exit_node: 100.102.0.12
rules:
  default_exit: main
  global_exit: main
  final: direct
  routes:
    - exit: lab # Keep existing group
      domains: ['lab.example']
`
			path := filepath.Join(t.TempDir(), "config.yaml")
			if err := os.WriteFile(path, []byte(original), 0o600); err != nil {
				t.Fatal(err)
			}
			_, revision, _ := ReadConfigDocument(path)
			if err := RemoveExitDocument(path, "main", revision, func(string) error { return nil }); err != nil {
				t.Fatal(err)
			}
			config, err := tailsplit.LoadDraft(path)
			if err != nil || config.Tailscale != nil || len(config.Exits) != 1 || config.Exits[0].ID != "lab" ||
				config.Rules.DefaultExit != tailsplit.UnsetExit || config.Rules.GlobalExit != tailsplit.UnsetExit || config.Rules.Final != "direct" {
				t.Fatalf("main removal changed inventory or selectors: %+v, %v", config, err)
			}
			if policy != "custom" && (len(config.DNS.MagicDomains) != 1 || config.DNS.MagicDomains[0] != "primary.ts.net") {
				t.Fatal("main removal discarded its private suffix:", config.DNS.MagicDomains)
			}
			if policy != "magic" && (len(config.Rules.Routes) != 2 || config.Rules.Routes[1].Exit != "main" ||
				len(config.Rules.Routes[1].Domains) != 1 || config.Rules.Routes[1].Domains[0] != "service.example") {
				t.Fatal("main removal discarded custom primary policy:", config.Rules.Routes)
			}
			// Re-selecting the ready remaining default must not resolve the old owner.
			config.Rules.DefaultExit, config.Rules.GlobalExit = "lab", "lab"
			if err := tailsplit.ValidateTrafficExit(config); !errors.Is(err, tailsplit.ErrUnresolvedSplitExit) {
				t.Fatal("removed primary names can fall through to the ready default:", err)
			}
			if _, err := tailsplit.Compile(config); !errors.Is(err, tailsplit.ErrUnresolvedSplitExit) {
				t.Fatal("compiler accepted orphan primary policy:", err)
			}
			data, _, _ := ReadConfigDocument(path)
			for _, text := range []string{"tailscale: null", "# Keep existing group", "domains: ['lab.example']"} {
				if !strings.Contains(string(data), text) {
					t.Fatalf("removal lost %q:\n%s", text, data)
				}
			}
			if policy != "magic" && !strings.Contains(string(data), "domains: ['service.example'] # Keep custom primary policy") ||
				policy != "custom" && !strings.Contains(string(data), "magic_domains: ['primary.ts.net'] # Keep private primary policy") {
				t.Fatalf("removal lost retained policy comments or scalar style:\n%s", data)
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

func TestAliasExpansionRejectsCompactExponentialDocuments(t *testing.T) {
	for _, test := range []struct {
		name, leaf, limit string
		levels            int
	}{
		{"nodes", "x", "node or byte limit", 30},
		{"bytes", strings.Repeat("x", 4096), "node or byte limit", 30},
		{"depth", "x", "depth limit", 80},
	} {
		t.Run(test.name, func(t *testing.T) {
			var original strings.Builder
			fmt.Fprintf(&original, "base: &a0 [%s]\n", test.leaf)
			for i := 1; i <= test.levels; i++ {
				fmt.Fprintf(&original, "a%d: &a%d [*a%d, *a%d]\n", i, i, i-1, i-1)
			}
			fmt.Fprintf(&original, "items: &selected [*a%d]\nretained: *selected\n", test.levels)
			started := time.Now()
			updated, err := PatchConfigDocument([]byte(original.String()), ConfigChange{Key: "items", Value: nil})
			if err == nil || !strings.Contains(err.Error(), test.limit) || updated != nil {
				t.Fatalf("unbounded alias expansion accepted: %d bytes, %v", len(updated), err)
			}
			if time.Since(started) > 2*time.Second {
				t.Fatal("compact alias expansion did not fail promptly")
			}
			// Inspect the budget independently of elapsed time: no branch can
			// allocate more than the cumulative node/text allowance before failing.
			var document yaml.Node
			if err := yaml.Unmarshal([]byte(original.String()), &document); err != nil {
				t.Fatal(err)
			}
			selected := document.Content[0].Content[len(document.Content[0].Content)-3]
			budget := new(yamlPatchBudget)
			_, err = expandYAMLAliases(selected, make(map[*yaml.Node]bool), budget, 0)
			if err == nil || budget.nodes > maxYAMLExpansionNodes+1 || budget.bytes > MaxConfigBytes+len(test.leaf)+64 || budget.work > maxYAMLPatchWork+1 {
				t.Fatalf("expansion exceeded its allocation budget: %+v, %v", budget, err)
			}
		})
	}
}

func TestAliasExpansionBudgetsAreSharedAcrossPatchChanges(t *testing.T) {
	original := "leaf: &leaf [x]\nbranch: &branch [" + strings.TrimSuffix(strings.Repeat("*leaf, ", 100), ", ") + "]\n"
	for _, key := range []string{"one", "two"} {
		original += key + ": &" + key + " [" + strings.TrimSuffix(strings.Repeat("*branch, ", 90), ", ") + "]\nretained_" + key + ": *" + key + "\n"
	}
	for _, key := range []string{"one", "two"} {
		if _, err := PatchConfigDocument([]byte(original), ConfigChange{Key: key, Value: nil}); err != nil {
			t.Fatalf("one bounded expansion must fit: %s: %v", key, err)
		}
	}
	if data, err := PatchConfigDocument([]byte(original), ConfigChange{Key: "one", Value: nil}, ConfigChange{Key: "two", Value: nil}); err == nil || data != nil || !strings.Contains(err.Error(), "node or byte limit") {
		t.Fatalf("patch changes reset their shared expansion budget: %d bytes, %v", len(data), err)
	}
}

func TestAliasExpansionWorkAndCyclesAreBounded(t *testing.T) {
	for _, test := range []struct {
		name  string
		work  int
		cycle bool
	}{
		{name: "work", work: maxYAMLPatchWork},
		{name: "cycle", cycle: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			node := &yaml.Node{Kind: yaml.SequenceNode}
			if test.cycle {
				node.Content = []*yaml.Node{{Kind: yaml.AliasNode, Alias: node}}
			}
			budget := &yamlPatchBudget{work: test.work}
			if _, err := expandYAMLAliases(node, make(map[*yaml.Node]bool), budget, 0); err == nil {
				t.Fatal("expansion accepted exhausted work or a cycle")
			}
		})
	}
}

func TestConfigPatchBoundsEncodedOutputAndDocumentDepth(t *testing.T) {
	original := []byte("value: " + strings.Repeat("x", MaxConfigBytes/2) + "\n")
	if data, err := PatchConfigDocument(original, ConfigChange{Key: "other", Value: strings.Repeat("x", MaxConfigBytes/2)}); err == nil || data != nil || !strings.Contains(err.Error(), "exceeds 1 MiB") {
		t.Fatalf("oversized output was returned: %d bytes, %v", len(data), err)
	}
	deep := []byte("value: " + strings.Repeat("[", maxYAMLDepth+1) + "x" + strings.Repeat("]", maxYAMLDepth+1) + "\n")
	if _, err := PatchConfigDocument(deep); err == nil || !strings.Contains(err.Error(), "depth limit") {
		t.Fatalf("deep document bypassed the patch depth bound: %v", err)
	}
}
