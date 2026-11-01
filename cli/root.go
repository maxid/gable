package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/maxid/gable"
)

// NewRootCommand returns the gable CLI.
func NewRootCommand() *cobra.Command {
	root := &cobra.Command{
		Use:           "gable",
		Short:         "gable application framework",
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	root.AddCommand(newNewCommand(), newDevCommand(), newDSLCommand(), newMigrateCommand(), newVersionCommand())
	return root
}

func newVersionCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "print the gable version",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			_, err := fmt.Fprintln(cmd.OutOrStdout(), "gable "+gable.Version)
			return err
		},
	}
}
