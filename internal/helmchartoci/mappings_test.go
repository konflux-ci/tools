package helmchartoci_test

import (
	"os"
	"path/filepath"

	"github.com/konflux-ci/tools/internal/helmchartoci"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"gopkg.in/yaml.v3"
)

var _ = Describe("Image mappings", func() {
	It("sorts mappings by longest source first", func() {
		mappings, err := helmchartoci.ParseImageMappings(`[
			{"source":"localhost/app","target":"quay.io/org/app"},
			{"source":"localhost/app/sub","target":"quay.io/org/app-sub"}
		]`)
		Expect(err).NotTo(HaveOccurred())
		Expect(mappings).To(HaveLen(2))
		Expect(mappings[0].Source).To(Equal("localhost/app/sub"))
	})

	It("returns nil for empty mappings", func() {
		mappings, err := helmchartoci.ParseImageMappings("")
		Expect(err).NotTo(HaveOccurred())
		Expect(mappings).To(BeNil())
	})

	It("returns error for invalid JSON", func() {
		_, err := helmchartoci.ParseImageMappings("{")
		Expect(err).To(HaveOccurred())
	})

	It("returns error for empty mapping source", func() {
		_, err := helmchartoci.ParseImageMappings(`[{"source":"","target":"quay.io/org/app"}]`)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("source is required"))
	})

	It("returns error for empty mapping target", func() {
		_, err := helmchartoci.ParseImageMappings(`[{"source":"localhost/app","target":""}]`)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("target is required"))
	})

	It("is a no-op when mappings are empty", func() {
		Expect(helmchartoci.ApplyImageMappings(GinkgoT().TempDir(), nil, nil)).To(Succeed())
	})

	It("skips missing values files", func() {
		chartDir := GinkgoT().TempDir()
		mappings := []helmchartoci.ImageMapping{
			{Source: "localhost/app", Target: "quay.io/org/app"},
		}
		Expect(helmchartoci.ApplyImageMappings(chartDir, mappings, []string{"missing.yaml"})).To(Succeed())
	})

	It("returns error for invalid values YAML", func() {
		chartDir := GinkgoT().TempDir()
		Expect(os.WriteFile(filepath.Join(chartDir, "values.yaml"), []byte("{"), 0o644)).To(Succeed())
		mappings := []helmchartoci.ImageMapping{
			{Source: "localhost/app", Target: "quay.io/org/app"},
		}
		Expect(helmchartoci.ApplyImageMappings(chartDir, mappings, []string{"values.yaml"})).NotTo(Succeed())
	})

	It("updates templates and structured values", func() {
		chartDir := GinkgoT().TempDir()
		templatesDir := filepath.Join(chartDir, "templates")
		Expect(os.MkdirAll(templatesDir, 0o755)).To(Succeed())
		template := `apiVersion: v1
kind: Deployment
spec:
  template:
    spec:
      containers:
        - image: localhost/my/repo:old
`
		Expect(os.WriteFile(filepath.Join(templatesDir, "deploy.yaml"), []byte(template), 0o644)).To(Succeed())

		values := map[string]any{
			"image": map[string]any{
				"repository": "localhost/my/repo",
				"tag":        "old",
			},
		}
		valuesData, err := yaml.Marshal(values)
		Expect(err).NotTo(HaveOccurred())
		Expect(os.WriteFile(filepath.Join(chartDir, "values.yaml"), valuesData, 0o644)).To(Succeed())

		mappings := []helmchartoci.ImageMapping{
			{Source: "localhost/my/repo:old", Target: "quay.io/myorg/myapp:new"},
		}
		Expect(helmchartoci.ApplyImageMappings(chartDir, mappings, []string{"values.yaml"})).To(Succeed())

		updatedTemplate, err := os.ReadFile(filepath.Join(templatesDir, "deploy.yaml"))
		Expect(err).NotTo(HaveOccurred())
		Expect(string(updatedTemplate)).To(ContainSubstring(`image: "quay.io/myorg/myapp:new"`))

		updatedValues, err := os.ReadFile(filepath.Join(chartDir, "values.yaml"))
		Expect(err).NotTo(HaveOccurred())
		var parsed map[string]any
		Expect(yaml.Unmarshal(updatedValues, &parsed)).To(Succeed())
		image := parsed["image"].(map[string]any)
		Expect(image["repository"]).To(Equal("quay.io/myorg/myapp"))
		Expect(image["tag"]).To(Equal("new"))
	})

	It("handles registry-port repositories in values", func() {
		chartDir := GinkgoT().TempDir()
		values := map[string]any{
			"image": map[string]any{
				"repository": "registry.example:5000/team/app",
				"tag":        "1.2",
			},
		}
		valuesData, err := yaml.Marshal(values)
		Expect(err).NotTo(HaveOccurred())
		Expect(os.WriteFile(filepath.Join(chartDir, "values.yaml"), valuesData, 0o644)).To(Succeed())

		mappings := []helmchartoci.ImageMapping{
			{Source: "registry.example:5000/team/app:1.2", Target: "quay.io/org/app:2.0"},
		}
		Expect(helmchartoci.ApplyImageMappings(chartDir, mappings, []string{"values.yaml"})).To(Succeed())

		updatedValues, err := os.ReadFile(filepath.Join(chartDir, "values.yaml"))
		Expect(err).NotTo(HaveOccurred())
		var parsed map[string]any
		Expect(yaml.Unmarshal(updatedValues, &parsed)).To(Succeed())
		image := parsed["image"].(map[string]any)
		Expect(image["repository"]).To(Equal("quay.io/org/app"))
		Expect(image["tag"]).To(Equal("2.0"))
	})

	It("inserts target tag when values omit tag key", func() {
		chartDir := GinkgoT().TempDir()
		values := map[string]any{
			"image": map[string]any{
				"repository": "localhost/app",
			},
		}
		valuesData, err := yaml.Marshal(values)
		Expect(err).NotTo(HaveOccurred())
		Expect(os.WriteFile(filepath.Join(chartDir, "values.yaml"), valuesData, 0o644)).To(Succeed())

		mappings := []helmchartoci.ImageMapping{
			{Source: "localhost/app", Target: "quay.io/org/app:v2.0"},
		}
		Expect(helmchartoci.ApplyImageMappings(chartDir, mappings, []string{"values.yaml"})).To(Succeed())

		updatedValues, err := os.ReadFile(filepath.Join(chartDir, "values.yaml"))
		Expect(err).NotTo(HaveOccurred())
		var parsed map[string]any
		Expect(yaml.Unmarshal(updatedValues, &parsed)).To(Succeed())
		image := parsed["image"].(map[string]any)
		Expect(image["repository"]).To(Equal("quay.io/org/app"))
		Expect(image["tag"]).To(Equal("v2.0"))
	})

	It("preserves template structure after image replacement", func() {
		chartDir := GinkgoT().TempDir()
		templatesDir := filepath.Join(chartDir, "templates")
		Expect(os.MkdirAll(templatesDir, 0o755)).To(Succeed())
		template := `apiVersion: v1
kind: Deployment
spec:
  template:
    spec:
      containers:
        - image: localhost/my/repo:old
          name: app
`
		Expect(os.WriteFile(filepath.Join(templatesDir, "deploy.yaml"), []byte(template), 0o644)).To(Succeed())

		mappings := []helmchartoci.ImageMapping{
			{Source: "localhost/my/repo:old", Target: "quay.io/myorg/myapp:new"},
		}
		Expect(helmchartoci.ApplyImageMappings(chartDir, mappings, nil)).To(Succeed())

		updatedTemplate, err := os.ReadFile(filepath.Join(templatesDir, "deploy.yaml"))
		Expect(err).NotTo(HaveOccurred())
		var parsed map[string]any
		Expect(yaml.Unmarshal(updatedTemplate, &parsed)).To(Succeed())
		spec := parsed["spec"].(map[string]any)
		podSpec := spec["template"].(map[string]any)["spec"].(map[string]any)
		containers := podSpec["containers"].([]any)
		container := containers[0].(map[string]any)
		Expect(container["image"]).To(Equal("quay.io/myorg/myapp:new"))
		Expect(container["name"]).To(Equal("app"))
	})

	It("does not partially match template image prefixes", func() {
		chartDir := GinkgoT().TempDir()
		templatesDir := filepath.Join(chartDir, "templates")
		Expect(os.MkdirAll(templatesDir, 0o755)).To(Succeed())
		template := `apiVersion: v1
kind: Pod
spec:
  containers:
    - image: quay.io/org/apple:latest
`
		Expect(os.WriteFile(filepath.Join(templatesDir, "deploy.yaml"), []byte(template), 0o644)).To(Succeed())

		mappings := []helmchartoci.ImageMapping{
			{Source: "quay.io/org/app", Target: "quay.io/org/target"},
		}
		Expect(helmchartoci.ApplyImageMappings(chartDir, mappings, nil)).To(Succeed())

		updatedTemplate, err := os.ReadFile(filepath.Join(templatesDir, "deploy.yaml"))
		Expect(err).NotTo(HaveOccurred())
		Expect(string(updatedTemplate)).To(Equal(template))
	})

	It("updates scalar values image fields", func() {
		chartDir := GinkgoT().TempDir()
		values := map[string]any{
			"image": "localhost/app:old",
		}
		valuesData, err := yaml.Marshal(values)
		Expect(err).NotTo(HaveOccurred())
		Expect(os.WriteFile(filepath.Join(chartDir, "values.yaml"), valuesData, 0o644)).To(Succeed())

		mappings := []helmchartoci.ImageMapping{
			{Source: "localhost/app:old", Target: "quay.io/org/app:new"},
		}
		Expect(helmchartoci.ApplyImageMappings(chartDir, mappings, []string{"values.yaml"})).To(Succeed())

		updatedValues, err := os.ReadFile(filepath.Join(chartDir, "values.yaml"))
		Expect(err).NotTo(HaveOccurred())
		Expect(string(updatedValues)).To(ContainSubstring("quay.io/org/app:new"))
	})

	It("stores mapped tags as YAML strings when source tag was numeric", func() {
		chartDir := GinkgoT().TempDir()
		valuesYAML := `image:
  repository: registry.example:5000/team/app
  tag: 1.2
`
		Expect(os.WriteFile(filepath.Join(chartDir, "values.yaml"), []byte(valuesYAML), 0o644)).To(Succeed())

		mappings := []helmchartoci.ImageMapping{
			{Source: "registry.example:5000/team/app:1.2", Target: "quay.io/org/app:2.0"},
		}
		Expect(helmchartoci.ApplyImageMappings(chartDir, mappings, []string{"values.yaml"})).To(Succeed())

		updatedValues, err := os.ReadFile(filepath.Join(chartDir, "values.yaml"))
		Expect(err).NotTo(HaveOccurred())

		var root yaml.Node
		Expect(yaml.Unmarshal(updatedValues, &root)).To(Succeed())
		tagNode := findYAMLMappingValue(root.Content[0], "image", "tag")
		Expect(tagNode).NotTo(BeNil())
		Expect(tagNode.Tag).To(Equal("!!str"))
		Expect(tagNode.Value).To(Equal("2.0"))
	})
})

var _ = Describe("App version", func() {
	It("resolves app version from override or commit SHA", func() {
		Expect(helmchartoci.ResolveAppVersion("", "sha")).To(Equal("sha"))
		Expect(helmchartoci.ResolveAppVersion("1.2.3", "sha")).To(Equal("1.2.3"))
	})
})

func findYAMLMappingValue(root *yaml.Node, keys ...string) *yaml.Node {
	if root == nil || len(keys) == 0 {
		return nil
	}
	node := root
	for i, key := range keys {
		if node.Kind != yaml.MappingNode {
			return nil
		}
		var next *yaml.Node
		for j := 0; j+1 < len(node.Content); j += 2 {
			if node.Content[j].Value == key {
				next = node.Content[j+1]
				break
			}
		}
		if next == nil {
			return nil
		}
		if i == len(keys)-1 {
			return next
		}
		node = next
	}
	return nil
}
