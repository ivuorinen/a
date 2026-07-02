package cmd

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestApplyConfigDefaults(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	cfg := &Config{}
	require.NoError(t, applyConfigDefaults(cfg))
	assert.Contains(t, cfg.LogFilePath, filepath.Join(".state", "a", "cli.log"))

	cfg2 := &Config{LogFilePath: "/custom.log"}
	require.NoError(t, applyConfigDefaults(cfg2))
	assert.Equal(t, "/custom.log", cfg2.LogFilePath)
}

func TestLoadConfig_MissingReturnsDefaults(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	cfg, err := LoadConfig(filepath.Join(t.TempDir(), "nope.yaml"))
	require.NoError(t, err)
	assert.NotEmpty(t, cfg.LogFilePath)
}

func TestLoadConfig_RejectsGroupOtherPerms(t *testing.T) {
	p := filepath.Join(t.TempDir(), "config.yaml")
	// #nosec G306 -- intentionally lax perms to exercise the permission enforcement in LoadConfig
	require.NoError(t, os.WriteFile(p, []byte("github_user: x\n"), 0o644))
	_, err := LoadConfig(p)
	assert.ErrorContains(t, err, "group/other accessible")
}

func TestLoadConfig_AcceptsStricterPerms(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	p := filepath.Join(t.TempDir(), "config.yaml")
	require.NoError(t, os.WriteFile(p, []byte("github_user: ok\n"), 0o600))
	require.NoError(t, os.Chmod(p, 0o400)) // stricter than 0600 must be accepted
	cfg, err := LoadConfig(p)
	require.NoError(t, err)
	assert.Equal(t, "ok", cfg.GitHubUser)
}

func TestLoadConfig_BadYAML(t *testing.T) {
	p := filepath.Join(t.TempDir(), "config.yaml")
	require.NoError(t, os.WriteFile(p, []byte("github_user: [unterminated\n"), 0o600))
	_, err := LoadConfig(p)
	assert.Error(t, err)
}

func TestSaveConfig_Error(t *testing.T) {
	// Parent directory does not exist.
	err := SaveConfig(filepath.Join(t.TempDir(), "missing-dir", "config.yaml"), &Config{})
	assert.Error(t, err)
}

func TestInitConfigPaths_Full(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "cfg"))
	t.Setenv("XDG_CACHE_HOME", filepath.Join(home, "cache"))

	paths, err := InitConfigPaths()
	require.NoError(t, err)
	assert.DirExists(t, filepath.Dir(paths.ConfigFile))
	assert.FileExists(t, paths.ConfigFile)
	assert.DirExists(t, paths.CacheDir)

	cfg, err := LoadConfig(paths.ConfigFile)
	require.NoError(t, err)
	assert.Equal(t, 120, cfg.CacheTTLMinutes, "bootstrapped config should default cache TTL to 120")

	// Idempotent: a second call must not error or overwrite the existing config.
	require.NoError(t, os.WriteFile(paths.ConfigFile, []byte("github_user: keepme\n"), 0o600))
	_, err = InitConfigPaths()
	require.NoError(t, err)
	reloaded, err := LoadConfig(paths.ConfigFile)
	require.NoError(t, err)
	assert.Equal(t, "keepme", reloaded.GitHubUser, "existing config must not be overwritten")
}

func TestScanSSHPrivateKeys(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	// Missing ~/.ssh -> error.
	_, err := ScanSSHPrivateKeys()
	assert.Error(t, err)

	sshDir := filepath.Join(home, ".ssh")
	require.NoError(t, os.MkdirAll(sshDir, 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(sshDir, "id_rsa"), []byte("k"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(sshDir, "id_rsa.pub"), []byte("k"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(sshDir, "config"), []byte("x"), 0o600))
	require.NoError(t, os.MkdirAll(filepath.Join(sshDir, "id_subdir"), 0o700))

	keys, err := ScanSSHPrivateKeys()
	require.NoError(t, err)
	assert.Equal(t, []string{filepath.Join(sshDir, "id_rsa")}, keys,
		"only id_* non-.pub regular files should be returned")
}
