// Package cmd provides CLI command constructors for the age wrapper.
package cmd

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

// Completion returns a cobra.Command that generates shell completions.
func Completion(rootCmd *cobra.Command) *cobra.Command {
	return &cobra.Command{
		Use:       "completion [bash|zsh|fish]",
		Short:     "Generate shell completion scripts",
		Args:      cobra.ExactArgs(1),
		ValidArgs: []string{"bash", "zsh", "fish"},
		RunE: func(_ *cobra.Command, args []string) error {
			switch args[0] {
			case "bash":
				return rootCmd.GenBashCompletion(os.Stdout)
			case "zsh":
				return rootCmd.GenZshCompletion(os.Stdout)
			case "fish":
				return rootCmd.GenFishCompletion(os.Stdout, true)
			default:
				return fmt.Errorf("unsupported shell %q: want bash, zsh, or fish", args[0])
			}
		},
	}
}
