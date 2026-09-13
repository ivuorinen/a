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

// fileHome points HOME at a regular file so applyConfigDefaults can't create its
// state directory.
//
// XDG_STATE_HOME is cleared too. It takes precedence over HOME, so a value
// inherited from the developer's environment both defeated this fault injection
// and pointed the test's MkdirAll at the real ~/.local/state.
func fileHome(t *testing.T) {
	t.Helper()
	h := filepath.Join(t.TempDir(), "home")
	require.NoError(t, os.WriteFile(h, []byte("x"), 0o600))
	t.Setenv("HOME", h)
	t.Setenv("XDG_STATE_HOME", "")
}

// The config and cache MkdirAll failures produce the same "not a directory"
// text, so each test also asserts the path in the message. Without that, the
// two are interchangeable: swap the branches in InitConfigPaths and both still
// pass, proving only that something somewhere failed.
func TestInitConfigPaths_ConfigMkdirError(t *testing.T) {
	blocked := blockedPath(t)
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", blocked)
	t.Setenv("XDG_CACHE_HOME", filepath.Join(t.TempDir(), "cache"))

	_, err := InitConfigPaths()
	require.Error(t, err)
	assert.ErrorContains(t, err, "not a directory")
	// filepath.Dir: MkdirAll names the first non-directory component it hit,
	// which is the "notadir" file itself, not the child path handed to it.
	assert.ErrorContains(t, err, filepath.Dir(blocked), "the failure must name the config path")
}

func TestInitConfigPaths_CacheMkdirError(t *testing.T) {
	blocked := blockedPath(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "cfg"))
	t.Setenv("XDG_CACHE_HOME", blocked)

	_, err := InitConfigPaths()
	require.Error(t, err)
	assert.ErrorContains(t, err, "not a directory")
	assert.ErrorContains(t, err, filepath.Dir(blocked), "the failure must name the cache path")
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
	require.Error(t, err)
	assert.ErrorContains(t, err, "not a directory",
		"the state-dir MkdirAll failure must reach the caller")
}

func TestApplyConfigDefaults_MkdirError(t *testing.T) {
	fileHome(t)
	assert.ErrorContains(t, applyConfigDefaults(&Config{}), "not a directory")
}

// Missing config file + a HOME that blocks ~/.state: the defaults error must
// surface from the not-exist branch too.
func TestLoadConfig_MissingDefaultsError(t *testing.T) {
	fileHome(t)
	_, err := LoadConfig(filepath.Join(t.TempDir(), "nope.yaml"))
	assert.ErrorContains(t, err, "not a directory")
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
	assert.ErrorContains(t, err, "neither $XDG_CONFIG_HOME nor $HOME are defined")
}

func TestLoadConfig_ReadError(t *testing.T) {
	// A directory passes the perm check (0700 has no group/other bits) but ReadFile fails.
	dir := filepath.Join(t.TempDir(), "config.yaml")
	require.NoError(t, os.Mkdir(dir, 0o700))

	_, err := LoadConfig(dir)
	assert.ErrorContains(t, err, "is a directory",
		"the read failure must be distinguishable from the perm rejection above it")
}

// Named for what it actually covers. As TestEncryptCmd_AgeFailure it read as
// coverage of a failure inside age, which it never reaches: parseRecipients
// rejects the recipient first, so encryptFile is never called. That name is why
// the real encryptFile-failure path looked covered while nothing exercised it
// (see TestEncryptCmd_EncryptFileFailureSurfaces).
func TestEncryptCmd_BadRecipientFailure(t *testing.T) {
	in := filepath.Join(t.TempDir(), "in.txt")
	require.NoError(t, os.WriteFile(in, []byte("data"), 0o600))
	// A bogus recipient string is not a file and not a valid ssh/age key, so
	// parseRecipients rejects it and RunE fails.
	c := Encrypt(&Config{DefaultRecipients: []string{"age1bogusrecipient"}}, discardLogger())
	require.NoError(t, c.Flags().Set("input", in))
	require.NoError(t, c.Flags().Set("output", filepath.Join(t.TempDir(), "o.age")))
	assert.ErrorContains(t, c.RunE(c, nil), "invalid recipient")
}
