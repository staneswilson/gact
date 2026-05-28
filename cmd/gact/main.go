// Command gact is the CLI entry point for gact, a tool that runs GitHub
// Actions workflows locally without Docker. This file is the thin shell that
// hands off to the cobra-based command tree in the sibling cmd package.
package main

import (
	"os"

	"github.com/staneswilson/gact/cmd/gact/cmd"
)

func main() { os.Exit(cmd.Execute()) }
