package cmd

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A failed decrypt (tampered ciphertext) must not leak plaintext to the output
// path, must not clobber a pre-existing file there, and must leave no temp files.
func TestTryDecrypt_FailureLeavesNoPlaintext(t *testing.T) {
	dir := t.TempDir()
	priv, pub := makeSSHKey(t, dir)
	recips, err := parseRecipients([]string{pub})
	require.NoError(t, err)

	// Plaintext larger than one age chunk (64 KiB) so a truncated ciphertext
	// fails mid-stream, after a plaintext chunk would otherwise be written.
	plain := filepath.Join(dir, "secret.txt")
	require.NoError(t, os.WriteFile(plain, bytes.Repeat([]byte("TOPSECRET"), 20000), 0o600))
	enc := filepath.Join(dir, "secret.age")
	require.NoError(t, encryptFile(plain, enc, recips))

	full, err := os.ReadFile(enc) // #nosec G304 -- test temp path
	require.NoError(t, err)
	tampered := filepath.Join(dir, "tampered.age")
	// #nosec G703 -- test temp path
	require.NoError(t, os.WriteFile(tampered, full[:len(full)/2], 0o600))

	// Intentionally a group/world-readable pre-existing file, to prove a failed
	// decrypt neither clobbers it nor leaves plaintext at loose perms.
	out := filepath.Join(dir, "out.plain")
	// #nosec G306 -- intentional loose perms on a pre-existing file (see above)
	require.NoError(t, os.WriteFile(out, []byte("PREEXISTING"), 0o644))

	assert.Error(t, tryDecrypt(priv, out, tampered), "tampered ciphertext must fail")

	got, err := os.ReadFile(out) // #nosec G304 -- test temp path
	require.NoError(t, err)
	assert.Equal(t, "PREEXISTING", string(got), "pre-existing file must be untouched")
	assert.NotContains(t, string(got), "TOPSECRET", "no plaintext may leak to the output path")

	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	for _, e := range entries {
		assert.NotContains(t, e.Name(), ".a-decrypt", "temp file must be cleaned up")
	}
}

func TestTryDecrypt_EmptyPath(t *testing.T) {
	assert.Error(t, tryDecrypt("", "o.txt", "i"))
	assert.Error(t, tryDecrypt("k", "", "i"))
	assert.Error(t, tryDecrypt("k", "o.txt", ""))
}

func TestSelectSSHKey(t *testing.T) {
	assert.Equal(t, "flagkey", selectSSHKey("flagkey", &Config{SSHKeyPath: "cfgkey"}))
	assert.Equal(t, "cfgkey", selectSSHKey("", &Config{SSHKeyPath: "cfgkey"}))
	assert.Empty(t, selectSSHKey("", &Config{}))
}

func TestTryAllKeys_NoMatch(t *testing.T) {
	// No key tried is never success: errors.Join of nothing is nil, which would
	// read as a successful decryption.
	tried, err := tryAllKeys(nil, "i", "o.txt", discardLogger())
	assert.Error(t, err)
	assert.Empty(t, tried)

	tried, err = tryAllKeys([]string{"/no/such/id_rsa"}, "i", "o.txt", discardLogger())
	require.Error(t, err)
	assert.Equal(t, []string{"/no/such/id_rsa"}, tried)
	assert.ErrorContains(t, err, "/no/such/id_rsa", "the failure must name the key it came from")
}

func TestDecryptCmd_Validation(t *testing.T) {
	run := func(flags map[string]string) error {
		c := Decrypt(&Config{}, discardLogger())
		for k, v := range flags {
			require.NoError(t, c.Flags().Set(k, v))
		}
		return c.RunE(c, nil)
	}
	assert.ErrorContains(t, run(nil), "input file is required")
	assert.ErrorContains(t,
		run(map[string]string{"input": "/no/such/file", "output": "o.txt"}),
		"input file does not exist")
}

// A stray positional alongside --input must stop the run: ignoring it accepted
// `decrypt -i a.age b.age` and silently decrypted only a.age.
func TestDecryptCmd_RejectsUnplaceableArg(t *testing.T) {
	dir := t.TempDir()
	in := filepath.Join(dir, "in.age")
	require.NoError(t, os.WriteFile(in, []byte("x"), 0o600))

	c := Decrypt(&Config{}, discardLogger())
	require.NoError(t, c.Flags().Set("input", in))
	require.NoError(t, c.Flags().Set("output", filepath.Join(dir, "o.txt")))
	assert.ErrorContains(t, c.RunE(c, []string{"/nonexistent/decoy.age"}), "unexpected argument")
}

