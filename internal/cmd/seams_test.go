package cmd

import (
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Tests for the branches that only the production seams make reachable:
// yamlMarshal, createTemp/tempFile, userConfigDir and the term wrappers. Each
// guards a real failure — a full disk, an I/O error mid-write, a macOS path, a
// terminal read — that cannot be provoked against a real file, filesystem or
// tty inside `go test`.

// errWriteFile is a tempFile whose Write always fails. Name() returns a real
// path so the caller's cleanup still has something to remove.
type errWriteFile struct {
	name     string
	closeErr error
}

func (*errWriteFile) Write([]byte) (int, error) { return 0, errors.New("simulated write failure") }
func (f *errWriteFile) Close() error            { return f.closeErr }
func (f *errWriteFile) Name() string            { return f.name }

// okWriteCloseErrFile accepts every write and fails only on Close, which is the
// distinct branch: the data reached the kernel but the file did not close
// cleanly, so the rename must not happen.
type okWriteCloseErrFile struct{ name string }

func (*okWriteCloseErrFile) Write(p []byte) (int, error) { return len(p), nil }
func (*okWriteCloseErrFile) Close() error                { return errors.New("simulated close failure") }
func (f *okWriteCloseErrFile) Name() string              { return f.name }

// countingFile counts writes and fails from the failOn-th onward (1-based; zero
// never fails). Counting is what lets a test target one specific write in a
// sequence it does not control, such as the age writer's final chunk flush.
type countingFile struct {
	name   string
	writes int
	failOn int
}

func (f *countingFile) Write(p []byte) (int, error) {
	f.writes++
	if f.failOn != 0 && f.writes >= f.failOn {
		return 0, errors.New("simulated write failure")
	}
	return len(p), nil
}
func (*countingFile) Close() error   { return nil }
func (f *countingFile) Name() string { return f.name }

// withCreateTemp swaps the createTemp seam for the duration of a test.
func withCreateTemp(t *testing.T, fn func(dir, pattern string) (tempFile, error)) {
	t.Helper()
	orig := createTemp
	createTemp = fn
	t.Cleanup(func() { createTemp = orig })
}

// withFailingMarshal makes yamlMarshal fail, reaching the marshal-error branches
// that are dead for the real Config.
func withFailingMarshal(t *testing.T) {
	t.Helper()
	orig := yamlMarshal
	yamlMarshal = func(any) ([]byte, error) { return nil, errors.New("simulated marshal failure") }
	t.Cleanup(func() { yamlMarshal = orig })
}

// ---------------------------------------------------------------------------
// yamlMarshal
// ---------------------------------------------------------------------------

func TestSaveConfig_MarshalError(t *testing.T) {
	withFailingMarshal(t)

	dir := t.TempDir()
	err := SaveConfig(filepath.Join(dir, "config.yaml"), &Config{})
	assert.ErrorContains(t, err, "simulated marshal failure")

	entries, readErr := os.ReadDir(dir)
	require.NoError(t, readErr)
	assert.Empty(t, entries, "a marshal failure must happen before any temp file exists")
}

func TestFormatConfig_MarshalError(t *testing.T) {
	withFailingMarshal(t)

	// formatConfig returns the error as display text rather than failing the
	// command: `config show` still prints something a user can read.
	out := formatConfig(&Config{})
	assert.Contains(t, out, "error rendering config")
	assert.Contains(t, out, "simulated marshal failure")
}

// ---------------------------------------------------------------------------
// createTemp / tempFile
// ---------------------------------------------------------------------------

func TestSaveConfig_TempFileWriteAndCloseErrors(t *testing.T) {
	t.Run("write fails", func(t *testing.T) {
		dir := t.TempDir()
		tmpName := filepath.Join(dir, ".config-stub.yaml")
		require.NoError(t, os.WriteFile(tmpName, nil, 0o600))
		withCreateTemp(t, func(string, string) (tempFile, error) {
			return &errWriteFile{name: tmpName}, nil
		})

		target := filepath.Join(dir, "config.yaml")
		err := SaveConfig(target, &Config{GitHubUser: "x"})
		assert.ErrorContains(t, err, "simulated write failure")
		assert.NoFileExists(t, target, "a failed write must not produce a config")
		assert.NoFileExists(t, tmpName, "the temp file must be removed on failure")
	})

	t.Run("close fails", func(t *testing.T) {
		dir := t.TempDir()
		tmpName := filepath.Join(dir, ".config-stub.yaml")
		require.NoError(t, os.WriteFile(tmpName, nil, 0o600))
		withCreateTemp(t, func(string, string) (tempFile, error) {
			return &okWriteCloseErrFile{name: tmpName}, nil
		})

		target := filepath.Join(dir, "config.yaml")
		err := SaveConfig(target, &Config{GitHubUser: "x"})
		assert.ErrorContains(t, err, "simulated close failure")
		assert.NoFileExists(t, target, "a file that did not close cleanly must not be renamed into place")
		assert.NoFileExists(t, tmpName, "the temp file must be removed on failure")
	})
}

func TestEncryptFile_TempFileWriteAndCloseErrors(t *testing.T) {
	dir := t.TempDir()
	in := filepath.Join(dir, "in.txt")
	require.NoError(t, os.WriteFile(in, []byte("data"), 0o600))
	_, pub := makeSSHKey(t, dir)
	recips, err := parseRecipients([]string{pub})
	require.NoError(t, err)

	t.Run("age writer cannot flush its final chunk", func(t *testing.T) {
		// age writes its header during age.Encrypt and buffers the payload into
		// 64 KiB chunks, so for a small input the *last* write is the flush
		// w.Close performs. Failing any earlier write lands in "initializing
		// encryption" instead, so the target write has to be the final one.
		//
		// Calibrated rather than hardcoded: the header's write count is an age
		// implementation detail that a version bump can change, and a stale
		// constant here would silently start testing the wrong branch.
		cal := &countingFile{name: filepath.Join(t.TempDir(), "stub")}
		withCreateTemp(t, func(string, string) (tempFile, error) { return cal, nil })
		// The rename at the end fails (the stub's Name is not a real file); only
		// the write count matters here.
		_ = encryptFile(in, filepath.Join(t.TempDir(), "cal.age"), recips)
		require.Greater(t, cal.writes, 1, "a successful encrypt must write a header and a payload flush")

		out := filepath.Join(t.TempDir(), "out.age")
		withCreateTemp(t, func(string, string) (tempFile, error) {
			return &countingFile{name: filepath.Join(t.TempDir(), "stub"), failOn: cal.writes}, nil
		})

		assert.ErrorContains(t, encryptFile(in, out, recips), "finalizing encryption")
		assert.NoFileExists(t, out)
	})

	t.Run("temp file cannot be closed", func(t *testing.T) {
		out := filepath.Join(t.TempDir(), "out.age")
		withCreateTemp(t, func(string, string) (tempFile, error) {
			return &okWriteCloseErrFile{name: filepath.Join(t.TempDir(), "stub")}, nil
		})

		assert.ErrorContains(t, encryptFile(in, out, recips), "closing temp output")
		assert.NoFileExists(t, out)
	})
}

func TestTryDecrypt_TempFileCloseError(t *testing.T) {
	dir := t.TempDir()
	priv, pub := makeSSHKey(t, dir)
	recips, err := parseRecipients([]string{pub})
	require.NoError(t, err)
	plain := filepath.Join(dir, "m.txt")
	require.NoError(t, os.WriteFile(plain, []byte("secret"), 0o600))
	enc := filepath.Join(dir, "m.age")
	require.NoError(t, encryptFile(plain, enc, recips))

	// Decryption succeeds and the plaintext is written; only the close fails.
	// The rename must not happen, so no plaintext reaches the target.
	out := filepath.Join(t.TempDir(), "out.txt")
	withCreateTemp(t, func(string, string) (tempFile, error) {
		return &okWriteCloseErrFile{name: filepath.Join(t.TempDir(), "stub")}, nil
	})

	assert.ErrorContains(t, tryDecrypt(priv, out, enc), "closing temp output")
	assert.NoFileExists(t, out, "plaintext must not reach the target when the temp file did not close")
}

// ---------------------------------------------------------------------------
// userConfigDir
// ---------------------------------------------------------------------------

// The darwin branch deliberately ignores "~/Library/Application Support" in
// favor of ~/.config. Parameterizing on goos is what makes it checkable from a
// linux runner.
func TestUserConfigDir(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	t.Run("darwin uses ~/.config", func(t *testing.T) {
		got, err := userConfigDir("darwin")
		require.NoError(t, err)
		assert.Equal(t, filepath.Join(home, ".config"), got)
	})

	t.Run("darwin surfaces an unresolvable home", func(t *testing.T) {
		t.Setenv("HOME", "")
		_, err := userConfigDir("darwin")
		assert.ErrorContains(t, err, "resolving home directory for the config path")
	})

	t.Run("other platforms defer to os.UserConfigDir", func(t *testing.T) {
		xdg := t.TempDir()
		t.Setenv("XDG_CONFIG_HOME", xdg)
		got, err := userConfigDir("linux")
		require.NoError(t, err)
		assert.Equal(t, xdg, got)
	})
}

// ---------------------------------------------------------------------------
// term wrappers
// ---------------------------------------------------------------------------

// withTerm makes the process look like it has a tty and supplies what the user
// would have typed. os.Stderr is redirected for the duration because
// passphrasePrompt writes its prompt there, which would otherwise appear in the
// middle of the test run's output.
func withTerm(t *testing.T, isTerminal bool, pass []byte, readErr error) {
	t.Helper()
	origIs, origRead, origErr := termIsTerminal, termReadPassword, os.Stderr
	f, err := os.CreateTemp(t.TempDir(), "stderr")
	require.NoError(t, err)
	termIsTerminal = func(int) bool { return isTerminal }
	termReadPassword = func(int) ([]byte, error) { return pass, readErr }
	os.Stderr = f
	t.Cleanup(func() {
		termIsTerminal, termReadPassword, os.Stderr = origIs, origRead, origErr
		assert.NoError(t, f.Close())
	})
}

func TestPassphrasePrompt(t *testing.T) {
	t.Run("no terminal", func(t *testing.T) {
		withTerm(t, false, nil, nil)
		_, err := passphrasePrompt("/k/id_rsa")
		assert.ErrorContains(t, err, "stdin is not a terminal")
	})

	t.Run("reads the passphrase", func(t *testing.T) {
		withTerm(t, true, []byte("correct horse"), nil)
		got, err := passphrasePrompt("/k/id_rsa")
		require.NoError(t, err)
		assert.Equal(t, []byte("correct horse"), got)
	})

	t.Run("read failure names the key", func(t *testing.T) {
		withTerm(t, true, nil, errors.New("simulated read failure"))
		_, err := passphrasePrompt("/k/id_rsa")
		require.Error(t, err)
		assert.ErrorContains(t, err, "could not read passphrase")
		assert.ErrorContains(t, err, "/k/id_rsa", "the user must be told which key failed")
		assert.ErrorContains(t, err, "simulated read failure")
	})
}

// ---------------------------------------------------------------------------
// response-body close (no production seam needed: keysHTTPClient is already a var)
// ---------------------------------------------------------------------------

// errCloseBody serves a body that reads fine and fails to close.
type errCloseBody struct{ io.Reader }

func (errCloseBody) Close() error { return errors.New("simulated body close failure") }

type stubTransport struct{ body string }

func (s stubTransport) RoundTrip(*http.Request) (*http.Response, error) {
	return &http.Response{
		StatusCode: http.StatusOK,
		Body:       errCloseBody{strings.NewReader(s.body)},
		Header:     make(http.Header),
	}, nil
}

// A body that fails to close is logged and otherwise ignored: the keys were
// already read, so failing the encryption over it would be a regression.
func TestFetchGitHubKeys_BodyCloseErrorIsNonFatal(t *testing.T) {
	orig := keysHTTPClient
	keysHTTPClient = &http.Client{Transport: stubTransport{body: "ssh-ed25519 CLOSEFAIL\n"}}
	t.Cleanup(func() { keysHTTPClient = orig })

	keys := fetchGitHubKeys(&Config{}, "octocat", discardLogger())
	assert.Equal(t, []string{"ssh-ed25519 CLOSEFAIL"}, keys,
		"a failed body close must not lose keys that were already read")
}
