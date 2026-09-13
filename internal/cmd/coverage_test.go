package cmd

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// requireNonRoot skips tests whose fault injection is a permission bit.
//
// root ignores the mode bits these tests rely on, so under root the syscall
// succeeds and the test asserts an error that never comes — a failure that says
// nothing about the code. The suite's other fault injection (blockedPath, which
// uses ENOTDIR) is root-independent and needs no guard; only these do.
func requireNonRoot(t *testing.T) {
	t.Helper()
	if os.Geteuid() == 0 {
		t.Skip("permission-based fault injection is meaningless as root")
	}
}

// readOnlyDir returns a directory that exists but cannot be written to, so any
// CreateTemp or WriteFile inside it fails with EACCES. Restored on cleanup so
// t.TempDir's own removal can succeed.
func readOnlyDir(t *testing.T) string {
	t.Helper()
	requireNonRoot(t)
	d := filepath.Join(t.TempDir(), "readonly")
	require.NoError(t, os.Mkdir(d, 0o500))
	// #nosec G302 -- a directory needs its execute bit; 0700 is restored only so
	// t.TempDir's own cleanup can remove it.
	t.Cleanup(func() { _ = os.Chmod(d, 0o700) })
	return d
}

// ---------------------------------------------------------------------------
// encrypt: paths reachable only through the command's own argument handling
// ---------------------------------------------------------------------------

// The primary documented invocation, `a e message.txt`, takes its input from a
// positional rather than --input. In-process nothing exercised that branch of
// resolveIO; only the out-of-process integration test did, where the coverage
// profile cannot see it.
func TestEncryptCmd_PositionalInput(t *testing.T) {
	dir := t.TempDir()
	in := filepath.Join(dir, "message.txt")
	require.NoError(t, os.WriteFile(in, []byte("positional secret"), 0o600))
	priv, pub := makeSSHKey(t, dir)

	c := Encrypt(&Config{DefaultRecipients: []string{pub}}, discardLogger())
	require.NoError(t, c.RunE(c, []string{in}), "input must be taken from the positional")

	enc := in + ".age"
	require.FileExists(t, enc, "output must derive to <input>.age")

	// Round-trip it: proving the file exists says nothing about it being the
	// right ciphertext for the right recipient.
	dec := filepath.Join(dir, "out.txt")
	require.NoError(t, tryDecrypt(priv, dec, enc))
	got, err := os.ReadFile(dec) // #nosec G304 -- test temp path
	require.NoError(t, err)
	assert.Equal(t, "positional secret", string(got))
}

// resolveIO's "output file is required" branch needs a derived output that comes
// back empty, which only decryptOutput can produce: an input named exactly
// ".age" strips to "". The Stat on the input happens after this check, so the
// file need not exist.
func TestDecryptCmd_EmptyDerivedOutput(t *testing.T) {
	c := Decrypt(&Config{}, discardLogger())
	require.NoError(t, c.Flags().Set("input", ".age"))
	assert.ErrorContains(t, c.RunE(c, nil), "output file is required")
}

// ensureWritableOutput must distinguish "not there, go ahead" from "cannot tell".
// A path under a regular file yields ENOTDIR rather than ErrNotExist, and the
// command must stop rather than treat an unreadable target as absent.
func TestEnsureWritableOutput_StatError(t *testing.T) {
	blocker := filepath.Join(t.TempDir(), "notadir")
	require.NoError(t, os.WriteFile(blocker, []byte("x"), 0o600))

	err := ensureWritableOutput(filepath.Join(blocker, "child.age"), false)
	require.Error(t, err)
	assert.ErrorContains(t, err, "checking output",
		"an unreadable target must not be mistaken for an absent one")
}

// githubKeysURL is replaced by every network test, so its real body — the thing
// that decides which host the tool contacts — was never executed.
func TestGitHubKeysURL(t *testing.T) {
	assert.Equal(t, "https://github.com/octocat.keys", githubKeysURL("octocat"),
		"keys must be fetched from github.com over https")
}

// ---------------------------------------------------------------------------
// encrypt: recipient parsing failure modes
// ---------------------------------------------------------------------------