func TestDecryptOutput(t *testing.T) {
	assert.Equal(t, "secret.txt", decryptOutput("secret.txt.age"), "strip .age")
	assert.Equal(t, "blob.dec", decryptOutput("blob"), "append .dec when no .age")
}

// With no ssh-key flag and no ~/.ssh directory, Decrypt surfaces the scan error.
func TestDecryptCmd_ScanError(t *testing.T) {
	home := t.TempDir() // intentionally has no .ssh directory
	t.Setenv("HOME", home)
	in := filepath.Join(home, "in.age")
	require.NoError(t, os.WriteFile(in, []byte("x"), 0o600))

	c := Decrypt(&Config{}, discardLogger())
	require.NoError(t, c.Flags().Set("input", in))
	require.NoError(t, c.Flags().Set("output", filepath.Join(home, "o.txt")))
	assert.ErrorContains(t, c.RunE(c, nil), "could not scan")
}

// requireSSHKeygen skips the test when ssh-keygen is absent.
//
// Key generation is a build-environment dependency, not a property of the code
// under test: on a slim runner image its absence otherwise reports as a product
// test failure whose error names nothing about the real cause. The CI test job
// runs on ubuntu-latest, which ships ssh-keygen, so this skip is a fallback and
// not the plan — a runner that starts hitting it has silently lost coverage.
func requireSSHKeygen(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("ssh-keygen"); err != nil {
		t.Skip("ssh-keygen not available")
	}
}

// makeSSHKey writes a fresh ed25519 keypair named id_ed25519 into dir.
func makeSSHKey(t *testing.T, dir string) (priv, pub string) {
	t.Helper()
	requireSSHKeygen(t)
	priv = filepath.Join(dir, "id_ed25519")
	// #nosec G204 -- test helper; dir is a test temp dir
	out, err := exec.Command("ssh-keygen", "-t", "ed25519", "-N", "", "-f", priv).CombinedOutput()
	require.NoError(t, err, string(out))
	return priv, priv + ".pub"
}

// makeEncryptedSSHKey writes a passphrase-protected ed25519 keypair. makeSSHKey
// generates unencrypted keys only, so nothing in the suite exercised the form
// ssh-keygen produces whenever its passphrase prompt is answered.
func makeEncryptedSSHKey(t *testing.T, dir, passphrase string) (priv, pub string) {
	t.Helper()
	requireSSHKeygen(t)
	priv = filepath.Join(dir, "id_ed25519")
	// #nosec G204 -- test helper; dir is a test temp dir
	out, err := exec.Command("ssh-keygen", "-t", "ed25519", "-N", passphrase, "-f", priv).CombinedOutput()
	require.NoError(t, err, string(out))
	return priv, priv + ".pub"
}

// withPassphrase supplies a passphrase without a terminal, so the successful
// decryption path for an encrypted key is reachable under `go test`.
func withPassphrase(t *testing.T, pass string) {
	t.Helper()
	orig := passphrasePrompt
	passphrasePrompt = func(string) ([]byte, error) { return []byte(pass), nil }
	t.Cleanup(func() { passphrasePrompt = orig })
}

// The point of the encrypted-key support: such a key must actually decrypt, not
// merely be diagnosed. Verified by hand against a real tty before this test
// existed, which is not a regression test.
func TestTryDecrypt_PassphraseProtectedKeyRoundTrip(t *testing.T) {
	const pass = "correct horse battery staple"
	dir := t.TempDir()
	priv, pub := makeEncryptedSSHKey(t, dir, pass)
	withPassphrase(t, pass)

	recips, err := parseRecipients([]string{pub})
	require.NoError(t, err)
	plain := filepath.Join(dir, "m.txt")
	require.NoError(t, os.WriteFile(plain, []byte("passphrase secret"), 0o600))
	enc := filepath.Join(dir, "m.age")
	require.NoError(t, encryptFile(plain, enc, recips))

	dec := filepath.Join(dir, "m.dec")
	require.NoError(t, tryDecrypt(priv, dec, enc))

	got, err := os.ReadFile(dec) // #nosec G304 -- test temp path
	require.NoError(t, err)
	assert.Equal(t, "passphrase secret", string(got))

	info, err := os.Stat(dec)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o600), info.Mode().Perm(), "decrypted output must be 0600")
}

// A wrong passphrase must fail, not silently produce garbage.
func TestTryDecrypt_WrongPassphrase(t *testing.T) {
	dir := t.TempDir()
	priv, pub := makeEncryptedSSHKey(t, dir, "the right one")
	withPassphrase(t, "the wrong one")

	recips, err := parseRecipients([]string{pub})
	require.NoError(t, err)
	plain := filepath.Join(dir, "m.txt")
	require.NoError(t, os.WriteFile(plain, []byte("x"), 0o600))
	enc := filepath.Join(dir, "m.age")
	require.NoError(t, encryptFile(plain, enc, recips))

	assert.Error(t, tryDecrypt(priv, filepath.Join(dir, "m.dec"), enc))
}

