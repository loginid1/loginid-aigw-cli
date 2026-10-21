package apicmd

import "github.com/spf13/cobra"

// AddAllWithBase lets tests point the commands at a fake server. The real
// binary changes the base only at build time; see APIBase.
func AddAllWithBase(root *cobra.Command, base string) {
	addAll(root, func() string { return base })
}

// OperationByID returns the generated operation with id, for tests that call
// its helpers directly.
func OperationByID(id string) *Operation {
	for i := range Operations {
		if Operations[i].ID == id {
			return &Operations[i]
		}
	}

	return nil
}
