package main

import (
	"bytes"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"runtime/debug"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"

	"github.com/ivuorinen/a/internal/cmd"
)

func discardLogger() *slog.Logger {
	return slog.New(slog.NewJSONHandler(io.Discard, nil))
}

// TestBuildVersionPrefersLdflagsStamp guards the -X override. cmd/link only
// patches a variable that is uninitialized or constant, so initializing
// `version` with a function call turned .goreleaser.yml's -X into a silent
// no-op and left releases reporting a VCS pseudo-version. The failure is
// invisible in a plain `go build`, which VCS stamping rescues, so it has to be
// pinned here.
func TestBuildVersionPrefersLdflagsStamp(t *testing.T) {
	old := version
	t.Cleanup(func() { version = old })

	version = "1.2.3-stamped"
	assert.Equal(t, "1.2.3-stamped", buildVersion(), "an ldflags stamp must win")

	version = ""
	// "(devel)" exactly, not merely non-empty: every return path of
	// buildVersion is non-empty by construction, so NotEmpty asserts the type
	// rather than the behavior. Under `go test` ReadBuildInfo reports
	// Main.Version as "(devel)", which the guard rejects, so the constant is
	// what must come back.
	assert.Equal(t, "(devel)", buildVersion(),
		"with no stamp and no release build info, the honest answer is (devel)")
}

// withBuildInfo replaces the readBuildInfo seam. Inside `go test` the real one
// always reports Main.Version as "(devel)", so the release-version branch and
// the info-unavailable branch are otherwise unreachable.
func withBuildInfo(t *testing.T, ok bool, mainVersion string) {
	t.Helper()
	orig := readBuildInfo
	readBuildInfo = func() (*debug.BuildInfo, bool) {
		if !ok {
			return nil, false
		}
		return &debug.BuildInfo{Main: debug.Module{Version: mainVersion}}, true
	}
	t.Cleanup(func() { readBuildInfo = orig })
}

// The three conditions guarding the build-info branch, each exercised both ways.
// A stale or wrong version here sends bug reports to the wrong tag, which is the
// defect that motivated the guard.
func TestBuildVersionFromBuildInfo(t *testing.T) {
	old := version
	t.Cleanup(func() { version = old })
	version = ""

	t.Run("release version wins", func(t *testing.T) {
		withBuildInfo(t, true, "v1.4.2")
		assert.Equal(t, "v1.4.2", buildVersion(), "a real module version must be reported as-is")
	})

	t.Run("(devel) is rejected in favor of the constant", func(t *testing.T) {
		withBuildInfo(t, true, "(devel)")
		assert.Equal(t, "(devel)", buildVersion())
	})

	t.Run("empty version falls through", func(t *testing.T) {
		withBuildInfo(t, true, "")
		assert.Equal(t, "(devel)", buildVersion(),
			"an empty Main.Version must not be reported as the version")
	})

	t.Run("no build info at all", func(t *testing.T) {
		withBuildInfo(t, false, "")
		assert.Equal(t, "(devel)", buildVersion())
	})
}

// run() reports the process exit status. Both outcomes matter: a failing command
// must not exit 0, and a succeeding one must not exit nonzero.
func TestRun(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "cfg"))
	t.Setenv("XDG_CACHE_HOME", filepath.Join(home, "cache"))
	t.Setenv("XDG_STATE_HOME", filepath.Join(home, "state"))

	// cobra reads os.Args, which is process-global: restore it or every test
	// that runs afterwards inherits this one's argv.
	origArgs := os.Args
	t.Cleanup(func() { os.Args = origArgs })

	t.Run("success", func(t *testing.T) {
		os.Args = []string{"a", "config", "show"}
		assert.Equal(t, 0, run())
	})

	t.Run("failure", func(t *testing.T) {
		os.Args = []string{"a", "completion", "powershell"}
		assert.Equal(t, 1, run(), "a failing command must exit nonzero")
	})
}