// A passphrase-protected key must be diagnosed as such, not reported as a key
// mismatch. The prompt is deliberately not overridden here, so this covers the
// no-terminal branch: it must name the passphrase rather than blame the
// recipient, which sent users to re-encrypt or assume data loss.
func TestTryDecrypt_PassphraseProtectedKeyIsDiagnosed(t *testing.T) {
	dir := t.TempDir()
	priv, pub := makeEncryptedSSHKey(t, dir, "correct horse battery staple")

	recips, err := parseRecipients([]string{pub})
	require.NoError(t, err)
	plain := filepath.Join(dir, "m.txt")
	require.NoError(t, os.WriteFile(plain, []byte("secret"), 0o600))
	enc := filepath.Join(dir, "m.age")
	require.NoError(t, encryptFile(plain, enc, recips))

	err = tryDecrypt(priv, filepath.Join(dir, "m.dec"), enc)
	require.Error(t, err)
	assert.ErrorContains(t, err, "passphrase",
		"an encrypted key must be diagnosed as such, not reported as a key mismatch")
}

// The passphrase diagnosis must survive to the command's error, not be swallowed
// by tryAllKeys into a generic "none of the tried SSH keys matched".
func TestDecryptCmd_SurfacesPassphraseDiagnosis(t *testing.T) {
	dir := t.TempDir()
	priv, pub := makeEncryptedSSHKey(t, dir, "correct horse battery staple")

	recips, err := parseRecipients([]string{pub})
	require.NoError(t, err)
	plain := filepath.Join(dir, "m.txt")
	require.NoError(t, os.WriteFile(plain, []byte("secret"), 0o600))
	enc := filepath.Join(dir, "m.age")
	require.NoError(t, encryptFile(plain, enc, recips))

	c := Decrypt(&Config{SSHKeyPath: priv}, discardLogger())
	require.NoError(t, c.Flags().Set("input", enc))
	require.NoError(t, c.Flags().Set("output", filepath.Join(dir, "m.dec")))

	err = c.RunE(c, nil)
	require.Error(t, err)
	assert.ErrorContains(t, err, "passphrase", "the real cause must reach the user")
}

// A ~/.ssh that exists but holds no id_* key must not report a failed match: no
// key was tried, and the remedy is local, not "ask the sender to re-encrypt".
func TestDecryptCmd_NoKeysToTry(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	require.NoError(t, os.MkdirAll(filepath.Join(home, ".ssh"), 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(home, ".ssh", "known_hosts"), []byte("x"), 0o600))

	in := filepath.Join(home, "in.age")
	require.NoError(t, os.WriteFile(in, []byte("x"), 0o600))

	c := Decrypt(&Config{}, discardLogger())
	require.NoError(t, c.Flags().Set("input", in))
	require.NoError(t, c.Flags().Set("output", filepath.Join(home, "o.txt")))

	err := c.RunE(c, nil)
	require.Error(t, err)
	assert.ErrorContains(t, err, "no SSH private key to try")
	assert.ErrorContains(t, err, "--ssh-key", "the error must name the fix")
	assert.NotContains(t, err.Error(), "tried []", "must not claim keys were tried")
}

// Exercises the no-flag branch of Decrypt: ScanSSHPrivateKeys + tryAllKeys success.
func TestDecryptCmd_ScanPathRoundTrip(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	sshDir := filepath.Join(home, ".ssh")
	require.NoError(t, os.MkdirAll(sshDir, 0o700))
	_, pub := makeSSHKey(t, sshDir)

	plain := filepath.Join(home, "in.txt")
	require.NoError(t, os.WriteFile(plain, []byte("scan path secret"), 0o600))
	enc := filepath.Join(home, "out.age")

	recips, err := parseRecipients([]string{pub})
	require.NoError(t, err)
	require.NoError(t, encryptFile(plain, enc, recips))

	dec := filepath.Join(home, "dec.txt")
	c := Decrypt(&Config{}, discardLogger()) // no SSHKeyPath -> scans ~/.ssh
	require.NoError(t, c.Flags().Set("input", enc))
	require.NoError(t, c.Flags().Set("output", dec))
	require.NoError(t, c.RunE(c, nil))

	got, err := os.ReadFile(dec) // #nosec G304 -- test temp path
	require.NoError(t, err)
	assert.Equal(t, "scan path secret", string(got))
}
