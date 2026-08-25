package cmd

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// withGitHubKeysServer points githubKeysURL at a local test server for the duration
// of the test and restores it afterwards.
func withGitHubKeysServer(t *testing.T, handler http.HandlerFunc) {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	orig := githubKeysURL
	githubKeysURL = func(user string) string { return srv.URL + "/" + user + ".keys" }
	t.Cleanup(func() { githubKeysURL = orig })
}

func TestFetchGitHubKeys_NetworkOK(t *testing.T) {
	withGitHubKeysServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/good.keys" {
			_, _ = fmt.Fprintln(w, "ssh-ed25519 NETKEY")
			return
		}
		w.WriteHeader(http.StatusNotFound)
	})

	dir := t.TempDir()
	cfg := &Config{CacheDir: dir, CacheTTLMinutes: 60}
	keys := fetchGitHubKeys(cfg, "good", discardLogger())
	assert.Equal(t, []string{"ssh-ed25519 NETKEY"}, keys)

	// The successful response must have been written to the cache.
	cached, err := os.ReadFile(filepath.Join(dir, "good.keys")) // #nosec G304 -- test temp path
	require.NoError(t, err)
	assert.Contains(t, string(cached), "NETKEY")
}

func TestFetchGitHubKeys_NotFound(t *testing.T) {
	withGitHubKeysServer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	})
	assert.Nil(t, fetchGitHubKeys(&Config{}, "missing", discardLogger()))
}

func TestFetchGitHubKeys_ConnError(t *testing.T) {
	orig := githubKeysURL
	githubKeysURL = func(user string) string { return "http://127.0.0.1:0/" + user + ".keys" }
	t.Cleanup(func() { githubKeysURL = orig })
	assert.Nil(t, fetchGitHubKeys(&Config{}, "x", discardLogger()))
}

func TestFetchGitHubKeys_CacheDisabled(t *testing.T) {
	calls := 0
	withGitHubKeysServer(t, func(w http.ResponseWriter, _ *http.Request) {
		calls++
		_, _ = fmt.Fprintln(w, "ssh-ed25519 NOCACHE")
	})
	dir := t.TempDir()
	cfg := &Config{CacheDir: dir, CacheTTLMinutes: 0} // TTL 0 disables caching
	_ = fetchGitHubKeys(cfg, "user", discardLogger())
	_ = fetchGitHubKeys(cfg, "user", discardLogger())
	assert.Equal(t, 2, calls, "both calls should hit the network when caching is disabled")
	assert.NoFileExists(t, filepath.Join(dir, "user.keys"), "no cache file when TTL is 0")
}

func TestFetchGitHubKeys_BodyReadError(t *testing.T) {
	// Hijack the connection and promise more bytes than we send, then close, so the
	// client's io.ReadAll fails with an unexpected EOF.
	withGitHubKeysServer(t, func(w http.ResponseWriter, _ *http.Request) {
		conn, bufrw, err := http.NewResponseController(w).Hijack()
		if err != nil {
			return
		}
		_, _ = bufrw.WriteString("HTTP/1.1 200 OK\r\nContent-Length: 100\r\n\r\nshort")
		_ = bufrw.Flush()
		_ = conn.Close()
	})
	assert.Nil(t, fetchGitHubKeys(&Config{}, "user", discardLogger()))
}

func TestCollectRecipients(t *testing.T) {
	log := discardLogger()

	// Invalid GitHub username: an error, not a silent skip. Returning the config
	// recipients here produced a file the requested recipient could not open.
	cfg := &Config{DefaultRecipients: []string{"/a.pub"}, GitHubUser: "bad user!"}
	got, ghUser, err := collectRecipients(cfg, []string{"extra"}, "", log)
	require.Error(t, err)
	assert.ErrorContains(t, err, "invalid GitHub username")
	assert.Nil(t, got)
	assert.Equal(t, "bad user!", ghUser)

	// No GitHub user: only config + flag recipients, no error.
	got2, ghUser2, err := collectRecipients(&Config{DefaultRecipients: []string{"x"}}, nil, "", log)
	require.NoError(t, err)
	assert.Equal(t, []string{"x"}, got2)
	assert.Empty(t, ghUser2)

	// Valid GitHub user via flag: keys fetched and appended.
	withGitHubKeysServer(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = fmt.Fprintln(w, "ssh-ed25519 GH")
	})
	got3, ghUser3, err := collectRecipients(&Config{}, []string{"local"}, "octocat", log)
	require.NoError(t, err)
	assert.Equal(t, []string{"local", "ssh-ed25519 GH"}, got3)
	assert.Equal(t, "octocat", ghUser3)

	// A valid user whose keys cannot be fetched is also an error: encrypting
	// without the requested recipient is the failure this guards.
	withGitHubKeysServer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	})
	got4, _, err := collectRecipients(&Config{DefaultRecipients: []string{"mine"}}, nil, "octocat", log)
	require.Error(t, err)
	assert.ErrorContains(t, err, "no public keys found")
	assert.Nil(t, got4)
}

// TestEnsureWritableOutput covers the guard that keeps a derived output path from
// silently replacing an unrelated file of the same name.
func TestEnsureWritableOutput(t *testing.T) {
	dir := t.TempDir()
	missing := filepath.Join(dir, "nope.age")
	require.NoError(t, ensureWritableOutput(missing, false))

	existing := filepath.Join(dir, "there.age")
	require.NoError(t, os.WriteFile(existing, []byte("keep"), 0o600))
	err := ensureWritableOutput(existing, false)
	require.Error(t, err)
	assert.ErrorContains(t, err, "--force")
	assert.NoError(t, ensureWritableOutput(existing, true), "--force must permit replacement")
}

