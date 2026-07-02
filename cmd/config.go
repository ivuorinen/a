package cmd

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

// configKeys lists the settable configuration keys (their YAML names), used in
// help text and error messages.
var configKeys = []string{
	"ssh_key_path",
	"github_user",
	"default_recipients",
	"cache_ttl_minutes",
	"log_file_path",
}

// ConfigCmd returns the `config` command (alias `c`) for viewing and changing
// configuration. With no subcommand it prints the available subcommands and the
// current configuration. The saveConfig callback persists changes.
func ConfigCmd(cfg *Config, saveConfig func(*Config) error) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "config",
		Aliases: []string{"c"},
		Short:   "View or change configuration (set|rem|show)",
		RunE: func(cmd *cobra.Command, _ []string) error {
			_, err := fmt.Fprintf(cmd.OutOrStdout(), "Usage:\n"+
				"  a config show             Show current configuration\n"+
				"  a config set <key> <val>  Set a configuration value\n"+
				"  a config rem <key>        Reset a configuration value to its default\n\n"+
				"Keys: %s\n\n"+
				"Current configuration:\n%s",
				strings.Join(configKeys, ", "), formatConfig(cfg))
			return err
		},
	}

	save := func(cmd *cobra.Command) error {
		if err := saveConfig(cfg); err != nil {
			return err
		}
		_, err := fmt.Fprint(cmd.OutOrStdout(), formatConfig(cfg))
		return err
	}

	cmd.AddCommand(
		&cobra.Command{
			Use:   "show",
			Short: "Show current configuration",
			Args:  cobra.NoArgs,
			RunE: func(cmd *cobra.Command, _ []string) error {
				_, err := fmt.Fprint(cmd.OutOrStdout(), formatConfig(cfg))
				return err
			},
		},
		&cobra.Command{
			Use:   "set <key> <value>",
			Short: "Set a configuration value",
			Args:  cobra.MinimumNArgs(2),
			RunE: func(cmd *cobra.Command, args []string) error {
				if err := setConfigKey(cfg, args[0], strings.Join(args[1:], " ")); err != nil {
					return err
				}
				return save(cmd)
			},
		},
		&cobra.Command{
			Use:   "rem <key>",
			Short: "Reset a configuration value to its default",
			Args:  cobra.ExactArgs(1),
			RunE: func(cmd *cobra.Command, args []string) error {
				if err := setConfigKey(cfg, args[0], ""); err != nil {
					return err
				}
				return save(cmd)
			},
		},
	)

	return cmd
}

// setConfigKey sets one configuration field by its YAML key name. An empty value
// resets the field to its zero value (used by `rem`).
func setConfigKey(cfg *Config, key, value string) error {
	switch key {
	case "ssh_key_path":
		cfg.SSHKeyPath = value
	case "github_user":
		cfg.GitHubUser = value
	case "log_file_path":
		cfg.LogFilePath = value
	case "default_recipients":
		if value == "" {
			cfg.DefaultRecipients = nil
			break
		}
		parts := strings.Split(value, ",")
		for i := range parts {
			parts[i] = strings.TrimSpace(parts[i])
		}
		cfg.DefaultRecipients = parts
	case "cache_ttl_minutes":
		if value == "" {
			cfg.CacheTTLMinutes = 0
			break
		}
		n, err := strconv.Atoi(value)
		if err != nil {
			return fmt.Errorf("cache_ttl_minutes must be an integer: %w", err)
		}
		cfg.CacheTTLMinutes = n
	default:
		return fmt.Errorf("unknown config key %q: valid keys are %s", key, strings.Join(configKeys, ", "))
	}
	return nil
}

// formatConfig renders the config as YAML for display.
func formatConfig(cfg *Config) string {
	data, err := yaml.Marshal(cfg)
	if err != nil {
		return fmt.Sprintf("error rendering config: %v\n", err)
	}
	return string(data)
}
