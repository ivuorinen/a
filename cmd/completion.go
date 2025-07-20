// Package cmd provides CLI command constructors for the age wrapper.
package cmd

import (
	"os"

	"github.com/spf13/cobra"
)

// Completion returns a cobra.Command that generates shell completions.
func Completion(rootCmd *cobra.Command) *cobra.Command {
	return &cobra.Command{
		Use:   "completion [bash|zsh|fish]",
		Short: "Generate shell completion scripts",
		Args:  cobra.ExactArgs(1),
		Run: func(_ *cobra.Command, args []string) {
			switch args[0] {
			case "bash":
				_ = rootCmd.GenBashCompletion(os.Stdout)
			case "zsh":
				_ = rootCmd.GenZshCompletion(os.Stdout)
			case "fish":
				_ = rootCmd.GenFishCompletion(os.Stdout, true)
			}
		},
	}
}
