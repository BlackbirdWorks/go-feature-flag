package generate

import (
	"errors"

	"github.com/spf13/cobra"

	"github.com/blackbirdworks/semaphore/cmd/cli/generate/manifest"
	"github.com/blackbirdworks/semaphore/cmd/cli/helper"
)

func NewGenerateCmd() *cobra.Command {
	g := &cobra.Command{
		Use:   "generate",
		Short: "🏗️ Generate Semaphore related files",
		Long:  `🏗️ Generate Semaphore relates files (examples: flag manifest, ...)`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			output := helper.Output{}
			output.Add("you must specify a subcommand (e.g., manifest)", helper.ErrorLevel)
			output.PrintLines(cmd)

			return errors.New("no subcommand provided")
		},
		SilenceErrors: true,
		SilenceUsage:  true,
	}

	g.AddCommand(manifest.NewManifestCmd())

	return g
}
