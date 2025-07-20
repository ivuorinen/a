package cmd

import (
	"github.com/spf13/cobra"
)

// ConfigCmd returns a cobra.Command for configuring SSH keys, GitHub settings, and logging.
//
// The saveConfig callback is called with the updated config.
func ConfigCmd(cfg any, saveConfig func(cfg any) error) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "config",
		Short: "Configure SSH keys, GitHub settings, and logging",
		RunE: func(cmd *cobra.Command, _ []string) error {
			// Type assertion for expected config struct
			config, ok := cfg.(*Config)
			if !ok {
				return nil
			}
			sshKey, _ := cmd.Flags().GetString("ssh-key")
			ghUser, _ := cmd.Flags().GetString("github-user")
			logPath, _ := cmd.Flags().GetString("log-file-path")
			recipients, _ := cmd.Flags().GetStringSlice("default-recipients")
			ttl, _ := cmd.Flags().GetInt("cache-ttl")
			config.SSHKeyPath = sshKey
			config.GitHubUser = ghUser
			config.DefaultRecipients = recipients
			config.CacheTTLMinutes = ttl
			config.LogFilePath = logPath
			return saveConfig(config)
		},
	}

	// These flag defaults assume cfg is already loaded
	if config, ok := cfg.(*Config); ok {
		cmd.Flags().String("ssh-key", "", "Path to private SSH key")
		cmd.Flags().String("github-user", "", "GitHub username for public keys")
		cmd.Flags().String("log-file-path", config.LogFilePath, "Path for the log file")
		cmd.Flags().StringSlice("default-recipients", []string{}, "Public key file paths")
		cmd.Flags().Int("cache-ttl", 120, "Cache TTL in minutes")
	} else {
		cmd.Flags().String("ssh-key", "", "Path to private SSH key")
		cmd.Flags().String("github-user", "", "GitHub username for public keys")
		cmd.Flags().String("log-file-path", "", "Path for the log file")
		cmd.Flags().StringSlice("default-recipients", []string{}, "Public key file paths")
		cmd.Flags().Int("cache-ttl", 120, "Cache TTL in minutes")
	}

	return cmd
}
