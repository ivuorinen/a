package cmd

import (
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// defaultCacheTTLMinutes is the GitHub-key cache lifetime written to a freshly
// bootstrapped config, and the value `a config rem cache_ttl_minutes` restores.
// There is no flag for it: the TTL is config-file only, so this constant is its
// single declaration.
const defaultCacheTTLMinutes = 120

// maxCacheTTLMinutes is the largest cache_ttl_minutes that survives conversion
// to a time.Duration, which counts int64 nanoseconds.
//
// Past this the multiplication in readKeyCache wraps, and not gracefully: the
// result is non-monotonic, so 200000000 minutes yields a negative duration that
// disables the cache while the larger 999999999 yields a positive ~147 years.
// An operator raising the TTL therefore gets the opposite of what they asked
// for, silently. Derived rather than written out so it cannot drift.
const maxCacheTTLMinutes = int(math.MaxInt64 / int64(time.Minute))

// yamlMarshal wraps yaml.Marshal so its failure is reachable from a test.
//
// yaml.Marshal cannot actually fail for Config: every field is a string,
// []string or int, and none of those can error. The error branches in SaveConfig
// and formatConfig are therefore dead as written — they exist because ignoring
// the return of a function that returns an error is worse, not because the
// failure can happen. This seam is what lets those branches be exercised instead
// of merely asserted to be unreachable.
var yamlMarshal = yaml.Marshal

// tempFile is the subset of *os.File that the atomic-write helpers use.
//
// SaveConfig, encryptFile and tryDecrypt all write to a temp file and rename it
// over the target, and each guards the Write and Close along the way. Those
// guards exist for a full disk or an I/O error mid-write, neither of which can
// be provoked against a real file in a temp directory — so without this
// interface those branches cannot be tested at all.
type tempFile interface {
	io.Writer
	Close() error
	Name() string
}

// createTemp wraps os.CreateTemp, which creates the file with 0600. It is a
// package variable so tests can substitute a file whose Write or Close fails;
// see tempFile.
//
// The explicit nil on the error path matters: returning os.CreateTemp's results
// directly would wrap a nil *os.File in a non-nil tempFile, and every `tmp ==
// nil` check downstream would silently stop working.
var createTemp = func(dir, pattern string) (tempFile, error) {
	f, err := os.CreateTemp(dir, pattern)
	if err != nil {
		return nil, err
	}
	return f, nil
}

// userConfigDir resolves the base configuration directory for goos.
//
// Parameterized on goos rather than reading runtime.GOOS directly so the darwin
// branch is reachable from a test on any platform; the sole caller passes
// runtime.GOOS.
func userConfigDir(goos string) (string, error) {
	// Personal preference, I don't like the "$HOME/Library/Application Support/" path
	if goos == "darwin" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("resolving home directory for the config path: %w", err)
		}
		return filepath.Join(home, ".config"), nil
	}
	return os.UserConfigDir()
}

// Config represents the application's YAML configuration.
type Config struct {
	SSHKeyPath        string   `yaml:"ssh_key_path"`
	GitHubUser        string   `yaml:"github_user"`
	DefaultRecipients []string `yaml:"default_recipients"`
	CacheTTLMinutes   int      `yaml:"cache_ttl_minutes"`
	LogFilePath       string   `yaml:"log_file_path"`

	// CacheDir is the runtime cache directory (from InitConfigPaths). It is not
	// persisted to the YAML file; it is populated after loading.
	CacheDir string `yaml:"-"`
}

// ConfigPaths holds config and cache file paths.
type ConfigPaths struct {
	ConfigFile string
	CacheDir   string
}

// InitConfigPaths initializes configuration and cache directories and returns their paths.
func InitConfigPaths() (ConfigPaths, error) {
	configDir, err := userConfigDir(runtime.GOOS)
	if err != nil {
		return ConfigPaths{}, err
	}

	cfgDir := filepath.Join(configDir, "a")
	cfgFile := filepath.Join(cfgDir, "config.yaml")
	if err := os.MkdirAll(cfgDir, 0o700); err != nil {
		return ConfigPaths{}, err
	}

	// Materialize a default config on first run so the `config` command (and any
	// other command whose PreRun loads config) can bootstrap without a manual step.
	if _, err := os.Stat(cfgFile); errors.Is(err, os.ErrNotExist) {
		if err := SaveConfig(cfgFile, &Config{CacheTTLMinutes: defaultCacheTTLMinutes}); err != nil {
			return ConfigPaths{}, err
		}
	}

	cacheBase, err := os.UserCacheDir()
	if err != nil {
		return ConfigPaths{}, err
	}
	cacheDir := filepath.Join(cacheBase, "a")
	if err := os.MkdirAll(cacheDir, 0o700); err != nil {
		return ConfigPaths{}, err
	}

	return ConfigPaths{
		ConfigFile: cfgFile,
		CacheDir:   cacheDir,
	}, nil
}

