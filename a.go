// a is a robust CLI wrapper for the age encryption tool using SSH/GitHub keys.
package main

import (
	"fmt"
	"log/slog"
	"os"

	"github.com/spf13/cobra"

	"github.com/ivuorinen/a/cmd"
)

// version is overridden at release time via -ldflags "-X main.version=...".
var version = "v0.3.0"

var (
	log      = slog.New(slog.NewJSONHandler(os.Stderr, nil))
	cfg      = &cmd.Config{}
	cfgFile  string
	cacheDir string
)

// initConfigPaths initializes configuration and cache directories.
func initConfigPaths() error {
	paths, err := cmd.InitConfigPaths()
	if err != nil {
		return err
	}
	cfgFile = paths.ConfigFile
	cacheDir = paths.CacheDir
	return nil
}

// loadConfig loads configuration from the YAML file into the shared cfg value.
//
// It mutates cfg in place (rather than reassigning the pointer) so that the
// subcommands, which captured the cfg pointer at construction time, observe the
// loaded values.
func loadConfig() error {
	loaded, err := cmd.LoadConfig(cfgFile)
	if err != nil {
		return err
	}
	*cfg = *loaded
	cfg.CacheDir = cacheDir
	return nil
}

// saveConfig saves configuration to the YAML file.
func saveConfig(cfg *cmd.Config) error {
	return cmd.SaveConfig(cfgFile, cfg)
}

// setupLogging configures JSON logging to the configured log file, falling back
// to stderr if the file cannot be opened.
//
// Logging is operational, not a security control, and encrypt/decrypt do not
// depend on it — so a bad log_file_path degrades to stderr rather than bricking
// every command (including `config`, the only way to fix the path). It never
// returns an error.
func setupLogging(verbose bool) error {
	level := slog.LevelInfo
	if verbose {
		level = slog.LevelDebug
	}
	opts := &slog.HandlerOptions{Level: level}

	logFile, err := os.OpenFile(cfg.LogFilePath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		log = slog.New(slog.NewJSONHandler(os.Stderr, opts))
		log.Warn("could not open log file; logging to stderr", "path", cfg.LogFilePath, "error", err)
		return nil
	}
	log = slog.New(slog.NewJSONHandler(logFile, opts))
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
			if err := loadConfig(); err != nil {
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
		cmd.ConfigCmd(cfg, saveConfig),
		cmd.Encrypt(cfg, log),
		cmd.Decrypt(cfg, log),
		cmd.Completion(rootCmd),
	)

	// Execute the root command
	if err := rootCmd.Execute(); err != nil {
		log.Error("Command execution failed", "error", err)
		os.Exit(1)
	}
}
