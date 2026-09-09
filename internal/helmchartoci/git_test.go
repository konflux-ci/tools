package helmchartoci_test

import (
	"github.com/konflux-ci/tools/internal/helmchartoci"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Git", func() {
	It("returns explicit chart version without git", func() {
		got, err := helmchartoci.ResolveChartVersion("9.9.9", "", "", "sha", nil)
		Expect(err).NotTo(HaveOccurred())
		Expect(got).To(Equal("9.9.9"))
	})

	It("derives chart version from git describe", func() {
		git := &fakeGit{
			describe: "helm-1.2",
			shortSHA: "abc1234",
		}
		got, err := helmchartoci.ResolveChartVersion("", "helm-", "-suffix", "sha", git)
		Expect(err).NotTo(HaveOccurred())
		Expect(got).To(Equal("1.2.0+abc1234-suffix"))
	})

	It("falls back when git describe fails", func() {
		git := &fakeGit{
			describeErr: errGitDescribe,
			shortSHA:    "abc1234",
			count:       7,
		}
		got, err := helmchartoci.ResolveChartVersion("", "helm-", "", "sha", git)
		Expect(err).NotTo(HaveOccurred())
		Expect(got).To(Equal("0.1.7+abc1234"))
	})

	It("requires git when chart version is unset", func() {
		_, err := helmchartoci.ResolveChartVersion("", "helm-", "", "abc123", nil)
		Expect(err).To(HaveOccurred())
	})

	Describe("ExecGit", func() {
		It("validates commit SHA before fetch", func() {
			git := &helmchartoci.ExecGit{Dir: GinkgoT().TempDir()}
			Expect(git.FetchUnshallow("--tags")).To(MatchError(ContainSubstring("invalid commit sha")))
			Expect(git.FetchUnshallow("")).To(MatchError(ContainSubstring("commit sha is required")))
		})

		It("uses injected runner for git commands", func() {
			calls := 0
			git := &helmchartoci.ExecGit{
				Runner: func(args ...string) (string, error) {
					calls++
					switch args[0] {
					case "rev-parse":
						if args[1] == "--is-shallow-repository" {
							return "false", nil
						}
						if args[1] == "--short" {
							return "abc1234", nil
						}
					case "fetch":
						return "", nil
					case "describe":
						return "helm-1.2", nil
					case "rev-list":
						return "9", nil
					}
					return "", nil
				},
			}

			Expect(git.FetchUnshallow("abc123")).To(Succeed())
			describe, err := git.Describe("helm-")
			Expect(err).NotTo(HaveOccurred())
			Expect(describe).To(Equal("helm-1.2"))
			short, err := git.ShortSHA()
			Expect(err).NotTo(HaveOccurred())
			Expect(short).To(Equal("abc1234"))
			count, err := git.CommitCount()
			Expect(err).NotTo(HaveOccurred())
			Expect(count).To(Equal(9))
			Expect(calls).NotTo(BeZero())
		})

		It("uses --unshallow for shallow repositories", func() {
			var fetchArgs []string
			git := &helmchartoci.ExecGit{
				Runner: func(args ...string) (string, error) {
					if args[0] == "rev-parse" {
						return "true", nil
					}
					if args[0] == "fetch" {
						fetchArgs = append([]string{}, args...)
					}
					return "", nil
				},
			}
			Expect(git.FetchUnshallow("abc123")).To(Succeed())
			Expect(fetchArgs).NotTo(BeEmpty())
			Expect(fetchArgs[1]).To(Equal("--unshallow"))
		})

		It("propagates runner errors", func() {
			sentinel := errSentinel("git failed")
			git := &helmchartoci.ExecGit{
				Runner: func(args ...string) (string, error) {
					return "", sentinel
				},
			}

			_, err := git.ShortSHA()
			Expect(err).To(MatchError(sentinel))
			_, err = git.CommitCount()
			Expect(err).To(MatchError(sentinel))
		})

		It("returns error for invalid commit count", func() {
			git := &helmchartoci.ExecGit{
				Runner: func(args ...string) (string, error) {
					if args[0] == "rev-list" {
						return "not-a-number", nil
					}
					return "", nil
				},
			}
			_, err := git.CommitCount()
			Expect(err).To(HaveOccurred())
		})

		It("works against a real git repository", func() {
			dir := GinkgoT().TempDir()
			initGitRepo(dir)

			git := &helmchartoci.ExecGit{Dir: dir}
			_ = git.FetchUnshallow("abc123def456")
			short, err := git.ShortSHA()
			Expect(err).NotTo(HaveOccurred())
			Expect(short).NotTo(BeEmpty())
			count, err := git.CommitCount()
			Expect(err).NotTo(HaveOccurred())
			Expect(count).To(BeNumerically(">=", 1))
		})
	})
})
