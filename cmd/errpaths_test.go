package cmd

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// blockedPath returns a path *under* a regular file. Any MkdirAll/Stat/ReadFile on
// it fails with ENOTDIR — root-independent fault injection for error branches.
func blockedPath(t *testing.T) string {
	t.Helper()
	f := filepath.Join(t.TempDir(), "notadir")
	require.NoError(t, os.WriteFile(f, []byte("x"), 0o600))
	return filepath.Join(f, "child")
}

// fileHome points HOME at a regular file so applyConfigDefaults can't create ~/.state.
func fileHome(t *testing.T) {
	t.Helper()
	h := filepath.Join(t.TempDir(), "home")
	require.NoError(t, os.WriteFile(h, []byte("x"), 0o600))
	t.Setenv("HOME", h)
}

func TestInitConfigPaths_ConfigMkdirError(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", blockedPath(t))
	t.Setenv("XDG_CACHE_HOME", filepath.Join(t.TempDir(), "cache"))
	_, err := InitConfigPaths()
	assert.Error(t, err)
}

func TestInitConfigPaths_CacheMkdirError(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "cfg"))
	t.Setenv("XDG_CACHE_HOME", blockedPath(t))
	_, err := InitConfigPaths()
	assert.Error(t, err)
}

func TestLoadConfig_StatError(t *testing.T) {
	_, err := LoadConfig(blockedPath(t))
	assert.ErrorContains(t, err, "could not stat")
}

func TestLoadConfig_DefaultsError(t *testing.T) {
	fileHome(t)
	p := filepath.Join(t.TempDir(), "config.yaml")
	require.NoError(t, os.WriteFile(p, []byte("github_user: x\n"), 0o600))
	_, err := LoadConfig(p)
	assert.Error(t, err)
}

func TestApplyConfigDefaults_MkdirError(t *testing.T) {
	fileHome(t)
	assert.Error(t, applyConfigDefaults(&Config{}))
}

// Missing config file + a HOME that blocks ~/.state: the defaults error must
// surface from the not-exist branch too.
func TestLoadConfig_MissingDefaultsError(t *testing.T) {
	fileHome(t)
	_, err := LoadConfig(filepath.Join(t.TempDir(), "nope.yaml"))
	assert.Error(t, err)
}

func TestReadKeyCache_Unreadable(t *testing.T) {
	// A directory at cachePath: Stat succeeds and is fresh, but ReadFile fails (EISDIR).
	dir := filepath.Join(t.TempDir(), "user.keys")
	require.NoError(t, os.Mkdir(dir, 0o700))
	_, ok := readKeyCache(dir, 60)
	assert.False(t, ok)
}

func TestInitConfigPaths_UserConfigDirError(t *testing.T) {
	t.Setenv("HOME", "")
	t.Setenv("XDG_CONFIG_HOME", "")
	_, err := InitConfigPaths()
	assert.Error(t, err)
}

func TestLoadConfig_ReadError(t *testing.T) {
	// A directory passes the perm check (0700 has no group/other bits) but ReadFile fails.
	dir := filepath.Join(t.TempDir(), "config.yaml")
	require.NoError(t, os.Mkdir(dir, 0o700))
	_, err := LoadConfig(dir)
	assert.Error(t, err)
}

func TestEncryptCmd_AgeFailure(t *testing.T) {
	in := filepath.Join(t.TempDir(), "in.txt")
	require.NoError(t, os.WriteFile(in, []byte("data"), 0o600))
	// A bogus recipient string is not a file and not a valid ssh/age key, so
	// parseRecipients rejects it and RunE fails.
	c := Encrypt(&Config{DefaultRecipients: []string{"age1bogusrecipient"}}, discardLogger())
	require.NoError(t, c.Flags().Set("input", in))
	require.NoError(t, c.Flags().Set("output", filepath.Join(t.TempDir(), "o.age")))
	assert.Error(t, c.RunE(c, nil))
}
