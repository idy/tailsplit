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

// A patch shares one budget across all alias copies and document traversals.
// Count aliases as work even though they do not themselves allocate a copy.
const (
	maxYAMLPatchWork      = 262144
	maxYAMLExpansionNodes = 32768
	maxYAMLDepth          = 128
)

type yamlPatchBudget struct {
	work, nodes, bytes int
}

func (b *yamlPatchBudget) step(depth int) error {
	b.work++
	if depth > maxYAMLDepth || b.work > maxYAMLPatchWork {
		return errors.New("YAML patch exceeds its work or depth limit")
	}
	return nil
}

func (b *yamlPatchBudget) tree(node *yaml.Node, depth int, visit func(*yaml.Node)) error {
	if err := b.step(depth); err != nil {
		return err
	}
	visit(node)
	for _, child := range node.Content {
		if err := b.tree(child, depth+1, visit); err != nil {
			return err
		}
	}
	return nil
}

// detachScalarAliases keeps aliases bound to the old value when a scalar is
// edited. The first alias owns the preserved anchor; later aliases stay compact.
func detachScalarAliases(document, scalar *yaml.Node, budget *yamlPatchBudget) error {
	if scalar.Kind != yaml.ScalarNode || scalar.Anchor == "" {
		return nil
	}
	var aliases []*yaml.Node
	if err := budget.tree(document, 0, func(node *yaml.Node) {
		if node.Kind == yaml.AliasNode && node.Alias == scalar {
			aliases = append(aliases, node)
		}
	}); err != nil {
		return err
	}
	if len(aliases) == 0 {
		return nil
	}
	preserved := aliases[0]
	head, line, foot := preserved.HeadComment, preserved.LineComment, preserved.FootComment
	*preserved = *scalar
	preserved.HeadComment, preserved.LineComment, preserved.FootComment = head, line, foot
	scalar.Anchor = ""
	for _, alias := range aliases[1:] {
		alias.Alias = preserved
	}
	return nil
}

// preserveAliases only detaches references to nodes actually removed by a patch.
// Reused list entries and their anchors keep their original representation.
func preserveAliases(document, replaced, replacement *yaml.Node, budget *yamlPatchBudget) error {
	if replaced.Kind != yaml.MappingNode && replaced.Kind != yaml.SequenceNode {
		return detachScalarAliases(document, replaced, budget)
	}
	removed := make(map[*yaml.Node]bool)
	if err := budget.tree(replaced, 0, func(node *yaml.Node) { removed[node] = true }); err != nil {
		return err
	}
	if err := budget.tree(replacement, 0, func(node *yaml.Node) { delete(removed, node) }); err != nil {
		return err
	}
	preservedScalars := make(map[*yaml.Node]*yaml.Node)
	var visit func(*yaml.Node, int) error
	visit = func(node *yaml.Node, depth int) error {
		if err := budget.step(depth); err != nil {
			return err
		}
		if removed[node] {
			return nil
		}
		if node.Kind == yaml.AliasNode && removed[node.Alias] {
			target := node.Alias
			if scalar := preservedScalars[target]; scalar != nil {
				node.Alias = scalar
				return nil
			}
			preserved, err := expandYAMLAliases(target, make(map[*yaml.Node]bool), budget, depth)
			if err != nil {
				return err
			}
			if target.Kind == yaml.ScalarNode {
				preserved.Anchor = target.Anchor
				preservedScalars[target] = node
			}
			replaceYAMLAlias(node, preserved)
			return nil
		}
		for _, child := range node.Content {
			if err := visit(child, depth+1); err != nil {
				return err
			}
		}
		return nil
	}
	if err := visit(document, 0); err != nil {
		return err
	}
	return visit(replacement, 0)
}

func expandYAMLAliases(node *yaml.Node, visiting map[*yaml.Node]bool, budget *yamlPatchBudget, depth int) (*yaml.Node, error) {
	if err := budget.step(depth); err != nil {
		return nil, err
	}
	if node == nil || visiting[node] {
		return nil, errors.New("cannot preserve a recursive YAML alias")
	}
	visiting[node] = true
	defer delete(visiting, node)
	if node.Kind == yaml.AliasNode {
		return expandYAMLAliases(node.Alias, visiting, budget, depth+1)
	}
	budget.nodes++
	budget.bytes += len(node.Value) + len(node.Tag) + len(node.HeadComment) + len(node.LineComment) + len(node.FootComment)
	if budget.nodes > maxYAMLExpansionNodes || budget.bytes > MaxConfigBytes {
		return nil, errors.New("YAML alias expansion exceeds its node or byte limit")
	}
	copy := *node
	copy.Anchor, copy.Alias, copy.Content = "", nil, nil
	for _, child := range node.Content {
		preserved, err := expandYAMLAliases(child, visiting, budget, depth+1)
		if err != nil {
			return nil, err
		}
		copy.Content = append(copy.Content, preserved)
	}
	return &copy, nil
}

