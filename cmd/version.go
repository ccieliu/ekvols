package cmd

import (
	"github.com/spf13/cobra"
)

var versionCmd = &cobra.Command{
	Use:     "version",
	Short:   "Display the version of the application",
	Long:    `This command displays the current version of the application.`,
	Args:    cobra.NoArgs,
	Run:     versionRun,
	Example: "version",
}

func versionRun(cmd *cobra.Command, args []string) {

	cmd.Printf("Version: %s\n", rootCmd.Version)
}

func init() {
	// Here you will define your flags and configuration settings.
	// Cobra supports persistent flags, which, if defined here,
	// will be global for your application.
	rootCmd.AddCommand(versionCmd)
}