// PersistentPreRunE's two error returns, in-process. Previously these were
// reachable only through the built binary.
func TestRootCmdPreRunErrors(t *testing.T) {
	t.Run("paths cannot be initialized", func(t *testing.T) {
		t.Setenv("HOME", t.TempDir())
		blocker := filepath.Join(t.TempDir(), "notadir")
		require.NoError(t, os.WriteFile(blocker, []byte("x"), 0o600))
		t.Setenv("XDG_CONFIG_HOME", filepath.Join(blocker, "child"))

		c := newRootCmd()
		c.SetArgs([]string{"config", "show"})
		c.SetOut(&bytes.Buffer{})
		c.SetErr(&bytes.Buffer{})
		assert.ErrorContains(t, c.Execute(), "error initializing paths")
	})

	t.Run("config cannot be loaded", func(t *testing.T) {
		home := t.TempDir()
		t.Setenv("HOME", home)
		cfgHome := filepath.Join(home, "cfg")
		require.NoError(t, os.MkdirAll(filepath.Join(cfgHome, "a"), 0o700))
		// #nosec G306 -- group/other-accessible on purpose so LoadConfig rejects it
		require.NoError(t, os.WriteFile(
			filepath.Join(cfgHome, "a", "config.yaml"), []byte("github_user: x\n"), 0o644))
		t.Setenv("XDG_CONFIG_HOME", cfgHome)
		t.Setenv("XDG_CACHE_HOME", filepath.Join(home, "cache"))

		c := newRootCmd()
		c.SetArgs([]string{"config", "show"})
		c.SetOut(&bytes.Buffer{})
		c.SetErr(&bytes.Buffer{})
		assert.ErrorContains(t, c.Execute(), "error loading config")
	})
}

func TestInitConfigPaths(t *testing.T) {
	// Isolate from the real home directory.
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "cfg"))
	t.Setenv("XDG_CACHE_HOME", filepath.Join(home, "cache"))

	paths, err := cmd.InitConfigPaths()
	require.NoError(t, err, "initializing config paths should not produce an error")

	assert.DirExists(t, filepath.Dir(paths.ConfigFile), "config directory should exist")
	assert.FileExists(t, paths.ConfigFile, "config file path should exist")
	assert.DirExists(t, paths.CacheDir, "cache directory should exist")
}

// TestConfigWrappers exercises the thin main-package wrappers that bridge to the
// cmd package: initConfigPaths, loadConfig (in-place mutation), saveConfig, and
// setupLogging.
func TestConfigWrappers(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "cfg"))
	t.Setenv("XDG_CACHE_HOME", filepath.Join(home, "cache"))
	t.Setenv("XDG_STATE_HOME", filepath.Join(home, "state"))

	require.NoError(t, initConfigPaths())
	assert.NotEmpty(t, cfgFile)
	assert.NotEmpty(t, cacheDir)

	require.NoError(t, loadConfig())
	assert.Equal(t, cacheDir, cfg.CacheDir, "loadConfig should populate CacheDir on the shared cfg")
	assert.NotEmpty(t, cfg.LogFilePath, "default log path should be set")

	cfg.GitHubUser = "wrapped"
	require.NoError(t, saveConfig(cfg))
	reloaded, err := cmd.LoadConfig(cfgFile)
	require.NoError(t, err)
	assert.Equal(t, "wrapped", reloaded.GitHubUser)

	setupLogging(true) // verbose -> Debug admitted
	log.Debug("debug probe")
	body, err := os.ReadFile(cfg.LogFilePath) // #nosec G304 -- test temp path
	require.NoError(t, err, "setupLogging must open the configured log file")
	assert.Contains(t, string(body), "debug probe", "verbose must admit Debug records")

	setupLogging(false) // non-verbose -> Debug filtered out
	log.Debug("must not appear")
	body, err = os.ReadFile(cfg.LogFilePath) // #nosec G304 -- test temp path
	require.NoError(t, err)
	assert.NotContains(t, string(body), "must not appear", "non-verbose must filter Debug")
}

func TestLoadAndSaveConfig(t *testing.T) {
	tempDir := t.TempDir()
	cfgFile := filepath.Join(tempDir, "config.yaml")

	cfg := &cmd.Config{
		SSHKeyPath:        "/tmp/id_rsa",
		GitHubUser:        "testuser",
		DefaultRecipients: []string{"/tmp/key.pub"},
		CacheTTLMinutes:   60,
		LogFilePath:       "/tmp/test.log",
	}

	require.NoError(t, cmd.SaveConfig(cfgFile, cfg), "saving config should not produce an error")

	loadedCfg, err := cmd.LoadConfig(cfgFile)
	require.NoError(t, err, "loading config should not produce an error")
	assert.Equal(t, cfg, loadedCfg, "loaded config should match saved config")
}