// yamlFields resolves merge keys without expanding their values. Every walk is
// budgeted, including repeated references to the same anchored mapping.
func yamlFields(node *yaml.Node, budget *yamlPatchBudget, depth int) (map[string]*yaml.Node, error) {
	if err := budget.step(depth); err != nil {
		return nil, err
	}
	if node.Kind == yaml.AliasNode {
		return yamlFields(node.Alias, budget, depth+1)
	}
	if node.Kind != yaml.MappingNode {
		return nil, errors.New("structured YAML list entries must be mappings")
	}
	fields := make(map[string]*yaml.Node)
	for i := 0; i < len(node.Content); i += 2 {
		key, value := node.Content[i], node.Content[i+1]
		if key.Tag != "!!merge" {
			continue
		}
		merges := []*yaml.Node{value}
		if value.Kind == yaml.SequenceNode {
			merges = value.Content
		}
		for _, merge := range merges {
			inherited, err := yamlFields(merge, budget, depth+1)
			if err != nil {
				return nil, err
			}
			for name, field := range inherited {
				if _, exists := fields[name]; !exists {
					fields[name] = field
				}
			}
		}
	}
	for i := 0; i < len(node.Content); i += 2 {
		if key := node.Content[i]; key.Tag != "!!merge" {
			fields[key.Value] = node.Content[i+1]
		}
	}
	return fields, nil
}

func yamlValueEqual(a, b *yaml.Node, identity bool, budget *yamlPatchBudget, depth int) (bool, error) {
	if err := budget.step(depth); err != nil {
		return false, err
	}
	if a == b {
		return true, nil
	}
	if a.Kind == yaml.AliasNode {
		return yamlValueEqual(a.Alias, b, identity, budget, depth+1)
	}
	if b.Kind == yaml.AliasNode {
		return yamlValueEqual(a, b.Alias, identity, budget, depth+1)
	}
	if a.Kind != b.Kind || a.Value != b.Value || (!identity && a.Tag != b.Tag) || len(a.Content) != len(b.Content) {
		return false, nil
	}
	for i := range a.Content {
		equal, err := yamlValueEqual(a.Content[i], b.Content[i], false, budget, depth+1)
		if err != nil || !equal {
			return equal, err
		}
	}
	return true, nil
}

func yamlZeroValue(key string, node *yaml.Node) bool {
	if key == "id" || key == "exit" {
		return false // Even YAML false/0 are nonempty string identities.
	}
	return node.Kind == yaml.ScalarNode && (node.Value == "" || node.Tag == "!!null" || key == "auto_name" && node.Tag == "!!bool" && node.Value == "false") ||
		node.Kind == yaml.SequenceNode && len(node.Content) == 0
}

func yamlEntryEqual(a, b map[string]*yaml.Node, budget *yamlPatchBudget) (bool, error) {
	for key, value := range a {
		other, exists := b[key]
		if !exists {
			if yamlZeroValue(key, value) {
				continue
			}
			return false, nil
		}
		equal, err := yamlValueEqual(value, other, key == "id" || key == "exit", budget, 0)
		if err != nil || !equal {
			return equal, err
		}
	}
	for key, value := range b {
		if _, exists := a[key]; !exists && !yamlZeroValue(key, value) {
			return false, nil
		}
	}
	return true, nil
}

func yamlWithComments(value, original *yaml.Node) *yaml.Node {
	copy := *value
	copy.HeadComment, copy.LineComment, copy.FootComment = original.HeadComment, original.LineComment, original.FootComment
	if value.Kind == original.Kind {
		copy.Style = original.Style
	}
	copy.Anchor = ""
	return &copy
}

