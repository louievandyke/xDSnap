// cmd/root.go
package main

import (
	"os"

	"github.com/markcampv/xDSnap/pkg/cmd"
	"github.com/spf13/pflag"
)

func main() {
	flags := pflag.NewFlagSet("xdsnap", pflag.ExitOnError)
	pflag.CommandLine = flags

	root := cmd.NewRootCommand(cmd.NewIOStreams())

	if err := root.Execute(); err != nil {
		os.Exit(1)
	}
}
