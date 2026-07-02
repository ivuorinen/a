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
		configDir = filepath.Join(os.Getenv("HOME"), ".config")
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
	// Reject only group/other access; stricter modes such as 0400 are fine.
	if perm := info.Mode().Perm(); perm&0o077 != 0 {
		return nil, fmt.Errorf("config file %s must not be group/other accessible (perms %#o)", cfgFile, perm)
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
func applyConfigDefaults(cfg *Config) error {
	if cfg.LogFilePath == "" {
		stateDir := filepath.Join(os.Getenv("HOME"), ".state", "a")
		// #nosec G703 -- stateDir is derived from HOME plus constants, not user input
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
func ScanSSHPrivateKeys() ([]string, error) {
	sshDir := filepath.Join(os.Getenv("HOME"), ".ssh")
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