// A recipient file that exists but cannot be read must fail loudly. Silently
// treating it as a literal would encrypt to a recipient named after a file path.
func TestLinesForInput_UnreadableFile(t *testing.T) {
	requireNonRoot(t)
	p := filepath.Join(t.TempDir(), "recipients.txt")
	require.NoError(t, os.WriteFile(p, []byte("ssh-ed25519 AAA\n"), 0o600))
	require.NoError(t, os.Chmod(p, 0o000))
	t.Cleanup(func() { _ = os.Chmod(p, 0o600) })

	_, err := linesForInput(p)
	require.Error(t, err)
	assert.ErrorContains(t, err, "reading recipient file")

	// The same failure must propagate out of parseRecipients, not be swallowed.
	_, err = parseRecipients([]string{p})
	assert.ErrorContains(t, err, "reading recipient file")
}

// A recipient naming a directory must be treated as a literal string, not read.
// os.Stat succeeds for a directory, so only the !IsDir guard stops linesForInput
// from calling os.ReadFile on it; without that guard the EISDIR error would be
// reported as an unreadable recipient file rather than as the invalid recipient
// it is.
func TestLinesForInput_DirectoryIsNotAFile(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "recipients.d")
	require.NoError(t, os.Mkdir(dir, 0o700))

	lines, err := linesForInput(dir)
	require.NoError(t, err, "a directory must fall through to the literal path, not be read")
	assert.Equal(t, []string{dir}, lines)

	_, err = parseRecipients([]string{dir})
	assert.ErrorContains(t, err, "invalid recipient",
		"a directory is rejected as an unparseable recipient, not as an unreadable file")
}

// A recipients file of nothing but comments and blanks parses cleanly to zero
// recipients. Encrypting to nobody produces a file its author cannot open, so
// this must be an error rather than an empty recipient set.
func TestParseRecipients_CommentsOnlyFile(t *testing.T) {
	p := filepath.Join(t.TempDir(), "recipients.txt")
	require.NoError(t, os.WriteFile(p, []byte("# just a comment\n\n   \n# another\n"), 0o600))

	_, err := parseRecipients([]string{p})
	assert.ErrorContains(t, err, "no valid recipients found")
}

// ---------------------------------------------------------------------------
// encrypt: encryptFile write-path failures
// ---------------------------------------------------------------------------

// Each of encryptFile's write failures must surface as an error and leave
// nothing behind. Together with TestEncryptFile_FailureLeavesPreexistingIntact
// these close the branch set that turns a broken write into a silent success.
func TestEncryptFile_WriteFailures(t *testing.T) {
	dir := t.TempDir()
	in := filepath.Join(dir, "in.txt")
	require.NoError(t, os.WriteFile(in, []byte("data"), 0o600))
	_, pub := makeSSHKey(t, dir)
	recips, err := parseRecipients([]string{pub})
	require.NoError(t, err)

	t.Run("temp file cannot be created", func(t *testing.T) {
		locked := readOnlyDir(t)
		err := encryptFile(in, filepath.Join(locked, "out.age"), recips)
		assert.ErrorContains(t, err, "creating temp output")
	})

	t.Run("input cannot be read after opening", func(t *testing.T) {
		// A directory opens fine and then fails on read with EISDIR, so the
		// failure lands inside io.Copy rather than at os.Open.
		srcDir := filepath.Join(t.TempDir(), "adir")
		require.NoError(t, os.Mkdir(srcDir, 0o700))
		out := filepath.Join(t.TempDir(), "out.age")

		err := encryptFile(srcDir, out, recips)
		assert.ErrorContains(t, err, "writing ciphertext")
		assert.NoFileExists(t, out, "a failed copy must leave no output")
	})

	t.Run("output cannot be renamed into place", func(t *testing.T) {
		// An existing directory at the output path: the temp file is written
		// successfully and only the final rename fails.
		parent := t.TempDir()
		out := filepath.Join(parent, "out.age")
		require.NoError(t, os.Mkdir(out, 0o700))

		err := encryptFile(in, out, recips)
		assert.ErrorContains(t, err, "finalizing output")

		entries, readErr := os.ReadDir(parent)
		require.NoError(t, readErr)
		for _, e := range entries {
			assert.NotContains(t, e.Name(), ".a-encrypt", "temp file must be cleaned up")
		}
	})
}

