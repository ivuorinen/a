package cmd

import (
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func discardLogger() *slog.Logger {
	return slog.New(slog.NewJSONHandler(io.Discard, nil))
}

func TestParseKeyLines(t *testing.T) {
	got := parseKeyLines("ssh-ed25519 AAA\n\n  ssh-rsa BBB  \n")
	assert.Equal(t, []string{"ssh-ed25519 AAA", "ssh-rsa BBB"}, got)
	assert.Nil(t, parseKeyLines("\n   \n"), "blank body yields no keys")
}

func TestReadKeyCache(t *testing.T) {
	dir := t.TempDir()
	cachePath := filepath.Join(dir, "octocat.keys")
	require.NoError(t, os.WriteFile(cachePath, []byte("ssh-ed25519 AAA\n"), 0o600))

	keys, ok := readKeyCache(cachePath, 60)
	require.True(t, ok, "fresh cache should be a hit")
	assert.Equal(t, []string{"ssh-ed25519 AAA"}, keys)

	// Age the file beyond the TTL: it must now miss.
	old := time.Now().Add(-2 * time.Hour)
	require.NoError(t, os.Chtimes(cachePath, old, old))
	_, ok = readKeyCache(cachePath, 60)
	assert.False(t, ok, "stale cache should miss")

	_, ok = readKeyCache(filepath.Join(dir, "missing.keys"), 60)
	assert.False(t, ok, "absent cache should miss")
}

func TestWriteKeyCache(t *testing.T) {
	dir := t.TempDir()
	cachePath := filepath.Join(dir, "user.keys")
	writeKeyCache(cachePath, []byte("ssh-rsa X\n"), discardLogger())

	data, err := os.ReadFile(cachePath) // #nosec G304 -- test-controlled temp path
	require.NoError(t, err)
	assert.Equal(t, "ssh-rsa X\n", string(data))

	info, err := os.Stat(cachePath)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o600), info.Mode().Perm(), "cache file must be 0600")
}

// A fresh cache entry must be served without any network access.
func TestFetchGitHubKeys_CacheHit(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "cacheduser.keys"), []byte("ssh-ed25519 CACHED\n"), 0o600))

	cfg := &Config{CacheDir: dir, CacheTTLMinutes: 60}
	keys := fetchGitHubKeys(cfg, "cacheduser", discardLogger())
	assert.Equal(t, []string{"ssh-ed25519 CACHED"}, keys)
}

// parseRecipients accepts SSH public-key files and literal ssh key strings (as
// GitHub returns them); a full encrypt/decrypt round trip through the age library
// recovers the input and writes the plaintext 0600.
func TestParseRecipientsAndRoundTrip(t *testing.T) {
	dir := t.TempDir()
	priv, pub := makeSSHKey(t, dir)

	fromFile, err := parseRecipients([]string{pub})
	require.NoError(t, err)
	assert.Len(t, fromFile, 1, "recipient parsed from .pub file")

	pubBytes, err := os.ReadFile(pub) // #nosec G304 -- test temp path
	require.NoError(t, err)
	fromString, err := parseRecipients([]string{strings.TrimSpace(string(pubBytes))})
	require.NoError(t, err)
	assert.Len(t, fromString, 1, "recipient parsed from raw key string")

	plain := filepath.Join(dir, "msg.txt")
	require.NoError(t, os.WriteFile(plain, []byte("library secret"), 0o600))
	enc := filepath.Join(dir, "msg.age")
	require.NoError(t, encryptFile(plain, enc, fromFile))

	dec := filepath.Join(dir, "msg.dec")
	require.NoError(t, tryDecrypt(priv, dec, enc))
	got, err := os.ReadFile(dec) // #nosec G304 -- test temp path
	require.NoError(t, err)
	assert.Equal(t, "library secret", string(got))

	info, err := os.Stat(dec)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o600), info.Mode().Perm(), "decrypted output must be 0600")
}

func TestParseRecipients_Invalid(t *testing.T) {
	_, err := parseRecipients([]string{""})
	assert.ErrorContains(t, err, "empty recipient")

	_, err = parseRecipients([]string{"garbage-not-a-key"})
	assert.Error(t, err, "unparseable recipient should error")
}

// An empty recipient from config must surface parseRecipients's error through Encrypt's RunE.
func TestEncryptCmd_BuildArgsError(t *testing.T) {
	in := filepath.Join(t.TempDir(), "in.txt")
	require.NoError(t, os.WriteFile(in, []byte("data"), 0o600))

	c := Encrypt(&Config{DefaultRecipients: []string{""}}, discardLogger())
	require.NoError(t, c.Flags().Set("input", in))
	require.NoError(t, c.Flags().Set("output", filepath.Join(t.TempDir(), "o.txt")))
	assert.ErrorContains(t, c.RunE(c, nil), "empty recipient")
}

func TestWriteKeyCache_ErrorIsNonFatal(t *testing.T) {
	// Parent directory does not exist: write fails but must not panic.
	badPath := filepath.Join(t.TempDir(), "missing-dir", "u.keys")
	writeKeyCache(badPath, []byte("ssh-rsa X\n"), discardLogger())
	assert.NoFileExists(t, badPath)
}
