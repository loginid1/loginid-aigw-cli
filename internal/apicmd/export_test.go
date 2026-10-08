package apicmd

import "github.com/spf13/cobra"

// AddAllWithBase lets tests point the commands at a fake server. The real
// binary has no way to change the base except the temporary env var; see
// ResolveTarget.
func AddAllWithBase(root *cobra.Command, base string) {
	addAll(root, func() (Target, error) { return Target{BaseURL: base}, nil })
}
