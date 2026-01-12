// pkg/cmd/root.go
package cmd

import (
	"github.com/spf13/cobra"
)

// NewRootCommand creates the root command for xDSnap
func NewRootCommand(streams IOStreams) *cobra.Command {
	rootCmd := &cobra.Command{
		Use:   "xdsnap",
		Short: "XDSnap captures Envoy state snapshots from Consul Connect sidecars on Nomad.",
		Long: `XDSnap is a tool for capturing and archiving Envoy proxy configuration
snapshots from Consul Connect service mesh workloads running on Nomad.

It helps operators debug service mesh connectivity issues by collecting:
- Envoy configuration dumps
- Stats, listeners, clusters, and certificates
- Task logs (application and sidecar)
- Optional network traffic captures`,
	}

	// Add the capture subcommand
	rootCmd.AddCommand(NewCaptureCommand(streams))
	// Add the analyze subcommand (disabled)
	// rootCmd.AddCommand(NewAnalyzeCommand(streams))

	return rootCmd
}
