package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
)

// Build-time injected via -ldflags "-X .../cmd.Version=..." (etc.) during
// release builds. The defaults below are what an unmoderated `go build`
// produces, which is fine for development.
var (
	Version   = "dev"
	Commit    = "unknown"
	BuildDate = "unknown"
)

var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "Print the gact version, commit, and build date",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		_, err := fmt.Fprintf(
			cmd.OutOrStdout(),
			"gact version %s (commit %s, built %s)\n",
			Version, Commit, BuildDate,
		)
		return err
	},
}

func init() {
	rootCmd.AddCommand(versionCmd)
}