// LoadConfig loads configuration from the YAML file.
//
// cfgFile is supplied by InitConfigPaths (derived from os.UserConfigDir), not from
// user input, so it is trusted. A missing file yields a default config so callers
// can bootstrap one.
func LoadConfig(cfgFile string) (*Config, error) {
	info, err := os.Stat(cfgFile)
	if errors.Is(err, os.ErrNotExist) {
		cfg := &Config{}
		if err := applyConfigDefaults(cfg); err != nil {
			return nil, err
		}
		return cfg, nil
	}
	if err != nil {
		return nil, fmt.Errorf("could not stat config file: %w", err)
	}
	// Reject only group/other access; stricter modes such as 0400 are fine. The
	// remedy belongs in the message: this check runs in PersistentPreRunE, so it
	// fails every command including `config set` — the documented way to change
	// settings — and a rule with no stated fix leaves the user guessing.
	if perm := info.Mode().Perm(); perm&0o077 != 0 {
		return nil, fmt.Errorf(
			"config file %s is group/other accessible (perms %#o); fix it with: chmod 600 %s",
			cfgFile, perm, cfgFile)
	}
	// #nosec G304 -- cfgFile is supplied by InitConfigPaths (os.UserConfigDir-derived), not user input
	data, err := os.ReadFile(cfgFile)
	if err != nil {
		return nil, err
	}
	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, err
	}
	if err := applyConfigDefaults(&cfg); err != nil {
		return nil, err
	}
	return &cfg, nil
}

// applyConfigDefaults fills in derived defaults for any unset fields.
//
// The home directory comes from os.UserHomeDir, which errors when it cannot be
// resolved. os.Getenv("HOME") does not: with HOME unset it yields "", and
// filepath.Join turns that into the relative path ".local/state/a" — so the log
// landed in whatever directory the command happened to run from, and a read-only
// working directory made the MkdirAll fail and bricked every command.
//
// The state directory follows $XDG_STATE_HOME (default ~/.local/state) rather
// than a hardcoded ~/.state, which is not a location any specification defines.
// The config and cache paths already honor their XDG variables via
// os.UserConfigDir/os.UserCacheDir; the log opting out stranded state in
// ~/.state whenever a user relocated the other two. The standard library has no
// os.UserStateDir, so this is resolved by hand.
func applyConfigDefaults(cfg *Config) error {
	if cfg.LogFilePath == "" {
		stateBase := os.Getenv("XDG_STATE_HOME")
		if stateBase == "" {
			home, err := os.UserHomeDir()
			if err != nil {
				return fmt.Errorf("resolving home directory for the default log path: %w", err)
			}
			stateBase = filepath.Join(home, ".local", "state")
		}
		stateDir := filepath.Join(stateBase, "a")
		// #nosec G703 -- stateDir is derived from XDG_STATE_HOME or os.UserHomeDir plus constants
		if err := os.MkdirAll(stateDir, 0o700); err != nil {
			return err
		}
		cfg.LogFilePath = filepath.Join(stateDir, "cli.log")
	}
	return nil
}

// SaveConfig saves configuration to the YAML file.
//
// It writes to a temp file (created 0600) in the config directory and renames it
// over cfgFile, so a failed or interrupted write cannot truncate the existing
// config, and the result is always 0600 (which LoadConfig requires).
//
// The rename is not fsynced, so the guarantee stops at process death: a host
// crash inside the writeback window can still leave a zero-length config, which
// LoadConfig reads as an empty one rather than an error. Add tmp.Sync() before
// the Close below if that ever needs to hold.
func SaveConfig(cfgFile string, cfg *Config) (err error) {
	data, err := yamlMarshal(cfg)
	if err != nil {
		return err
	}
	tmp, err := createTemp(filepath.Dir(cfgFile), ".config-*.yaml")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer func() {
		if err != nil {
			_ = os.Remove(tmpName)
		}
	}()
	if _, err = tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err = tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, cfgFile)
}

// ScanSSHPrivateKeys scans ~/.ssh for private keys matching id_* (excluding .pub).
//
// os.UserHomeDir rather than os.Getenv("HOME"): an unset HOME made the empty
// string join to the relative ".ssh", so the scan read private keys out of the
// current working directory — meaning a checkout could plant ./.ssh/id_* and have
// them parsed.
func ScanSSHPrivateKeys() ([]string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("resolving home directory to scan for SSH keys: %w", err)
	}
	sshDir := filepath.Join(home, ".ssh")
	files, err := os.ReadDir(sshDir)
	if err != nil {
		return nil, err
	}
	var keys []string
	for _, f := range files {
		if f.IsDir() {
			continue
		}
		name := f.Name()
		if strings.HasPrefix(name, "id_") && !strings.HasSuffix(name, ".pub") {
			keys = append(keys, filepath.Join(sshDir, name))
		}
	}
	return keys, nil
}