// ---------------------------------------------------------------------------
// decrypt: key parsing and write-path failures
// ---------------------------------------------------------------------------

// makePEMEncryptedKey writes a passphrase-protected key in the legacy PEM format.
//
// Unlike the OPENSSH format makeEncryptedSSHKey produces, PEM does not embed the
// public key, so ssh.PassphraseMissingError comes back with PublicKey == nil.
// That is the only way to reach parseSSHIdentity's .pub fallback, which exists
// precisely for these older keys.
func makePEMEncryptedKey(t *testing.T, dir, passphrase string) (priv, pub string) {
	t.Helper()
	requireSSHKeygen(t)
	priv = filepath.Join(dir, "id_rsa")
	// #nosec G204 -- test helper; dir is a test temp dir
	out, err := exec.Command("ssh-keygen",
		"-t", "rsa", "-b", "2048", "-m", "PEM", "-N", passphrase, "-f", priv).CombinedOutput()
	require.NoError(t, err, string(out))

	pemBytes, err := os.ReadFile(priv) // #nosec G304 -- test temp path
	require.NoError(t, err)
	// Proc-Type rather than the BEGIN line: it is the marker that actually
	// distinguishes an encrypted legacy PEM key (no embedded public key, which
	// is the whole point here) from the OPENSSH format, and asserting on a
	// BEGIN...PRIVATE KEY literal trips the detect-private-key pre-commit hook.
	require.Contains(t, string(pemBytes), "Proc-Type: 4,ENCRYPTED",
		"ssh-keygen -m PEM must produce an encrypted legacy PEM key, not OPENSSH")
	return priv, priv + ".pub"
}

// A PEM key carries no embedded public key, so parseSSHIdentity has to read the
// sibling .pub. All three outcomes matter: found and parseable, absent, and
// present but corrupt.
func TestParseSSHIdentity_PubKeyFallback(t *testing.T) {
	const pass = "correct horse battery staple"

	t.Run("sibling .pub is used", func(t *testing.T) {
		dir := t.TempDir()
		priv, pub := makePEMEncryptedKey(t, dir, pass)
		withPassphrase(t, pass)

		recips, err := parseRecipients([]string{pub})
		require.NoError(t, err)
		plain := filepath.Join(dir, "m.txt")
		require.NoError(t, os.WriteFile(plain, []byte("pem secret"), 0o600))
		enc := filepath.Join(dir, "m.age")
		require.NoError(t, encryptFile(plain, enc, recips))

		dec := filepath.Join(dir, "m.dec")
		require.NoError(t, tryDecrypt(priv, dec, enc),
			"a PEM key must decrypt via its sibling .pub")
		got, err := os.ReadFile(dec) // #nosec G304 -- test temp path
		require.NoError(t, err)
		assert.Equal(t, "pem secret", string(got))
	})

	t.Run("missing .pub names the conversion command", func(t *testing.T) {
		dir := t.TempDir()
		priv, pub := makePEMEncryptedKey(t, dir, pass)
		require.NoError(t, os.Remove(pub))

		pemBytes, err := os.ReadFile(priv) // #nosec G304 -- test temp path
		require.NoError(t, err)
		_, err = parseSSHIdentity(priv, pemBytes)
		require.Error(t, err)
		assert.ErrorContains(t, err, "public key is unavailable")
		assert.ErrorContains(t, err, "ssh-keygen -p -m RFC4716",
			"the error must name the fix, not just the problem")
	})

	t.Run("corrupt .pub is reported against the .pub path", func(t *testing.T) {
		dir := t.TempDir()
		priv, pub := makePEMEncryptedKey(t, dir, pass)
		require.NoError(t, os.WriteFile(pub, []byte("not a public key\n"), 0o600))

		pemBytes, err := os.ReadFile(priv) // #nosec G304 -- test temp path
		require.NoError(t, err)
		_, err = parseSSHIdentity(priv, pemBytes)
		require.Error(t, err)
		assert.ErrorContains(t, err, pub, "the error must name the file it could not parse")
	})
}

