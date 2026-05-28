// Package cmd hosts the cobra command tree for the gact CLI. It deliberately
// stays free of domain dependencies — drivers wire to application services
// elsewhere; this package is just the shell that parses argv and dispatches.
package cmd

import (
	"fmt"
	"io"
	"os"

	"github.com/spf13/cobra"
)

// rootCmd is the top-level `gact` command. Subcommands attach to this via
// init() in their own files (e.g. version.go).
var rootCmd = &cobra.Command{
	Use:   "gact",
	Short: "Run GitHub Actions workflows locally, without Docker",
	Long: "gact runs GitHub Actions workflows locally without requiring Docker " +
		"for the common case, surfacing workflow-authoring mistakes in seconds " +
		"instead of minutes round-tripping through GitHub.",
	// We print usage/errors ourselves in Execute so behaviour is uniform across
	// all subcommands and so tests can assert on streams cleanly.
	SilenceUsage:  true,
	SilenceErrors: true,
}

// Execute runs the root command against os.Args[1:] using os.Stdout/os.Stderr
// and returns the process exit code. The binary's main() should call
// os.Exit(Execute()).
func Execute() int {
	return ExecuteWith(os.Args[1:], os.Stdout, os.Stderr)
}

// ExecuteWith is the test-friendly seam under Execute. It runs the root
// command with the supplied args and writers and returns the exit code.
// Tests drive this directly; production code goes through Execute().
func ExecuteWith(args []string, stdout, stderr io.Writer) int {
	// Fresh per-call wiring so streams don't leak across invocations
	// (especially important in tests that run in parallel later).
	rootCmd.SetOut(stdout)
	rootCmd.SetErr(stderr)
	rootCmd.SetArgs(args)

	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(stderr, "Error:", err)
		return 1
	}
	return 0
}
