package helmchartoci

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"

	"gopkg.in/yaml.v3"
)

// ImageMapping mirrors the JSON objects in IMAGE_MAPPINGS.
type ImageMapping struct {
	Source string `json:"source"`
	Target string `json:"target"`
}

// ParseImageMappings decodes the task's JSON array parameter.
func ParseImageMappings(raw string) ([]ImageMapping, error) {
	if raw == "" || raw == "[]" {
		return nil, nil
	}
	var mappings []ImageMapping
	if err := json.Unmarshal([]byte(raw), &mappings); err != nil {
		return nil, fmt.Errorf("parse image mappings: %w", err)
	}
	for i, mapping := range mappings {
		if mapping.Source == "" {
			return nil, fmt.Errorf("parse image mappings: mapping %d: source is required", i)
		}
		if mapping.Target == "" {
			return nil, fmt.Errorf("parse image mappings: mapping %d: target is required", i)
		}
	}
	sort.Slice(mappings, func(i, j int) bool {
		return len(mappings[i].Source) > len(mappings[j].Source)
	})
	return mappings, nil
}

// ApplyImageMappings updates templates/ and values files under chartDir.
// Missing values files are skipped, matching 0.3's `[ -f "$values_file" ]` check.
func ApplyImageMappings(chartDir string, mappings []ImageMapping, valuesFiles []string) error {
	if len(mappings) == 0 {
		return nil
	}

	templatesDir := filepath.Join(chartDir, "templates")
	if info, err := os.Stat(templatesDir); err == nil && info.IsDir() {
		if err := applyTemplateMappings(templatesDir, mappings); err != nil {
			return err
		}
	}

	for _, valuesFile := range valuesFiles {
		path := filepath.Join(chartDir, valuesFile)
		if _, err := os.Stat(path); err != nil {
			continue // absent or inaccessible values file; same as 0.3
		}
		if err := applyValuesMappings(path, mappings); err != nil {
			return err
		}
	}
	return nil
}

// applyTemplateMappings rewrites image fields in templates/*.yaml and templates/*.yml only,
// matching 0.3's `find templates -name "*.yaml" -o -name "*.yml"`.
func applyTemplateMappings(templatesDir string, mappings []ImageMapping) error {
	return filepath.WalkDir(templatesDir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		ext := filepath.Ext(path)
		if ext != ".yaml" && ext != ".yml" {
			return nil
		}
		content, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		updated := string(content)
		for _, mapping := range mappings {
			quoted := regexp.QuoteMeta(mapping.Source)
			// Trailing whitespace is captured as $1 so newlines after image: are preserved.
			pattern := regexp.MustCompile(
				`image:\s*(?:"` + quoted + `"|'` + quoted + `'|` + quoted + `)(\s|$)`,
			)
			updated = pattern.ReplaceAllString(updated, `image: "`+mapping.Target+`"$1`)
		}
		return os.WriteFile(path, []byte(updated), 0o644)
	})
}

func applyValuesMappings(path string, mappings []ImageMapping) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var root yaml.Node
	if err := yaml.Unmarshal(data, &root); err != nil {
		return fmt.Errorf("parse %s: %w", path, err)
	}
	if root.Kind != yaml.DocumentNode || len(root.Content) == 0 {
		return fmt.Errorf("parse %s: empty document", path)
	}

	for _, mapping := range mappings {
		source := splitImageRef(mapping.Source)
		target := splitImageRef(mapping.Target)
		replaceImageNodes(root.Content[0], mapping.Source, mapping.Target, source, target)
	}

	out, err := yaml.Marshal(&root)
	if err != nil {
		return err
	}
	return os.WriteFile(path, out, 0o644)
}

func replaceImageNodes(
	node *yaml.Node,
	sourceImage, targetImage string,
	sourceParts, targetParts imageParts,
) {
	if node == nil {
		return
	}

	switch node.Kind {
	case yaml.MappingNode:
		for i := 0; i+1 < len(node.Content); i += 2 {
			key := node.Content[i]
			value := node.Content[i+1]
			if key.Value == "image" {
				switch value.Kind {
				case yaml.ScalarNode:
					if value.Value == sourceImage {
						value.Value = targetImage
					}
				case yaml.MappingNode:
					replaceRepositoryTagPair(value, sourceParts, targetParts)
				}
			}
			replaceImageNodes(value, sourceImage, targetImage, sourceParts, targetParts)
		}
	case yaml.SequenceNode, yaml.DocumentNode:
		for _, child := range node.Content {
			replaceImageNodes(child, sourceImage, targetImage, sourceParts, targetParts)
		}
	}
}

func replaceRepositoryTagPair(node *yaml.Node, sourceParts, targetParts imageParts) {
	var repoNode, tagNode *yaml.Node
	for i := 0; i+1 < len(node.Content); i += 2 {
		switch node.Content[i].Value {
		case "repository":
			repoNode = node.Content[i+1]
		case "tag":
			tagNode = node.Content[i+1]
		}
	}
	if repoNode == nil {
		return
	}
	tagValue := "latest"
	if tagNode != nil && tagNode.Value != "" {
		tagValue = tagNode.Value
	}
	if repoNode.Value == sourceParts.repo && tagValue == sourceParts.tag {
		repoNode.Value = targetParts.repo
		if tagNode != nil {
			tagNode.SetString(targetParts.tag)
		} else {
			setMappingScalar(node, "tag", targetParts.tag)
		}
	}
}

func setMappingScalar(node *yaml.Node, key, value string) {
	if node == nil || node.Kind != yaml.MappingNode {
		return
	}
	for i := 0; i+1 < len(node.Content); i += 2 {
		if node.Content[i].Value == key {
			node.Content[i+1].SetString(value)
			return
		}
	}
	node.Content = append(node.Content,
		&yaml.Node{Kind: yaml.ScalarNode, Value: key},
		&yaml.Node{Kind: yaml.ScalarNode, Value: value},
	)
}