// tryDecrypt's failures before any plaintext exists must each name their stage,
// so the user can tell a bad key from a missing input from an unwritable target.
func TestTryDecrypt_PreWriteFailures(t *testing.T) {
	dir := t.TempDir()
	priv, pub := makeSSHKey(t, dir)
	recips, err := parseRecipients([]string{pub})
	require.NoError(t, err)
	plain := filepath.Join(dir, "m.txt")
	require.NoError(t, os.WriteFile(plain, []byte("secret"), 0o600))
	enc := filepath.Join(dir, "m.age")
	require.NoError(t, encryptFile(plain, enc, recips))

	t.Run("unparseable key", func(t *testing.T) {
		junk := filepath.Join(t.TempDir(), "id_junk")
		require.NoError(t, os.WriteFile(junk, []byte("not a key at all\n"), 0o600))
		err := tryDecrypt(junk, filepath.Join(t.TempDir(), "o"), enc)
		assert.ErrorContains(t, err, "parsing key")
	})

	t.Run("missing input", func(t *testing.T) {
		err := tryDecrypt(priv, filepath.Join(t.TempDir(), "o"), filepath.Join(t.TempDir(), "gone.age"))
		assert.ErrorContains(t, err, "opening input")
	})
}

// After a successful decrypt the plaintext still has to reach the target. Both
// failures here happen with plaintext in hand, so each must also prove nothing
// was left on disk.
func TestTryDecrypt_WriteFailures(t *testing.T) {
	dir := t.TempDir()
	priv, pub := makeSSHKey(t, dir)
	recips, err := parseRecipients([]string{pub})
	require.NoError(t, err)
	plain := filepath.Join(dir, "m.txt")
	require.NoError(t, os.WriteFile(plain, []byte("secret"), 0o600))
	enc := filepath.Join(dir, "m.age")
	require.NoError(t, encryptFile(plain, enc, recips))

	t.Run("temp file cannot be created", func(t *testing.T) {
		locked := readOnlyDir(t)
		err := tryDecrypt(priv, filepath.Join(locked, "out.txt"), enc)
		assert.ErrorContains(t, err, "creating temp output")
	})

	t.Run("plaintext cannot be renamed into place", func(t *testing.T) {
		parent := t.TempDir()
		out := filepath.Join(parent, "out.txt")
		require.NoError(t, os.Mkdir(out, 0o700))

		err := tryDecrypt(priv, out, enc)
		assert.ErrorContains(t, err, "finalizing output")

		entries, readErr := os.ReadDir(parent)
		require.NoError(t, readErr)
		for _, e := range entries {
			assert.NotContains(t, e.Name(), ".a-decrypt",
				"a failed rename must not strand plaintext in the target directory")
		}
	})
}

// ---------------------------------------------------------------------------
// config: bootstrap and home-resolution failures
// ---------------------------------------------------------------------------

