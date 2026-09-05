package helmchartoci_test

import (
	"github.com/konflux-ci/tools/internal/helmchartoci"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Version", func() {
	DescribeTable("ChartVersionFromDescribe",
		func(tagPrefix, describe, shortSHA, want string) {
			got := helmchartoci.ChartVersionFromDescribe(tagPrefix, describe, shortSHA)
			Expect(got).To(Equal(want))
		},
		Entry("tag not on commit", "helm-", "helm-1.2-3-gabc1234", "abc1234", "1.2.3+gabc1234"),
		Entry("tag on commit", "helm-", "helm-1.2", "abc1234", "1.2.0+abc1234"),
	)

	It("builds fallback chart version", func() {
		Expect(helmchartoci.FallbackChartVersion(42, "deadbeef")).To(Equal("0.1.42+deadbeef"))
	})
})