func TestDefaultLogFilePath(t *testing.T) {
	// Isolate the state dir: XDG_STATE_HOME outranks HOME, so an inherited value
	// would send the default log path into the developer's real home.
	t.Setenv("XDG_STATE_HOME", filepath.Join(t.TempDir(), "state"))

	tempDir := t.TempDir()
	cfgFile := filepath.Join(tempDir, "config.yaml")

	cfg := &cmd.Config{
		SSHKeyPath:        "/tmp/id_rsa",
		GitHubUser:        "testuser",
		DefaultRecipients: []string{"/tmp/key.pub"},
		CacheTTLMinutes:   60,
	}

	data, err := yaml.Marshal(cfg)
	require.NoError(t, err, "marshaling config should not produce an error")
	require.NoError(t, os.WriteFile(cfgFile, data, 0o600))

	loadedCfg, err := cmd.LoadConfig(cfgFile)
	require.NoError(t, err, "loading config should not produce an error")
	assert.NotEmpty(t, loadedCfg.LogFilePath, "default log file path should be set")
}

func TestCmdConfig(t *testing.T) {
	cfg := &cmd.Config{}
	cmdObj := cmd.ConfigCmd(cfg, func(_ *cmd.Config) error { return nil })
	require.NotNil(t, cmdObj, "ConfigCmd should return a non-nil cobra command")
	assert.Contains(t, cmdObj.Aliases, "c", "config should be aliased to c")

	names := make([]string, 0, 3)
	for _, sub := range cmdObj.Commands() {
		names = append(names, sub.Name())
	}
	assert.ElementsMatch(t, []string{"set", "rem", "show"}, names, "config subcommands")
}

// requireBinary skips when bin is absent locally, but fails when CI is set.
//
// A build-environment dependency missing locally is not a product failure, so
// the skip is right there. In CI it is the opposite: Go reports a skipped test
// as `ok`, and the tests behind this guard are the ones that generate real SSH
// keys plus TestCLIIntegration, the only coverage of main(). A runner image
// that quietly stopped shipping ssh-keygen would retire all of them behind a
// green run, which is the failure this branch exists to make loud.
func requireBinary(t *testing.T, bin string) {
	t.Helper()
	if _, err := exec.LookPath(bin); err != nil {
		if os.Getenv("CI") != "" {
			t.Fatalf("%s missing on a CI runner that must provide it: %v", bin, err)
		}
		t.Skipf("%s not available", bin)
	}
}

// Helper to generate a temporary SSH keypair for testing.
//
// Each keypair gets its own t.TempDir() so several can be generated within one
// test without ssh-keygen refusing to overwrite an existing id_rsa. The file is
// still named id_rsa, which decryption requires. t.TempDir() rather than
// os.MkdirTemp: it registers its own cleanup and needs no parent directory
// threaded through the callers.
//
// Callers must guard with requireBinary: ssh-keygen is a build-environment
// dependency, and its absence must skip rather than report as a product failure.
func generateSSHKeyPair(t *testing.T) (privKey, pubKey string, err error) {
	t.Helper()
	requireBinary(t, "ssh-keygen")
	privKey = filepath.Join(t.TempDir(), "id_rsa")
	pubKey = privKey + ".pub"
	// #nosec G204 -- test helper; all args are literals except privKey, which is a path under a test temp dir
	cmd := exec.Command("ssh-keygen", "-t", "rsa", "-b", "2048", "-N", "", "-f", privKey)
	if err := cmd.Run(); err != nil {
		return "", "", err
	}
	return privKey, pubKey, nil
}