// InitConfigPaths bootstraps a default config on first run. When the config
// directory exists but is not writable, that write fails and the error must
// surface rather than leaving the caller with a path to a file that is not there.
func TestInitConfigPaths_BootstrapSaveError(t *testing.T) {
	requireNonRoot(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	cfgHome := filepath.Join(home, "cfg")
	// Pre-create the "a" subdirectory read-only: MkdirAll then succeeds (it
	// already exists) and only SaveConfig's CreateTemp fails.
	require.NoError(t, os.MkdirAll(filepath.Join(cfgHome, "a"), 0o700))
	// #nosec G302 -- directories, so the execute bit is required; 0500 is the
	// fault injection and 0700 is restored for t.TempDir's cleanup.
	require.NoError(t, os.Chmod(filepath.Join(cfgHome, "a"), 0o500))
	// #nosec G302 -- see above
	t.Cleanup(func() { _ = os.Chmod(filepath.Join(cfgHome, "a"), 0o700) })
	t.Setenv("XDG_CONFIG_HOME", cfgHome)
	t.Setenv("XDG_CACHE_HOME", filepath.Join(home, "cache"))

	_, err := InitConfigPaths()
	require.Error(t, err, "a config directory that cannot be written to must not be reported as ready")
	assert.ErrorContains(t, err, "permission denied")
}

// UserCacheDir fails independently of UserConfigDir: XDG_CONFIG_HOME satisfies
// the first while an empty HOME and XDG_CACHE_HOME defeat the second.
func TestInitConfigPaths_UserCacheDirError(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("HOME", "")
	t.Setenv("XDG_CACHE_HOME", "")

	_, err := InitConfigPaths()
	require.Error(t, err)
	assert.ErrorContains(t, err, "$HOME", "the cache-dir failure must name the unresolvable home")
}

// applyConfigDefaults resolves the state directory through os.UserHomeDir rather
// than os.Getenv("HOME") so an unset HOME errors instead of silently producing
// the relative path ".local/state/a".
func TestApplyConfigDefaults_HomeError(t *testing.T) {
	t.Setenv("HOME", "")
	t.Setenv("XDG_STATE_HOME", "")

	err := applyConfigDefaults(&Config{})
	require.Error(t, err)
	assert.ErrorContains(t, err, "resolving home directory for the default log path")
}

// The same reasoning for the SSH scan: an unset HOME must error, not join to the
// relative ".ssh" and read keys out of the working directory.
func TestScanSSHPrivateKeys_HomeError(t *testing.T) {
	t.Setenv("HOME", "")

	_, err := ScanSSHPrivateKeys()
	require.Error(t, err)
	assert.ErrorContains(t, err, "resolving home directory to scan for SSH keys")
}

// A save that fails at the rename must not strand its temp file in the config
// directory, where the next run would find a stray .config-*.yaml.
func TestSaveConfig_RenameFailureCleansUpTemp(t *testing.T) {
	parent := t.TempDir()
	target := filepath.Join(parent, "config.yaml")
	require.NoError(t, os.Mkdir(target, 0o700)) // a directory: the rename cannot replace it

	err := SaveConfig(target, &Config{GitHubUser: "x"})
	require.Error(t, err)

	entries, readErr := os.ReadDir(parent)
	require.NoError(t, readErr)
	for _, e := range entries {
		assert.NotContains(t, e.Name(), ".config-", "the temp file must be removed on failure")
	}
}

// `rem` shares setConfigKey with `set`, so an unknown key must be rejected there
// too rather than silently succeeding.
func TestConfig_RemRejectsUnknownKey(t *testing.T) {
	_, err := runConfig(t, &Config{}, "rem", "nope")
	assert.ErrorContains(t, err, "unknown config key")
}

// A TTL past maxCacheTTLMinutes overflows time.Duration and wraps, so a bigger
// number can mean a shorter or negative lifetime. The boundary is accepted and
// one past it is refused, rather than silently disabling the cache the operator
// was trying to extend.
func TestConfig_SetCacheTTLRejectsOverflow(t *testing.T) {
	cfg := &Config{}
	_, err := runConfig(t, cfg, "set", "cache_ttl_minutes", strconv.Itoa(maxCacheTTLMinutes))
	require.NoError(t, err, "the boundary value must be accepted")
	assert.Equal(t, maxCacheTTLMinutes, cfg.CacheTTLMinutes)

	_, err = runConfig(t, cfg, "set", "cache_ttl_minutes", strconv.Itoa(maxCacheTTLMinutes+1))
	require.Error(t, err, "one past the boundary must be refused")
	assert.ErrorContains(t, err, "must be at most")
	assert.Equal(t, maxCacheTTLMinutes, cfg.CacheTTLMinutes, "the rejected value must not be stored")
}

// readKeyCache clamps, because a value written straight into config.yaml never
// passes through setConfigKey. Unclamped, these TTLs wrap negative and every
// lookup reports a miss.
func TestReadKeyCache_OverflowingTTLStillHits(t *testing.T) {
	cachePath := filepath.Join(t.TempDir(), "octocat.keys")
	require.NoError(t, os.WriteFile(cachePath, []byte("ssh-ed25519 AAA\n"), 0o600))

	// Every one of these wraps to a negative duration when multiplied unclamped.
	for _, ttl := range []int{maxCacheTTLMinutes + 1, 200000000, 307445734} {
		keys, ok := readKeyCache(cachePath, ttl)
		assert.True(t, ok, "ttl=%d: a fresh cache must hit, not wrap to always-stale", ttl)
		assert.Equal(t, []string{"ssh-ed25519 AAA"}, keys)
	}
}
