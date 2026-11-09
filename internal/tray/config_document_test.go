package tray

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/idy/tailsplit"
	"gopkg.in/yaml.v3"
)

func TestConfigDocumentSaveChecksRevisionAndRetainsLiteralYAML(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	initial := "# User formatting\nlisten: '127.0.0.1:17890'\n"
	if err := os.WriteFile(path, []byte(initial), 0o600); err != nil {
		t.Fatal(err)
	}
	_, revision, err := ReadConfigDocument(path)
	if err != nil {
		t.Fatal(err)
	}
	updated := []byte("# Deliberate raw formatting\nlisten: \"127.0.0.1:17892\"\n\n")
	if err := SaveConfigDocument(path, revision, updated); err != nil {
		t.Fatal(err)
	}
	actual, newRevision, err := ReadConfigDocument(path)
	if err != nil || string(actual) != string(updated) || newRevision == revision {
		t.Fatal("raw edit did not retain its exact text:", string(actual), err)
	}
	if err := SaveConfigDocument(path, revision, []byte(initial)); !errors.Is(err, ErrConfigChanged) {
		t.Fatal("stale editor overwrote a new configuration:", err)
	}
	for _, invalid := range []string{"listen: 0.0.0.0:80\n", "dns:\n  listen: 127.0.0.1:15353\n"} {
		if err := SaveConfigDocument(path, newRevision, []byte(invalid)); err == nil {
			t.Fatalf("invalid configuration was saved: %s", invalid)
		}
	}
	actual, _, _ = ReadConfigDocument(path)
	if string(actual) != string(updated) {
		t.Fatal("failed save modified the current document")
	}
}

func TestRuleSourcePatchOverridesMergedLegacyValues(t *testing.T) {
	for name, rules := range map[string]string{
		"inline merge":    "  <<: {gfwlist: builtin, direct_domains: [example.com]}\n",
		"anchored merge":  "  <<: &defaults\n    gfwlist: builtin # Keep inherited source\n    direct_domains: [example.com]\n",
		"merge sequence":  "  <<: [{gfwlist: builtin}, {direct_domains: [example.com]}]\n",
		"direct override": "  <<: {gfwlist: builtin, direct_domains: [example.com]}\n  gfwlist: https://rules.example/old.txt\n",
	} {
		t.Run(name, func(t *testing.T) {
			original := []byte("tailscale: {}\nrules:\n" + rules)
			path := filepath.Join(t.TempDir(), "config.yaml")
			if _, err := ValidateConfigDocument(path, original); err != nil {
				t.Fatal("invalid legacy fixture:", err)
			}
			for _, source := range []string{"https://rules.example/new.txt", "builtin", ""} {
				updated, err := PatchConfigDocument(original, ConfigChange{"rules", "source", source})
				if err != nil {
					t.Fatal(err)
				}
				config, err := ValidateConfigDocument(path, updated)
				if err != nil || config.Rules.GFWList != source || len(config.Rules.DirectDomains) != 1 || config.Rules.DirectDomains[0] != "example.com" {
					t.Fatalf("merged source edit failed: %+v, %v\n%s", config.Rules, err, updated)
				}
				if !strings.Contains(string(updated), "<<:") || name == "anchored merge" && (!strings.Contains(string(updated), "&defaults") || !strings.Contains(string(updated), "# Keep inherited source")) {
					t.Fatalf("source edit replaced the merge or its comments: %s", updated)
				}
			}
		})
	}
}

func TestRuleSourcePatchPreservesSharedScalarValues(t *testing.T) {
	for name, text := range map[string]string{
		"nullable legacy":    "rules:\n  gfwlist: &subscription builtin\n  source: null\ntailscale:\n  hostname: *subscription # Keep hostname\nstate_dir: ./state\n",
		"legacy rename":      "rules:\n  gfwlist: &subscription builtin\ntailscale:\n  hostname: *subscription # Keep hostname\nstate_dir: ./state\n",
		"canonical source":   "rules:\n  source: &subscription builtin\ntailscale:\n  hostname: *subscription # Keep hostname\nstate_dir: ./state\n",
		"inherited source":   "rules:\n  <<: {gfwlist: &subscription builtin}\n  source: null\ntailscale:\n  hostname: *subscription # Keep hostname\nstate_dir: ./state\n",
		"legacy alias":       "tailscale:\n  hostname: &subscription builtin # Keep hostname\nrules:\n  gfwlist: *subscription\n  source: null\nstate_dir: ./state\n",
		"multiple consumers": "rules:\n  gfwlist: &subscription builtin\n  source: null\ntailscale:\n  hostname: *subscription # Keep hostname\nstate_dir: *subscription\n",
	} {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "config.yaml")
			before, err := ValidateConfigDocument(path, []byte(text))
			if err != nil {
				t.Fatal("invalid shared scalar fixture:", err)
			}
			for _, source := range []string{"https://rules.example/new.txt", "builtin", ""} {
				updated, err := PatchConfigDocument([]byte(text), ConfigChange{"rules", "source", source})
				if err != nil {
					t.Fatal(err)
				}
				after, err := ValidateConfigDocument(path, updated)
				if err != nil || after.Rules.GFWList != source || after.Tailscale == nil || after.Tailscale.Hostname != before.Tailscale.Hostname || after.StateDir != before.StateDir {
					t.Fatalf("source edit changed an aliased field: %+v, %v\n%s", after, err, updated)
				}
				if !strings.Contains(string(updated), "# Keep hostname") || !strings.Contains(string(updated), "&subscription") {
					t.Fatalf("source edit lost a shared anchor or alias comment: %s", updated)
				}
				if name == "multiple consumers" && !strings.Contains(string(updated), "state_dir: *subscription") {
					t.Fatalf("source edit expanded a remaining shared alias: %s", updated)
				}
			}
		})
	}
}