func TestEncryptDecrypt_Success(t *testing.T) {
	tempDir := t.TempDir()
	plaintext := []byte("This is a secret message for encryption test.")

	// Generate SSH keypair
	privKey, pubKey, err := generateSSHKeyPair(t)
	require.NoError(t, err, "ssh-keygen should succeed")

	// Write plaintext file
	inputFile := filepath.Join(tempDir, "input.txt")
	require.NoError(t, os.WriteFile(inputFile, plaintext, 0o600))

	// Prepare config
	cfg := &cmd.Config{
		DefaultRecipients: []string{pubKey},
		LogFilePath:       filepath.Join(tempDir, "cli.log"),
	}
	log := discardLogger()

	// Encrypt
	encryptedFile := filepath.Join(tempDir, "encrypted.txt")
	encryptCmd := cmd.Encrypt(cfg, log)
	require.NoError(t, encryptCmd.Flags().Set("input", inputFile))
	require.NoError(t, encryptCmd.Flags().Set("output", encryptedFile))
	require.NoError(t, encryptCmd.RunE(encryptCmd, []string{}))
	assert.FileExists(t, encryptedFile, "encrypted file should exist")

	// Decrypt
	decryptCfg := &cmd.Config{SSHKeyPath: privKey, LogFilePath: cfg.LogFilePath}
	decryptedFile := filepath.Join(tempDir, "decrypted.txt")
	decryptCmd := cmd.Decrypt(decryptCfg, log)
	require.NoError(t, decryptCmd.Flags().Set("input", encryptedFile))
	require.NoError(t, decryptCmd.Flags().Set("output", decryptedFile))
	require.NoError(t, decryptCmd.RunE(decryptCmd, []string{}))

	info, err := os.Stat(decryptedFile)
	require.NoError(t, err, "decrypted file should exist")
	assert.Equal(t, os.FileMode(0o600), info.Mode().Perm(), "decrypted file must be 0600")

	// #nosec G304 -- decryptedFile is generated in tempDir and not user-controlled
	decrypted, err := os.ReadFile(decryptedFile)
	require.NoError(t, err)
	assert.Equal(t, plaintext, decrypted, "decrypted output should match original plaintext")
}

func TestEncryptDecrypt_WrongKey(t *testing.T) {
	tempDir := t.TempDir()
	plaintext := []byte("Secret message for wrong key test.")

	// Generate two SSH keypairs
	_, pubKey1, err := generateSSHKeyPair(t)
	require.NoError(t, err)
	privKey2, _, err := generateSSHKeyPair(t)
	require.NoError(t, err)

	// Write plaintext file
	inputFile := filepath.Join(tempDir, "input.txt")
	require.NoError(t, os.WriteFile(inputFile, plaintext, 0o600))

	// Encrypt with pubKey1
	cfg := &cmd.Config{
		DefaultRecipients: []string{pubKey1},
		LogFilePath:       filepath.Join(tempDir, "cli.log"),
	}
	log := discardLogger()
	encryptedFile := filepath.Join(tempDir, "encrypted.txt")
	encryptCmd := cmd.Encrypt(cfg, log)
	require.NoError(t, encryptCmd.Flags().Set("input", inputFile))
	require.NoError(t, encryptCmd.Flags().Set("output", encryptedFile))
	require.NoError(t, encryptCmd.RunE(encryptCmd, []string{}))
	assert.FileExists(t, encryptedFile, "encrypted file should exist")

	// Try to decrypt with privKey2 (should fail)
	decryptCfg := &cmd.Config{SSHKeyPath: privKey2, LogFilePath: cfg.LogFilePath}
	decryptedFile := filepath.Join(tempDir, "decrypted_wrongkey.txt")
	decryptCmd := cmd.Decrypt(decryptCfg, log)
	require.NoError(t, decryptCmd.Flags().Set("input", encryptedFile))
	require.NoError(t, decryptCmd.Flags().Set("output", decryptedFile))
	assert.Error(t, decryptCmd.RunE(decryptCmd, []string{}), "decryption should fail with wrong key")
}

func TestEncryptDecrypt_MissingRecipient(t *testing.T) {
	tempDir := t.TempDir()
	plaintext := []byte("Secret message for missing recipient test.")

	// Write plaintext file
	inputFile := filepath.Join(tempDir, "input.txt")
	require.NoError(t, os.WriteFile(inputFile, plaintext, 0o600))

	// Encrypt with no recipient
	cfg := &cmd.Config{
		DefaultRecipients: []string{},
		LogFilePath:       filepath.Join(tempDir, "cli.log"),
	}
	log := discardLogger()
	encryptedFile := filepath.Join(tempDir, "encrypted.txt")
	encryptCmd := cmd.Encrypt(cfg, log)
	require.NoError(t, encryptCmd.Flags().Set("input", inputFile))
	require.NoError(t, encryptCmd.Flags().Set("output", encryptedFile))
	assert.Error(t, encryptCmd.RunE(encryptCmd, []string{}), "encryption should fail with no recipient")
}

