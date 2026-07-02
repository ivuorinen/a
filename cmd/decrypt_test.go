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
	tried, ok := tryAllKeys(nil, "i", "o.txt", discardLogger())
	assert.False(t, ok)
	assert.Empty(t, tried)

	tried, ok = tryAllKeys([]string{"/no/such/id_rsa"}, "i", "o.txt", discardLogger())
	assert.False(t, ok)
	assert.Equal(t, []string{"/no/such/id_rsa"}, tried)
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

// makeSSHKey writes a fresh ed25519 keypair named id_ed25519 into dir.
func makeSSHKey(t *testing.T, dir string) (priv, pub string) {
	t.Helper()
	priv = filepath.Join(dir, "id_ed25519")
	// #nosec G204 -- test helper; dir is a test temp dir
	out, err := exec.Command("ssh-keygen", "-t", "ed25519", "-N", "", "-f", priv).CombinedOutput()
	require.NoError(t, err, string(out))
	return priv, priv + ".pub"
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
