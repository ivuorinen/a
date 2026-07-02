package main

import (
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"

	"github.com/ivuorinen/a/cmd"
)

func discardLogger() *slog.Logger {
	return slog.New(slog.NewJSONHandler(io.Discard, nil))
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

	assert.NoError(t, setupLogging(true))
	assert.NoError(t, setupLogging(false))
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

// Helper to generate a temporary SSH keypair for testing.
//
// Each keypair is placed in its own subdirectory so multiple keypairs can be
// generated under the same parent dir without ssh-keygen refusing to overwrite an
// existing id_rsa. The file is still named id_rsa, which decryption requires.
func generateSSHKeyPair(dir string) (privKey, pubKey string, err error) {
	keyDir, err := os.MkdirTemp(dir, "key")
	if err != nil {
		return "", "", err
	}
	privKey = filepath.Join(keyDir, "id_rsa")
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
	privKey, pubKey, err := generateSSHKeyPair(tempDir)
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
	_, pubKey1, err := generateSSHKeyPair(tempDir)
	require.NoError(t, err)
	privKey2, _, err := generateSSHKeyPair(tempDir)
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
	assert.NoError(t, setupLogging(false), "bad log path should fall back to stderr, not error")
}

func TestInitConfigPathsWrapperError(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	blocker := filepath.Join(t.TempDir(), "notadir")
	require.NoError(t, os.WriteFile(blocker, []byte("x"), 0o600))
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(blocker, "child"))
	assert.Error(t, initConfigPaths())
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
func TestCLIIntegration(t *testing.T) {
	for _, bin := range []string{"go", "ssh-keygen"} {
		if _, err := exec.LookPath(bin); err != nil {
			t.Skipf("%s not available", bin)
		}
	}

	binPath := filepath.Join(t.TempDir(), "a")
	// #nosec G204 -- test builds the current module with a controlled temp output path
	if out, err := exec.Command("go", "build", "-o", binPath, ".").CombinedOutput(); err != nil {
		t.Fatalf("build failed: %v\n%s", err, out)
	}

	home := t.TempDir()
	env := append(os.Environ(),
		"HOME="+home,
		"XDG_CONFIG_HOME="+filepath.Join(home, "cfg"),
		"XDG_CACHE_HOME="+filepath.Join(home, "cache"),
	)
	run := func(args ...string) (string, error) {
		// #nosec G204 -- launches the freshly built test binary with controlled args
		c := exec.Command(binPath, args...)
		c.Env = env
		out, err := c.CombinedOutput()
		return string(out), err
	}

	// Subcommand runs the full PersistentPreRunE wiring (init/load/logging).
	out, err := run("completion", "bash")
	require.NoError(t, err, out)
	assert.NotEmpty(t, out, "completion script should be produced")

	_, err = run("completion", "powershell")
	assert.Error(t, err, "unknown shell should fail")

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
