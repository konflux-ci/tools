// Command helm-chart-oci packages and pushes a Helm chart to an OCI registry.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"

	"github.com/konflux-ci/tools/internal/helmchartoci"
)

func main() {
	os.Exit(runMain(os.Args[1:], os.Getenv, helmchartoci.Run))
}

func runMain(args []string, getenv func(string) string, runFn func(context.Context, helmchartoci.RunOptions) error) int {
	cfg, err := parseCLI(getenv, args)
	if err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		fmt.Fprintf(os.Stderr, "helm-chart-oci: %v\n", err)
		return 1
	}
	if err := execute(context.Background(), cfg, runFn); err != nil {
		fmt.Fprintf(os.Stderr, "helm-chart-oci: %v\n", err)
		return 1
	}
	return 0
}