// TestEncryptCmd_RefusesExistingOutput drives the whole command: a pre-existing
// output must survive untouched, and --force must replace it.
func TestEncryptCmd_RefusesExistingOutput(t *testing.T) {
	dir := t.TempDir()
	in := filepath.Join(dir, "msg.txt")
	require.NoError(t, os.WriteFile(in, []byte("plaintext"), 0o600))
	out := in + ".age"
	require.NoError(t, os.WriteFile(out, []byte("OLD-CIPHERTEXT"), 0o600))

	_, pub := makeSSHKey(t, dir)
	run := func(extra ...string) error {
		c := Encrypt(&Config{DefaultRecipients: []string{pub}}, discardLogger())
		require.NoError(t, c.Flags().Set("input", in))
		for i := 0; i < len(extra); i += 2 {
			require.NoError(t, c.Flags().Set(extra[i], extra[i+1]))
		}
		return c.RunE(c, nil)
	}

	err := run()
	require.Error(t, err)
	assert.ErrorContains(t, err, "already exists")
	body, readErr := os.ReadFile(out) // #nosec G304 -- test temp path
	require.NoError(t, readErr)
	assert.Equal(t, "OLD-CIPHERTEXT", string(body), "refused run must not touch the output")

	require.NoError(t, run("force", "true"))
	body, readErr = os.ReadFile(out) // #nosec G304 -- test temp path
	require.NoError(t, readErr)
	assert.NotEqual(t, "OLD-CIPHERTEXT", string(body), "--force must replace the output")
}

// TestEncryptCmd_GitHubUserPositionalWithInputFlag pins the arg-slot shift:
// --input moves the github-user positional from args[1] to args[0], and reading
// a fixed index there discarded the requested recipient and exited 0 -- producing
// a file the intended recipient could not open.
func TestEncryptCmd_GitHubUserPositionalWithInputFlag(t *testing.T) {
	withGitHubKeysServer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	})
	dir := t.TempDir()
	in := filepath.Join(dir, "msg.txt")
	require.NoError(t, os.WriteFile(in, []byte("plaintext"), 0o600))
	_, pub := makeSSHKey(t, dir)

	c := Encrypt(&Config{DefaultRecipients: []string{pub}}, discardLogger())
	require.NoError(t, c.Flags().Set("input", in))
	err := c.RunE(c, []string{"octocat"})

	require.Error(t, err, "the positional github-user must not be discarded")
	assert.ErrorContains(t, err, "no public keys found")
	assert.NoFileExists(t, in+".age", "no output without the requested recipient")
}

// An argument the command cannot place must stop the run rather than be dropped.
func TestEncryptCmd_RejectsUnplaceableArg(t *testing.T) {
	dir := t.TempDir()
	in := filepath.Join(dir, "msg.txt")
	require.NoError(t, os.WriteFile(in, []byte("plaintext"), 0o600))
	_, pub := makeSSHKey(t, dir)

	c := Encrypt(&Config{DefaultRecipients: []string{pub}}, discardLogger())
	require.NoError(t, c.Flags().Set("input", in))
	require.NoError(t, c.Flags().Set("github-user", "octocat"))
	assert.ErrorContains(t, c.RunE(c, []string{"stray"}), "unexpected argument")
}

func TestEncryptCmd_Validation(t *testing.T) {
	log := discardLogger()
	run := func(flags map[string]string) error {
		c := Encrypt(&Config{}, log)
		for k, v := range flags {
			require.NoError(t, c.Flags().Set(k, v))
		}
		return c.RunE(c, nil)
	}

	assert.ErrorContains(t, run(nil), "input file is required")
	assert.ErrorContains(t,
		run(map[string]string{"input": "/no/such/file", "output": "o.txt"}),
		"input file does not exist")

	in := filepath.Join(t.TempDir(), "in.txt")
	require.NoError(t, os.WriteFile(in, []byte("data"), 0o600))
	assert.ErrorContains(t,
		run(map[string]string{"input": in, "output": filepath.Join(t.TempDir(), "o.txt")}),
		"at least one recipient is required")
}

func TestEncryptFile_MissingInput(t *testing.T) {
	// A missing input file must error before any encryption is attempted.
	err := encryptFile("/no/such/input", filepath.Join(t.TempDir(), "o.age"), nil)
	assert.ErrorContains(t, err, "opening input")
}

// A failed encrypt (zero recipients) must not clobber a pre-existing output file
// or leave a temp file behind.
func TestEncryptFile_FailureLeavesPreexistingIntact(t *testing.T) {
	dir := t.TempDir()
	in := filepath.Join(dir, "in.txt")
	require.NoError(t, os.WriteFile(in, []byte("data"), 0o600))
	out := filepath.Join(dir, "out.age")
	require.NoError(t, os.WriteFile(out, []byte("PREEXISTING"), 0o600))

	assert.Error(t, encryptFile(in, out, nil), "zero recipients must fail")

	got, err := os.ReadFile(out) // #nosec G304 -- test temp path
	require.NoError(t, err)
	assert.Equal(t, "PREEXISTING", string(got), "pre-existing file must survive a failed encrypt")

	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	for _, e := range entries {
		assert.NotContains(t, e.Name(), ".a-encrypt", "temp file must be cleaned up")
	}
}
