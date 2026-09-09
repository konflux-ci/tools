package push

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func captureStderr(fn func()) string {
	old := os.Stderr
	r, w, err := os.Pipe()
	Expect(err).NotTo(HaveOccurred())
	os.Stderr = w

	fn()

	Expect(w.Close()).To(Succeed())
	os.Stderr = old

	var buf bytes.Buffer
	_, err = io.Copy(&buf, r)
	Expect(err).NotTo(HaveOccurred())
	Expect(r.Close()).To(Succeed())
	return buf.String()
}

func writeTestChart() string {
	chartDir := GinkgoT().TempDir()
	chartYAML := `apiVersion: v2
name: test-chart
description: test
version: 0.0.1
`
	Expect(os.WriteFile(filepath.Join(chartDir, "Chart.yaml"), []byte(chartYAML), 0o644)).To(Succeed())
	templatesDir := filepath.Join(chartDir, "templates")
	Expect(os.MkdirAll(templatesDir, 0o755)).To(Succeed())
	template := `apiVersion: v1
kind: ConfigMap
metadata:
  name: test
`
	Expect(os.WriteFile(filepath.Join(templatesDir, "configmap.yaml"), []byte(template), 0o644)).To(Succeed())
	return chartDir
}

func writeSubchart(chartDir, name, version string) {
	chartYAML := fmt.Sprintf(`apiVersion: v2
name: %s
version: %s
`, name, version)
	Expect(os.WriteFile(filepath.Join(chartDir, "Chart.yaml"), []byte(chartYAML), 0o644)).To(Succeed())
}

func writeParentChart(chartDir, depName, depVersion, repository string) {
	chartYAML := fmt.Sprintf(`apiVersion: v2
name: parent
version: 0.1.0
dependencies:
  - name: %s
    version: %s
    repository: %s
`, depName, depVersion, repository)
	Expect(os.WriteFile(filepath.Join(chartDir, "Chart.yaml"), []byte(chartYAML), 0o644)).To(Succeed())
}

func writeParentWithFileDependency(chartDir string) {
	depDir := filepath.Join(chartDir, "deps", "subchart")
	Expect(os.MkdirAll(depDir, 0o755)).To(Succeed())
	writeSubchart(depDir, "subchart", "1.0.0")
	writeParentChart(chartDir, "subchart", "1.0.0", "file://./deps/subchart")
}