// mergeYAMLEntry retains direct fields and merge keys where possible. Editing an
// alias changes that occurrence alone; an omitted inherited field materializes
// the entry so the old merge cannot restore a field the user removed.
func mergeYAMLEntry(old, next *yaml.Node, before, after map[string]*yaml.Node, budget *yamlPatchBudget) (*yaml.Node, error) {
	equal, err := yamlEntryEqual(before, after, budget)
	if err != nil || equal {
		return old, err
	}
	keepMerge := old.Kind == yaml.MappingNode
	hasMerge := false
	for i := 0; i < len(old.Content); i += 2 {
		hasMerge = hasMerge || old.Content[i].Tag == "!!merge"
	}
	for key, value := range before {
		if _, present := after[key]; hasMerge && !present && !yamlZeroValue(key, value) {
			keepMerge = false
		}
	}
	merged := yamlWithComments(next, old)
	merged.Kind, merged.Tag, merged.Content, merged.Alias = yaml.MappingNode, "!!map", nil, nil
	written := make(map[string]bool)
	if keepMerge {
		for i := 0; i < len(old.Content); i += 2 {
			key, value := old.Content[i], old.Content[i+1]
			if key.Tag == "!!merge" {
				merged.Content = append(merged.Content, key, value)
				continue
			}
			incoming, exists := after[key.Value]
			if !exists {
				continue
			}
			same, err := yamlValueEqual(value, incoming, key.Value == "id" || key.Value == "exit", budget, 0)
			if err != nil {
				return nil, err
			}
			if !same {
				value = yamlWithComments(incoming, value)
			}
			merged.Content = append(merged.Content, key, value)
			written[key.Value] = true
		}
	}
	for i := 0; i < len(next.Content); i += 2 {
		key, incoming := next.Content[i], next.Content[i+1]
		if written[key.Value] {
			continue
		}
		previous, exists := before[key.Value]
		if !exists && yamlZeroValue(key.Value, incoming) {
			continue // Do not serialize defaults into an existing entry.
		}
		if exists {
			same, err := yamlValueEqual(previous, incoming, key.Value == "id" || key.Value == "exit", budget, 0)
			if err != nil {
				return nil, err
			}
			if keepMerge && same {
				continue // The retained merge already supplies this field.
			}
			incoming = yamlWithComments(incoming, previous)
		}
		merged.Content = append(merged.Content, key, incoming)
	}
	return merged, nil
}

func mergeYAMLList(old, next *yaml.Node, identity string, budget *yamlPatchBudget) (*yaml.Node, error) {
	sequence := old
	for sequence.Kind == yaml.AliasNode {
		if err := budget.step(0); err != nil {
			return nil, err
		}
		sequence = sequence.Alias
	}
	if sequence.Kind != yaml.SequenceNode || next.Kind != yaml.SequenceNode {
		return next, nil
	}
	before := make([]map[string]*yaml.Node, len(sequence.Content))
	after := make([]map[string]*yaml.Node, len(next.Content))
	for i, entry := range sequence.Content {
		fields, err := yamlFields(entry, budget, 0)
		if err != nil {
			return nil, err
		}
		before[i] = fields
	}
	for i, entry := range next.Content {
		fields, err := yamlFields(entry, budget, 0)
		if err != nil {
			return nil, err
		}
		after[i] = fields
	}
	matches := make([]int, len(after))
	used := make([]bool, len(before))
	for i := range matches {
		matches[i] = -1
	}
	// Match unchanged routes first, so removing an earlier group with the same
	// exit does not transfer its comments to a surviving group.
	for pass := 0; pass < 2; pass++ {
		for i, fields := range after {
			if matches[i] >= 0 {
				continue
			}
			for j, previous := range before {
				if err := budget.step(0); err != nil {
					return nil, err
				}
				if used[j] {
					continue
				}
				equal := false
				if pass == 0 {
					var err error
					equal, err = yamlEntryEqual(previous, fields, budget)
					if err != nil {
						return nil, err
					}
				} else if previous[identity] != nil && fields[identity] != nil {
					var err error
					equal, err = yamlValueEqual(previous[identity], fields[identity], true, budget, 0)
					if err != nil {
						return nil, err
					}
				}
				if equal {
					matches[i], used[j] = j, true
					break
				}
			}
		}
	}
	if identity == "exit" && len(before) == len(after) {
		// With no stable group ID, pair remaining edits in their existing order.
		for i := range matches {
			if matches[i] < 0 {
				for j := range used {
					if !used[j] {
						matches[i], used[j] = j, true
						break
					}
				}
			}
		}
	}
	merged := yamlWithComments(next, old)
	merged.Content = make([]*yaml.Node, len(next.Content))
	unchanged := len(sequence.Content) == len(next.Content)
	for i, entry := range next.Content {
		if j := matches[i]; j >= 0 {
			var err error
			entry, err = mergeYAMLEntry(sequence.Content[j], entry, before[j], after[i], budget)
			if err != nil {
				return nil, err
			}
		}
		merged.Content[i] = entry
		unchanged = unchanged && entry == sequence.Content[i]
	}
	if unchanged {
		return old, nil
	}
	return merged, nil
}

func replaceYAMLAlias(node, preserved *yaml.Node) {
	head, line, foot := node.HeadComment, node.LineComment, node.FootComment
	*node = *preserved
	// yaml.v3 does not emit a mapping value's line comment after an alias is
	// expanded. Keep it immediately above the expanded container instead.
	if (node.Kind == yaml.MappingNode || node.Kind == yaml.SequenceNode) && line != "" {
		head = strings.TrimSpace(head + "\n" + line)
		line = ""
	}
	node.HeadComment, node.LineComment, node.FootComment = head, line, foot
}

