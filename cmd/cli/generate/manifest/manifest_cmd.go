package manifest

import (
	"github.com/spf13/cobra"
)

func NewManifestCmd() *cobra.Command {
	// Flag values are local to each command so several commands can exist (and run) at once.
	var (
		manifestFlagFormat      string
		manifestConfigFile      string
		flagManifestDestination string
	)

	manifestCmd := &cobra.Command{
		Use:   "manifest",
		Short: "📄 (experimental) Generate an OpenFeature flag manifest based on your flags configuration.",
		Long: "📄 (experimental) Generate an OpenFeature flag manifest based on your flags configuration. " +
			"⚠️ note that this is an experimental feature and we may change this command line without warning.",

		RunE: func(cmd *cobra.Command, _ []string) error {
			m, err := NewManifest(manifestConfigFile, manifestFlagFormat, flagManifestDestination)
			if err != nil {
				return err
			}

			output, err := m.Generate()
			if err != nil {
				cmd.SilenceUsage = true

				return err
			}

			output.PrintLines(cmd)

			return nil
		},
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	manifestCmd.Flags().StringVarP(&manifestFlagFormat,
		"format", "f", "yaml", "Format of your input file (YAML, JSON or TOML)")
	manifestCmd.Flags().StringVarP(&manifestConfigFile,
		"config", "c", "", "Location of your Semaphore local configuration file")
	manifestCmd.Flags().StringVar(&flagManifestDestination,
		"flag_manifest_destination", "", "Destination of your flag manifest file. "+
			"If not provided, the manifest will be printed to the console.")
	_ = manifestCmd.MarkFlagRequired("flag_manifest_destination")

	return manifestCmd
}