func TestSetupLoggingFallback(t *testing.T) {
	// Pointing the log path at a directory makes OpenFile fail; logging must
	// degrade to stderr rather than error, so a bad log_file_path cannot brick
	// every command (including the `config` command needed to fix it).
	dir := t.TempDir()
	cfg = &cmd.Config{LogFilePath: dir}

	setupLogging(false)

	// "Must not panic" was the old criterion, and an empty body satisfied it.
	// The fallback logger has to be usable, and nothing may have been created
	// at the unusable path.
	log.Info("probe")
	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	assert.Empty(t, entries, "a directory log path must not gain a file")
}

func TestRollLog(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "cli.log")

	// Under the cap: left alone, so a roll never discards a live log.
	require.NoError(t, os.WriteFile(path, []byte("small"), 0o600))
	rollLog(path)
	assert.NoFileExists(t, path+".1", "a log under the cap must not roll")

	// Over the cap: moved aside, and the previous generation is kept rather
	// than deleted, so the run that triggered the roll stays recoverable.
	require.NoError(t, os.WriteFile(path, make([]byte, maxLogBytes+1), 0o600))
	rollLog(path)
	assert.NoFileExists(t, path, "the oversized log must be moved aside")
	assert.FileExists(t, path+".1", "one previous generation is kept")

	// Absent path and empty path are both no-ops, not panics: rollLog runs on
	// every command, ahead of the OpenFile that creates the file.
	rollLog(filepath.Join(dir, "never-existed.log"))
	rollLog("")
}

func TestInitConfigPathsWrapperError(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	blocker := filepath.Join(t.TempDir(), "notadir")
	require.NoError(t, os.WriteFile(blocker, []byte("x"), 0o600))
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(blocker, "child"))
	assert.ErrorContains(t, initConfigPaths(), "not a directory")
}

func TestLoadConfigWrapperError(t *testing.T) {
	old := cfgFile
	t.Cleanup(func() { cfgFile = old })
	bad := filepath.Join(t.TempDir(), "config.yaml")
	// #nosec G306 -- group/other-accessible on purpose so LoadConfig rejects it
	require.NoError(t, os.WriteFile(bad, []byte("x"), 0o644))
	cfgFile = bad
	assert.Error(t, loadConfig())
}

