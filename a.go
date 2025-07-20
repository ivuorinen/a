// a is a robust CLI wrapper for the age encryption tool using SSH/GitHub keys.
package main

import (
	"fmt"
	"os"

	"github.com/sirupsen/logrus"
	"github.com/spf13/cobra"

	"github.com/ivuorinen/a/cmd"
)

const version = "v0.3.0"

var (
	log     = logrus.New()
	cfg     *cmd.Config
	cfgFile string
)

// initConfigPaths initializes configuration and cache directories.
func initConfigPaths() error {
	paths, err := cmd.InitConfigPaths()
	if err != nil {
		return err
	}
	cfgFile = paths.ConfigFile
	return nil
}

// loadConfig loads configuration from the YAML file.
func loadConfig() (*cmd.Config, error) {
	return cmd.LoadConfig(cfgFile)
}

// saveConfig saves configuration to the YAML file.
func saveConfig(cfg *cmd.Config) error {
	return cmd.SaveConfig(cfgFile, cfg)
}

// setupLogging configures JSON logging to file and stdout.
func setupLogging(verbose bool) error {
	log.SetFormatter(&logrus.JSONFormatter{})
	logFile, err := os.OpenFile(cfg.LogFilePath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return fmt.Errorf("could not open log file: %w", err)
	}
	log.SetOutput(logFile)
	if verbose {
		log.SetLevel(logrus.DebugLevel)
	} else {
		log.SetLevel(logrus.InfoLevel)
	}
	return nil
}

func main() {
	var verbose bool

	rootCmd := &cobra.Command{
		Use:     "a",
		Short:   "CLI wrapper for age encryption using SSH/GitHub keys",
		Version: version,
		PersistentPreRunE: func(_ *cobra.Command, _ []string) error {
			if err := initConfigPaths(); err != nil {
				return fmt.Errorf("error initializing paths: %w", err)
			}
			var err error
			cfg, err = loadConfig()
			if err != nil {
				return fmt.Errorf("error loading config: %w", err)
			}
			return setupLogging(verbose)
		},
	}

	rootCmd.PersistentFlags().BoolVarP(
		&verbose,
		"verbose",
		"v",
		false,
		"Enable verbose output",
	)

	// Add subcommands from cmd/*
	rootCmd.AddCommand(
		cmd.ConfigCmd(cfg, func(c any) error {
			return saveConfig(c.(*cmd.Config))
		}),
		cmd.Encrypt(cfg, log),
		cmd.Decrypt(cfg, log),
		cmd.Completion(rootCmd),
	)

	// Execute the root command
	if err := rootCmd.Execute(); err != nil {
		log.WithError(err).Fatal("Command execution failed")
	}
}