// Reordering a list can move an alias before its anchor. Materialize only that
// occurrence; YAML cannot refer forward to an anchor emitted later in the file.
func orderYAMLAliases(node *yaml.Node, seen map[*yaml.Node]bool, budget *yamlPatchBudget, depth int) error {
	if err := budget.step(depth); err != nil {
		return err
	}
	if node.Kind == yaml.AliasNode && !seen[node.Alias] {
		preserved, err := expandYAMLAliases(node.Alias, make(map[*yaml.Node]bool), budget, depth)
		if err != nil {
			return err
		}
		replaceYAMLAlias(node, preserved)
	}
	if node.Anchor != "" {
		seen[node] = true
	}
	for _, child := range node.Content {
		if err := orderYAMLAliases(child, seen, budget, depth+1); err != nil {
			return err
		}
	}
	return nil
}

type configYAMLBuffer struct{ bytes.Buffer }

func (b *configYAMLBuffer) Write(data []byte) (int, error) {
	if len(data) > MaxConfigBytes-b.Len() {
		return 0, errors.New("patched configuration exceeds 1 MiB")
	}
	return b.Buffer.Write(data)
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
	budget := new(yamlPatchBudget)
	if err := budget.tree(&document, 0, func(*yaml.Node) {}); err != nil {
		return nil, err
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
				if err := preserveAliases(&document, legacy, &yaml.Node{}, budget); err != nil {
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
		replacement, isNode := change.Value.(*yaml.Node)
		if !isNode {
			replacement = new(yaml.Node)
			if err := replacement.Encode(change.Value); err != nil {
				return nil, err
			}
		}
		if err := budget.tree(replacement, 0, func(*yaml.Node) {}); err != nil {
			return nil, err
		}
		identity := ""
		if change.Section == "" && change.Key == "exits" {
			identity = "id"
		} else if change.Section == "rules" && change.Key == "routes" {
			identity = "exit"
		}
		if identity != "" {
			var err error
			replacement, err = mergeYAMLList(scalar, replacement, identity, budget)
			if err != nil {
				return nil, err
			}
			if replacement == scalar {
				continue
			}
		}
		if err := preserveAliases(&document, scalar, replacement, budget); err != nil {
			return nil, err
		}
		head, line, foot := scalar.HeadComment, scalar.LineComment, scalar.FootComment
		*scalar = *replacement
		scalar.HeadComment, scalar.LineComment, scalar.FootComment = head, line, foot
	}
	if err := orderYAMLAliases(&document, make(map[*yaml.Node]bool), budget, 0); err != nil {
		return nil, err
	}
	var output configYAMLBuffer
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
	var rawDocument yaml.Node
	if err := yaml.Unmarshal(original, &rawDocument); err != nil {
		return nil, err
	}
	var raw tailsplit.Config
	if err := rawDocument.Decode(&raw); err != nil {
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
	for _, exit := range raw.Exits {
		if strings.TrimSpace(exit.ID) != id {
			remaining = append(remaining, strings.TrimSpace(exit.ID))
		}
	}
	if id != tailsplit.MainExit {
		fields, err := yamlFields(rawDocument.Content[0], new(yamlPatchBudget), 0)
		if err != nil {
			return nil, err
		}
		exits := fields["exits"]
		for exits.Kind == yaml.AliasNode {
			exits = exits.Alias
		}
		retained := *exits
		retained.Content = nil
		for _, entry := range exits.Content {
			var exit tailsplit.ExitConfig
			if err := entry.Decode(&exit); err != nil {
				return nil, err
			}
			if strings.TrimSpace(exit.ID) != id {
				retained.Content = append(retained.Content, entry)
			}
		}
		changes = append(changes, ConfigChange{Key: "exits", Value: &retained})
	}
	// Removing a selected device requires an explicit new choice before traffic.
	fallback := tailsplit.UnsetExit
	if config.Rules.DefaultExit == id || !slices.Contains(remaining, config.Rules.DefaultExit) {
		changes = append(changes, ConfigChange{Section: "rules", Key: "default_exit", Value: fallback})
	}
	if config.Rules.GlobalExit == id || !slices.Contains(remaining, config.Rules.GlobalExit) {
		changes = append(changes, ConfigChange{Section: "rules", Key: "global_exit", Value: fallback})
	}
	// Explicit final and group references are retained as unresolved Split policy.
	// A replacement must be chosen before Split can carry traffic through them;
	// removing a profile must never silently turn that traffic into direct access.
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