func TestConfigListPatchRetainsEmptyInheritedOverrides(t *testing.T) {
	const original = `tailscale: {}
exits:
  - id: office
    exit_node: 100.101.0.11
  - id: lab
    exit_node: 100.102.0.12
rules:
  routes:
    - &corporate
      exit: office
      domains: ['corp.example'] # Keep private match
    - <<: *corporate # Keep inherited policy
      exit: lab
      domains: [] # Keep empty override
      ips: [192.0.2.0/24]
`
	for _, removeAnchor := range []bool{false, true} {
		t.Run(fmt.Sprintf("remove_anchor=%v", removeAnchor), func(t *testing.T) {
			routes := []tailsplit.RouteConfig{{Exit: "lab", IPs: []string{"198.51.100.0/24"}}}
			if !removeAnchor {
				routes = append([]tailsplit.RouteConfig{{Exit: "office", Domains: []string{"corp.example"}}}, routes...)
			}
			updated, err := PatchConfigDocument([]byte(original), ConfigChange{"rules", "routes", routes})
			if err != nil {
				t.Fatal(err)
			}
			config, err := ValidateConfigDocument(filepath.Join(t.TempDir(), "config.yaml"), updated)
			if err != nil {
				t.Fatal(err)
			}
			lab := config.Rules.Routes[len(config.Rules.Routes)-1]
			if lab.Exit != "lab" || len(lab.Domains) != 0 || len(lab.IPs) != 1 || lab.IPs[0] != "198.51.100.0/24" {
				t.Fatalf("empty override restored an inherited private match: %+v\n%s", lab, updated)
			}
			for _, text := range []string{"domains: []", "# Keep empty override", "# Keep inherited policy"} {
				if !strings.Contains(string(updated), text) {
					t.Fatalf("lost %q:\n%s", text, updated)
				}
			}
			if !removeAnchor && (!strings.Contains(string(updated), "&corporate") || !strings.Contains(string(updated), "*corporate")) {
				t.Fatalf("unrelated edit rewrote the shared anchor:\n%s", updated)
			}
		})
	}
}

func TestConfigListPatchLargeOrdinaryRoutes(t *testing.T) {
	const groups = 800
	for _, operation := range []string{"unchanged", "edit", "delete", "reverse", "duplicates"} {
		t.Run(operation, func(t *testing.T) {
			var original strings.Builder
			original.WriteString("tailscale: {}\nrules:\n  routes:\n")
			routes := make([]tailsplit.RouteConfig, groups)
			for i := range routes {
				domain := fmt.Sprintf("group-%d.example", i)
				if operation == "duplicates" {
					domain = "same.example"
				}
				fmt.Fprintf(&original, "    - exit: main # Group %d\n      domains: ['%s']\n", i, domain)
				routes[i] = tailsplit.RouteConfig{Exit: "main", Domains: []string{domain}}
			}
			switch operation {
			case "edit":
				for i := range routes {
					routes[i].Domains = []string{fmt.Sprintf("edited-%d.example", i)}
				}
			case "delete":
				routes = routes[1:]
			case "reverse":
				for i, j := 0, len(routes)-1; i < j; i, j = i+1, j-1 {
					routes[i], routes[j] = routes[j], routes[i]
				}
			}
			if original.Len() >= MaxConfigBytes {
				t.Fatal("ordinary list fixture exceeds the file cap")
			}
			updated, err := PatchConfigDocument([]byte(original.String()), ConfigChange{"rules", "routes", routes})
			if err != nil {
				t.Fatal("ordinary route list exhausted its patch budget:", err)
			}
			var raw tailsplit.Config
			if err := yaml.Unmarshal(updated, &raw); err != nil {
				t.Fatal(err)
			}
			if len(raw.Rules.Routes) != len(routes) {
				t.Fatal("list edit changed the number of groups")
			}
			for i, route := range raw.Rules.Routes {
				if route.Exit != routes[i].Exit || len(route.Domains) != 1 || route.Domains[0] != routes[i].Domains[0] {
					t.Fatalf("group %d changed: %+v", i, route)
				}
			}
			first := 0
			if operation == "delete" {
				first = 1
			} else if operation == "reverse" {
				first = groups - 1
			}
			if !strings.Contains(string(updated), fmt.Sprintf("exit: main # Group %d\n", first)) ||
				operation != "edit" && !strings.Contains(string(updated), "domains: ['") {
				t.Fatalf("list edit lost source comments or quote style:\n%s", updated)
			}
		})
	}
}

func TestConfigValidationKeepsRelativePathsAndCleansTemporaryFiles(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, "config.yaml")
	config, err := ValidateConfigDocument(path, []byte("state_dir: ./my-state\n"))
	if err != nil || config.StateDir != filepath.Join(directory, "my-state") {
		t.Fatal("validation changed the base for relative paths:", config.StateDir, err)
	}
	if _, err := ValidateConfigDocument(path, []byte("unknown: true\n")); err == nil {
		t.Fatal("unknown field was accepted")
	}
	files, _ := os.ReadDir(directory)
	if len(files) != 0 {
		t.Fatal("validation left configuration candidates on disk")
	}
	if _, err := PatchConfigDocument([]byte(strings.Repeat("x", MaxConfigBytes+1))); err == nil {
		t.Fatal("oversized preview accepted")
	}
}
