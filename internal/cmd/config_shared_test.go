package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestApplyConfigDefaults(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_STATE_HOME", "")

	cfg := &Config{}
	require.NoError(t, applyConfigDefaults(cfg))
	assert.Contains(t, cfg.LogFilePath, filepath.Join(".local", "state", "a", "cli.log"))

	// XDG_STATE_HOME must win: config and cache already honor their XDG variables,
	// and the log ignoring its own stranded state in ~/.state whenever a user
	// relocated the other two.
	state := t.TempDir()
	t.Setenv("XDG_STATE_HOME", state)
	cfg3 := &Config{}
	require.NoError(t, applyConfigDefaults(cfg3))
	assert.Equal(t, filepath.Join(state, "a", "cli.log"), cfg3.LogFilePath)

	cfg2 := &Config{LogFilePath: "/custom.log"}
	require.NoError(t, applyConfigDefaults(cfg2))
	assert.Equal(t, "/custom.log", cfg2.LogFilePath)
}

func TestLoadConfig_MissingReturnsDefaults(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_STATE_HOME", "") // else the default log path escapes to the real home
	cfg, err := LoadConfig(filepath.Join(t.TempDir(), "nope.yaml"))
	require.NoError(t, err)
	assert.NotEmpty(t, cfg.LogFilePath)
}

// Each mode isolates one triplet. A single 0644 case sets bits in both, so it
// cannot tell the halves apart: narrowing the mask to 0o070 or 0o007 kept the
// test green while silently accepting a group- or other-readable config. 0610
// and 0601 cover the execute bits, which the check deliberately includes.
func TestLoadConfig_RejectsGroupOtherPerms(t *testing.T) {
	for _, mode := range []os.FileMode{0o640, 0o604, 0o644, 0o610, 0o601} {
		t.Run(fmt.Sprintf("%#o", mode), func(t *testing.T) {
			p := filepath.Join(t.TempDir(), "config.yaml")
			require.NoError(t, os.WriteFile(p, []byte("github_user: x\n"), 0o600))
			// Chmod after the write so the process umask cannot clear the bits
			// under test.
			require.NoError(t, os.Chmod(p, mode))

			_, err := LoadConfig(p)
			require.Error(t, err, "%#o must be refused", mode)
			assert.ErrorContains(t, err, "group/other accessible")
			// The check runs in PersistentPreRunE, so it blocks `config set` too
			// -- the remedy has to be in the message or the user is stuck guessing.
			assert.ErrorContains(t, err, "chmod 600", "the error must name the fix")
		})
	}
}

func TestLoadConfig_AcceptsStricterPerms(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_STATE_HOME", "") // else the default log path escapes to the real home
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
	assert.ErrorContains(t, err, "yaml:",
		"a malformed config must be reported as a parse failure, not swallowed into a default")
}

func TestSaveConfig_Error(t *testing.T) {
	// Parent directory does not exist, so CreateTemp fails before anything is
	// written. Asserting the message keeps this pinned to that stage rather
	// than to "some error happened".
	err := SaveConfig(filepath.Join(t.TempDir(), "missing-dir", "config.yaml"), &Config{})
	assert.ErrorContains(t, err, "no such file or directory")
}

func TestInitConfigPaths_Full(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "cfg"))
	t.Setenv("XDG_CACHE_HOME", filepath.Join(home, "cache"))
	t.Setenv("XDG_STATE_HOME", filepath.Join(home, "state"))

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

	// Missing ~/.ssh -> error naming the directory it could not read.
	_, err := ScanSSHPrivateKeys()
	require.Error(t, err)
	assert.ErrorContains(t, err, ".ssh")
	assert.ErrorContains(t, err, "no such file or directory")

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
