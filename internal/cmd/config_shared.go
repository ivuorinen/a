package cmd

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"gopkg.in/yaml.v3"
)

// defaultCacheTTLMinutes is the GitHub-key cache lifetime written to a freshly
// bootstrapped config. It matches the --cache-ttl flag default.
const defaultCacheTTLMinutes = 120

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
	var configDir string
	var err error

	// Personal preference, I don't like the "$HOME/Library/Application Support/" path
	if runtime.GOOS == "darwin" {
		home, homeErr := os.UserHomeDir()
		if homeErr != nil {
			return ConfigPaths{}, fmt.Errorf("resolving home directory for the config path: %w", homeErr)
		}
		configDir = filepath.Join(home, ".config")
	} else {
		configDir, err = os.UserConfigDir()
		if err != nil {
			return ConfigPaths{}, err
		}
	}

	cfgDir := filepath.Join(configDir, "a")
	cfgFile := filepath.Join(cfgDir, "config.yaml")
	// #nosec G703 -- cfgDir is derived from os.UserConfigDir/HOME plus a constant, not user input
	if err := os.MkdirAll(cfgDir, 0o700); err != nil {
		return ConfigPaths{}, err
	}

	// Materialize a default config on first run so the `config` command (and any
	// other command whose PreRun loads config) can bootstrap without a manual step.
	// #nosec G703 -- cfgFile is derived from os.UserConfigDir/HOME plus constants, not user input
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
// over cfgFile, so an interrupted or disk-full write cannot truncate or lose the
// existing config, and the result is always 0600 (which LoadConfig requires).
func SaveConfig(cfgFile string, cfg *Config) (err error) {
	data, err := yaml.Marshal(cfg)
	if err != nil {
		return err
	}
	// #nosec G304 -- cfgFile is supplied by InitConfigPaths (os.UserConfigDir-derived), not user input
	tmp, err := os.CreateTemp(filepath.Dir(cfgFile), ".config-*.yaml")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer func() {
		if err != nil {
			// #nosec G703 -- tmpName is CreateTemp's own path under the trusted config dir
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
	// #nosec G703 -- tmpName and cfgFile are both InitConfigPaths-derived, not user input
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
