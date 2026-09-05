package helmchartoci_test

import (
	"github.com/konflux-ci/tools/internal/helmchartoci"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("ChartNameFromImage", func() {
	DescribeTable("parses chart name",
		func(image, want string) {
			got, err := helmchartoci.ChartNameFromImage(image)
			Expect(err).NotTo(HaveOccurred())
			Expect(got).To(Equal(want))
		},
		Entry("quay tagged image", "quay.io/org/my-chart:1.0.0", "my-chart"),
		Entry("localhost with port", "localhost:5000/my-chart", "my-chart"),
		Entry("nested repository path", "registry.example.com/tenant/app/on-pr-abc123", "on-pr-abc123"),
	)

	It("returns error for empty image", func() {
		_, err := helmchartoci.ChartNameFromImage("")
		Expect(err).To(MatchError(ContainSubstring(`image ""`)))
		Expect(err).To(MatchError(ContainSubstring("empty")))
	})
})
