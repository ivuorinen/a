// a is a robust CLI wrapper for the age encryption tool using SSH/GitHub keys.
package main

import (
	"fmt"
	"log/slog"
	"os"
	"runtime/debug"

	"github.com/spf13/cobra"

	"github.com/ivuorinen/a/internal/cmd"
)

// version is set at release time via -ldflags "-X main.version=..." (see
// .goreleaser.yml).
//
// It must stay a plain string with no initializer. cmd/link's -X only patches a
// variable that is uninitialized or set to a constant expression, so
// `var version = buildVersion()` made the release stamp a silent no-op: package
// init overwrote whatever the linker wrote, and the value fell back to VCS
// metadata that a tarball or proxy build does not have.
var version string

// buildVersion reports the version to display: the release stamp when the linker
// set one, otherwise the module version Go embeds at build time.
//
// A hardcoded fallback silently rots behind the tags — this returned "v0.3.0"
// while the repository was at v1.0.0 — and `a --version` is what bug reports
// quote, so a stale number sends the maintainer to the wrong tag. go install
// populates Main.Version with the real module version; a plain `go build` in a
// working tree with no VCS metadata has none, and "(devel)" is the honest
// answer there.
func buildVersion() string {
	if version != "" {
		return version
	}
	// The `!= "(devel)"` term cannot change the result today: it filters exactly
	// the value the fallback below returns anyway, so dropping it is a mutation
	// no test can catch. It stays because it states the intent — build info
	// reporting "(devel)" is not a version, it is the absence of one — and that
	// stops mattering only for as long as the fallback happens to be the same
	// string. Change the fallback and the term starts carrying weight.
	if info, ok := readBuildInfo(); ok && info.Main.Version != "" && info.Main.Version != "(devel)" {
		return info.Main.Version
	}
	return "(devel)"
}

// readBuildInfo wraps debug.ReadBuildInfo. It is a package variable because the
// three conditions above cannot all be exercised from a test otherwise: inside
// `go test` the build info always reports Main.Version as "(devel)", so the
// branch that returns a real release version, and the one where the info is
// unavailable at all, are both unreachable. Those are the branches that decide
// what `a --version` prints in bug reports, and the logic has broken once.
var readBuildInfo = debug.ReadBuildInfo

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

// maxLogBytes is the size past which setupLogging rolls the log to <path>.1.
const maxLogBytes = 5 << 20

// rollLog renames the log to <path>.1 once it exceeds maxLogBytes, so the file
// stays bounded.
//
// slog appends forever, and every command writes at least one record naming the
// input path, the output path and the recipients — so this file is a cleartext
// index of everything the user encrypted, and unbounded growth is also
// unbounded retention, in a tool whose whole job is confidentiality. One
// previous generation is kept, so a roll can never destroy the record of the
// run that triggered it.
//
// Every failure is ignored on purpose: logging is not a security control here
// (see setupLogging), and a roll that cannot happen must not stop the command
// that was trying to log.
func rollLog(path string) {
	if path == "" {
		return
	}
	if info, err := os.Stat(path); err == nil && info.Size() > maxLogBytes {
		_ = os.Rename(path, path+".1")
	}
}

// setupLogging configures JSON logging to the configured log file, falling back
// to stderr if the file cannot be opened.
//
// Logging is operational, not a security control, and encrypt/decrypt do not
// depend on it — so a bad log_file_path degrades to stderr rather than bricking
// every command (including `config`, the only way to fix the path).
//
// It returns nothing on purpose. An error return here is a slot a later edit can
// fill with `return err`, reintroducing exactly that bricking; making the absence
// of failure part of the signature forces that question back into review.
func setupLogging(verbose bool) {
	level := slog.LevelInfo
	if verbose {
		level = slog.LevelDebug
	}
	opts := &slog.HandlerOptions{Level: level}

	rollLog(cfg.LogFilePath)

	logFile, err := os.OpenFile(cfg.LogFilePath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		// Mutate the shared logger in place (rather than reassigning the pointer)
		// so the subcommands, which captured the log pointer at construction time,
		// observe the configured handler and level.
		*log = *slog.New(slog.NewJSONHandler(os.Stderr, opts))
		log.Warn("could not open log file; logging to stderr", "path", cfg.LogFilePath, "error", err)
		return
	}
	*log = *slog.New(slog.NewJSONHandler(logFile, opts))
}

// newRootCmd builds the root command with its persistent flag and subcommands.
//
// Split out of main so the wiring, and PersistentPreRunE's two error returns,
// can be driven in-process. Left inside main they were reachable only by
// building the binary and running it, which no coverage or condition analysis
// of this package can see.
func newRootCmd() *cobra.Command {
	var verbose bool

	rootCmd := &cobra.Command{
		Use:     "a",
		Short:   "CLI wrapper for age encryption using SSH/GitHub keys",
		Version: buildVersion(),
		PersistentPreRunE: func(_ *cobra.Command, _ []string) error {
			if err := initConfigPaths(); err != nil {
				return fmt.Errorf("error initializing paths: %w", err)
			}
			if err := loadConfig(); err != nil {
				return fmt.Errorf("error loading config: %w", err)
			}
			setupLogging(verbose)
			return nil
		},
	}

	rootCmd.PersistentFlags().BoolVarP(
		&verbose,
		"verbose",
		"v",
		false,
		"Enable verbose output",
	)

	// Add subcommands from internal/cmd/*
	rootCmd.AddCommand(
		cmd.ConfigCmd(cfg, saveConfig),
		cmd.Encrypt(cfg, log),
		cmd.Decrypt(cfg, log),
		cmd.Completion(rootCmd),
	)

	return rootCmd
}

// run executes the root command and reports the process exit status.
//
// Returning the status rather than calling os.Exit is what keeps both outcomes
// testable: os.Exit terminates the test binary, so a main that called it
// directly could only ever be checked from a subprocess.
func run() int {
	if err := newRootCmd().Execute(); err != nil {
		log.Error("Command execution failed", "error", err)
		return 1
	}
	return 0
}

func main() { os.Exit(run()) }
