package helmchartoci_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/konflux-ci/tools/internal/helmchartoci/push"
	. "github.com/onsi/gomega"
)

type fakeGit struct {
	describe    string
	describeErr error
	shortSHA    string
	count       int
}

func (f *fakeGit) FetchUnshallow(string) error { return nil }
func (f *fakeGit) Describe(string) (string, error) {
	return f.describe, f.describeErr
}
func (f *fakeGit) ShortSHA() (string, error) { return f.shortSHA, nil }
func (f *fakeGit) CommitCount() (int, error) { return f.count, nil }

type fakePusher struct {
	result push.Result
	err    error
	opts   push.Options
}

func (f *fakePusher) PackageAndPush(_ context.Context, opts push.Options) (push.Result, error) {
	f.opts = opts
	return f.result, f.err
}

var errGitDescribe = errSentinel("git describe failed")

type errSentinel string

func (e errSentinel) Error() string { return string(e) }

func writeChartFile(dir, content string) {
	Expect(os.WriteFile(filepath.Join(dir, "Chart.yaml"), []byte(content), 0o644)).To(Succeed())
}

func writeChart(dir, name string) {
	writeChartFile(dir, `apiVersion: v2
name: `+name+`
description: test
version: 0.0.1
`)
}

func initGitRepo(dir string) {
	runGit(dir, "init")
	runGit(dir, "config", "user.email", "test@example.com")
	runGit(dir, "config", "user.name", "test")
	Expect(os.WriteFile(filepath.Join(dir, "file"), []byte("x"), 0o644)).To(Succeed())
	runGit(dir, "add", "file")
	runGit(dir, "commit", "-m", "init")
}

func runGit(dir string, args ...string) {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	Expect(err).NotTo(HaveOccurred(), "git %s failed:\n%s", strings.Join(args, " "), out)
}
