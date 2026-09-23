package helmchartoci

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"gopkg.in/yaml.v3"
)

// ParseAnnotations converts Tekton-style key=value entries into a map.
// Each entry must contain '='; the value may itself contain '='.
func ParseAnnotations(entries []string) (map[string]string, error) {
	annotations := make(map[string]string, len(entries))
	for _, entry := range entries {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}
		key, value, ok := strings.Cut(entry, "=")
		key = strings.TrimSpace(key)
		if !ok || key == "" {
			return nil, fmt.Errorf("invalid annotation entry %q: expected key=value", entry)
		}
		annotations[key] = value
	}
	return annotations, nil
}

// ApplyChartAnnotations merges annotations into Chart.yaml before packaging.
// Existing keys are overwritten; new keys are added. When annotations is empty,
// Chart.yaml is left unchanged.
//
// If Chart.yaml already has an annotations field that is not a mapping (for
// example a scalar or sequence), it is replaced with a mapping and the prior
// value is discarded. Valid Helm charts use a mapping; this coercion only
// applies to malformed Chart.yaml.
func ApplyChartAnnotations(chartDir string, annotations map[string]string) error {
	if len(annotations) == 0 {
		return nil
	}

	path := filepath.Join(chartDir, "Chart.yaml")
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read Chart.yaml: %w", err)
	}

	var root yaml.Node
	if err := yaml.Unmarshal(data, &root); err != nil {
		return fmt.Errorf("parse Chart.yaml: %w", err)
	}
	if root.Kind != yaml.DocumentNode || len(root.Content) == 0 {
		return fmt.Errorf("parse Chart.yaml: empty document")
	}

	annotationsNode := mappingChild(root.Content[0], "annotations")
	if annotationsNode == nil {
		annotationsNode = addMappingChild(root.Content[0], "annotations")
	}
	keys := make([]string, 0, len(annotations))
	for key := range annotations {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	for _, key := range keys {
		if !setMappingValue(annotationsNode, key, annotations[key]) {
			addMappingEntry(annotationsNode, key, annotations[key])
		}
	}

	out, err := yaml.Marshal(&root)
	if err != nil {
		return err
	}
	return os.WriteFile(path, out, 0o644)
}

func mappingChild(node *yaml.Node, key string) *yaml.Node {
	if node == nil || node.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i+1 < len(node.Content); i += 2 {
		if node.Content[i].Value == key {
			child := node.Content[i+1]
			if child.Kind != yaml.MappingNode {
				child.Kind = yaml.MappingNode
				child.Tag = "!!map"
				child.Content = nil
			}
			return child
		}
	}
	return nil
}

func addMappingChild(node *yaml.Node, key string) *yaml.Node {
	child := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
	node.Content = append(node.Content, &yaml.Node{
		Kind:  yaml.ScalarNode,
		Value: key,
	}, child)
	return child
}

func addMappingEntry(node *yaml.Node, key, value string) {
	keyNode := &yaml.Node{}
	keyNode.SetString(key)
	valueNode := &yaml.Node{}
	valueNode.SetString(value)
	node.Content = append(node.Content, keyNode, valueNode)
}