// TestCLIIntegration builds the real binary and drives a full lifecycle through it,
// covering main(), PersistentPreRunE, and the command wiring end to end.
//
// main() runs in a subprocess, so a plain `go test -coverprofile` cannot see any
// of it and reports the function at 0% however thoroughly this test exercises
// it. Setting A_INTEGRATION_GOCOVERDIR builds the binary with `-cover` and
// points it at that directory, so `just coverage` can merge the subprocess's
// counters into the unit-test profile. Unset (the default, and every plain
// `go test` run), the build is ordinary and nothing here changes.
func TestCLIIntegration(t *testing.T) {
	for _, bin := range []string{"go", "ssh-keygen"} {
		requireBinary(t, bin)
	}

	covDir := os.Getenv("A_INTEGRATION_GOCOVERDIR")
	binPath := filepath.Join(t.TempDir(), "a")
	buildArgs := []string{"build", "-o", binPath, "."}
	if covDir != "" {
		// #nosec G703 -- covDir is the coverage directory the Justfile hands this
		// test, not user input; 0750 so `go tool covdata` can read it back.
		require.NoError(t, os.MkdirAll(covDir, 0o750))
		buildArgs = []string{"build", "-cover", "-o", binPath, "."}
	}
	// #nosec G204 -- test builds the current module with a controlled temp output path
	if out, err := exec.Command("go", buildArgs...).CombinedOutput(); err != nil {
		t.Fatalf("build failed: %v\n%s", err, out)
	}

	home := t.TempDir()
	env := append(os.Environ(),
		"HOME="+home,
		"XDG_CONFIG_HOME="+filepath.Join(home, "cfg"),
		"XDG_CACHE_HOME="+filepath.Join(home, "cache"),
		// Inherited from os.Environ() otherwise, which would write the subprocess's
		// log into the developer's real ~/.local/state.
		"XDG_STATE_HOME="+filepath.Join(home, "state"),
	)
	if covDir != "" {
		env = append(env, "GOCOVERDIR="+covDir)
	}
	run := func(args ...string) (string, error) {
		// #nosec G204 -- launches the freshly built test binary with controlled args
		c := exec.Command(binPath, args...)
		c.Env = env
		out, err := c.CombinedOutput()
		return string(out), err
	}
	runWithEnv := func(extra []string, args ...string) (string, error) {
		// #nosec G204 -- launches the freshly built test binary with controlled args
		c := exec.Command(binPath, args...)
		c.Env = append(append([]string{}, env...), extra...)
		out, err := c.CombinedOutput()
		return string(out), err
	}

	// Subcommand runs the full PersistentPreRunE wiring (init/load/logging).
	out, err := run("completion", "bash")
	require.NoError(t, err, out)
	assert.NotEmpty(t, out, "completion script should be produced")

	_, err = run("completion", "powershell")
	assert.Error(t, err, "unknown shell should fail")

	// PersistentPreRunE's two error returns, which only main() reaches. Both must
	// exit nonzero and say which stage failed: a config the tool cannot set up is
	// not something to proceed past.
	blocker := filepath.Join(home, "notadir")
	require.NoError(t, os.WriteFile(blocker, []byte("x"), 0o600))
	out, err = runWithEnv([]string{"XDG_CONFIG_HOME=" + filepath.Join(blocker, "child")}, "config", "show")
	require.Error(t, err, "an unusable config directory must fail the command")
	assert.Contains(t, out, "error initializing paths")

	badHome := t.TempDir()
	badCfgDir := filepath.Join(badHome, "a")
	require.NoError(t, os.MkdirAll(badCfgDir, 0o700))
	// #nosec G306 -- group/other-accessible on purpose so LoadConfig rejects it
	require.NoError(t, os.WriteFile(filepath.Join(badCfgDir, "config.yaml"), []byte("github_user: x\n"), 0o644))
	out, err = runWithEnv([]string{"XDG_CONFIG_HOME=" + badHome}, "config", "show")
	require.Error(t, err, "a group-readable config must fail the command")
	assert.Contains(t, out, "error loading config")

	// Full encrypt/decrypt roundtrip through the binary.
	sshDir := filepath.Join(home, ".ssh")
	require.NoError(t, os.MkdirAll(sshDir, 0o700))
	priv := filepath.Join(sshDir, "id_ed25519")
	// #nosec G204 -- ssh-keygen args are literals except the temp key path
	if out, err := exec.Command("ssh-keygen", "-t", "ed25519", "-N", "", "-f", priv).CombinedOutput(); err != nil {
		t.Fatalf("ssh-keygen failed: %v\n%s", err, out)
	}

	plain := filepath.Join(home, "msg.txt")
	require.NoError(t, os.WriteFile(plain, []byte("integration secret"), 0o600))
	enc := filepath.Join(home, "msg.age")
	out, err = run("encrypt", "-i", plain, "-o", enc, "-r", priv+".pub")
	require.NoError(t, err, out)
	assert.FileExists(t, enc)

	dec := filepath.Join(home, "msg.dec")
	out, err = run("decrypt", "-i", enc, "-o", dec, "--ssh-key", priv)
	require.NoError(t, err, out)
	got, err := os.ReadFile(dec) // #nosec G304 -- test temp path
	require.NoError(t, err)
	assert.Equal(t, "integration secret", string(got))

	// Shorthand aliases with positional args and derived output paths:
	// `a e <file>` -> <file>.age, then `a d <file>.age` -> <file>.
	sh := filepath.Join(home, "note.txt")
	require.NoError(t, os.WriteFile(sh, []byte("shorthand secret"), 0o600))
	out, err = run("config", "set", "ssh_key_path", priv)
	require.NoError(t, err, out)
	out, err = run("config", "set", "default_recipients", priv+".pub")
	require.NoError(t, err, out)

	out, err = run("e", sh) // encrypt shorthand, output note.txt.age
	require.NoError(t, err, out)
	assert.FileExists(t, sh+".age")

	require.NoError(t, os.Remove(sh)) // decrypt shorthand recreates note.txt
	out, err = run("d", sh+".age")
	require.NoError(t, err, out)
	got, err = os.ReadFile(sh) // #nosec G304 -- test temp path
	require.NoError(t, err)
	assert.Equal(t, "shorthand secret", string(got))
}
