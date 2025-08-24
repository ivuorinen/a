package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"gopkg.in/yaml.v3"
)

// Config represents the application's YAML configuration.
type Config struct {
	SSHKeyPath        string   `yaml:"ssh_key_path"`
	GitHubUser        string   `yaml:"github_user"`
	DefaultRecipients []string `yaml:"default_recipients"`
	CacheTTLMinutes   int      `yaml:"cache_ttl_minutes"`
	LogFilePath       string   `yaml:"log_file_path"`
}

// ConfigPaths holds config and cache file paths.
type ConfigPaths struct {
	ConfigDir  string
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
	if err := os.MkdirAll(cfgDir, 0o700); err != nil {
		return ConfigPaths{}, err
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
		ConfigDir:  cfgDir,
		ConfigFile: cfgFile,
		CacheDir:   cacheDir,
	}, nil
}

// LoadConfig loads configuration from the YAML file.
// gosec G304: cfgFile is always set by InitConfigPaths and not user-controlled.
func LoadConfig(cfgFile string) (*Config, error) {
	// gosec G304 mitigation: Ensure cfgFile is within the expected config directory
	configDir, err := os.UserConfigDir()
	if err != nil {
		return nil, err
	}
	expectedDir := filepath.Join(configDir, "a")
	absCfgFile, err := filepath.Abs(cfgFile)
	if err != nil {
		return nil, err
	}
	if !strings.HasPrefix(absCfgFile, expectedDir) {
		return nil, fmt.Errorf(
			"config file path %s is not within expected config directory %s",
			absCfgFile,
			expectedDir,
		)
	}
	if _, err := os.Stat(cfgFile); err != nil {
		return nil, fmt.Errorf("config file does not exist: %w", err)
	}

	info, err := os.Stat(cfgFile)
	if err != nil {
		return nil, fmt.Errorf("config file does not exist: %w", err)
	}
	if info.Mode().Perm() != 0o600 {
		return nil, fmt.Errorf("config file must have 0600 permissions, got %o", info.Mode().Perm())
	}
	// #nosec G304 -- cfgFile is validated to be within the config directory
	data, err := os.ReadFile(cfgFile)
	if err != nil {
		return nil, err
	}
	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, err
	}
	if cfg.LogFilePath == "" {
		stateDir := filepath.Join(os.Getenv("HOME"), ".state", "a")
		if err := os.MkdirAll(stateDir, 0o700); err != nil {
			return nil, err
		}
		cfg.LogFilePath = filepath.Join(stateDir, "cli.log")
	}
	return &cfg, nil
}

// SaveConfig saves configuration to the YAML file.
func SaveConfig(cfgFile string, cfg *Config) error {
	data, err := yaml.Marshal(cfg)
	if err != nil {
		return err
	}
	return os.WriteFile(cfgFile, data, 0o600)
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
