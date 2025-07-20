package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sirupsen/logrus"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"gopkg.in/yaml.v2"

	"github.com/ivuorinen/a/cmd"
)

func TestInitConfigPaths(t *testing.T) {
	paths, err := cmd.InitConfigPaths()
	assert.NoError(t, err, "initializing config paths should not produce an error")

	assert.DirExists(t, paths.ConfigDir, "config directory should exist")
	assert.FileExists(t, paths.ConfigFile, "config file path should exist")
	assert.DirExists(t, paths.CacheDir, "cache directory should exist")
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

	err := cmd.SaveConfig(cfgFile, cfg)
	assert.NoError(t, err, "saving config should not produce an error")

	loadedCfg, err := cmd.LoadConfig(cfgFile)
	assert.NoError(t, err, "loading config should not produce an error")
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
	assert.NoError(t, err, "marshaling config should not produce an error")
	assert.NoError(t, os.WriteFile(cfgFile, data, 0o600))

	loadedCfg, err := cmd.LoadConfig(cfgFile)
	assert.NoError(t, err, "loading config should not produce an error")
	assert.NotEmpty(t, loadedCfg.LogFilePath, "default log file path should be set")
}

func TestSetupLogging(t *testing.T) {
	tempLogFile := filepath.Join(t.TempDir(), "cli.log")
	cfg := &cmd.Config{LogFilePath: tempLogFile}

	log := logrus.New()
	log.SetFormatter(&logrus.JSONFormatter{})
	logFile, err := os.OpenFile(cfg.LogFilePath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	assert.NoError(t, err, "opening log file should not produce an error")
	log.SetOutput(logFile)
	log.SetLevel(logrus.InfoLevel)

	log.Info("Test log entry")
	assert.FileExists(t, tempLogFile, "log file should exist after setup")
}

func TestCmdConfig(t *testing.T) {
	cfg := &cmd.Config{}
	cmdObj := cmd.ConfigCmd(cfg, func(_ any) error { return nil })
	assert.NotNil(t, cmdObj, "ConfigCmd should return a non-nil cobra command")

	flags := cmdObj.Flags()
	sshKey, _ := flags.GetString("ssh-key")
	assert.Empty(t, sshKey, "default ssh-key flag should be empty")
}

func TestCmdEncryptPlaceholder(t *testing.T) {
	cfg := &cmd.Config{}
	log := logrus.New()
	cmdObj := cmd.Encrypt(cfg, log)
	assert.NotNil(t, cmdObj, "Encrypt should return a non-nil cobra command")
}

func TestCmdDecryptPlaceholder(t *testing.T) {
	cfg := &cmd.Config{}
	log := logrus.New()
	cmdObj := cmd.Decrypt(cfg, log)
	assert.NotNil(t, cmdObj, "Decrypt should return a non-nil cobra command")
}

func TestCmdCompletion(t *testing.T) {
	rootCmd := &cobra.Command{Use: "a"}
	cmdObj := cmd.Completion(rootCmd)
	assert.NotNil(t, cmdObj, "Completion should return a non-nil cobra command")
}

// Helper to generate a temporary SSH keypair for testing
func generateSSHKeyPair(dir string) (privKey, pubKey string, err error) {
	privKey = filepath.Join(dir, "id_rsa")
	pubKey = privKey + ".pub"
	cmd := exec.Command("ssh-keygen", "-t", "rsa", "-b", "2048", "-N", "", "-f", privKey)
	if err := cmd.Run(); err != nil {
		return "", "", err
	}
	return privKey, pubKey, nil
}

// Helper to write test results to a file
func writeTestResult(dir, name string, content []byte) {
	_ = os.WriteFile(filepath.Join(dir, name), content, 0o600)
}

func TestEncryptDecrypt_Success(t *testing.T) {
	tempDir := t.TempDir()
	plaintext := []byte("This is a secret message for encryption test.")

	// Generate SSH keypair
	privKey, pubKey, err := generateSSHKeyPair(tempDir)
	writeTestResult(
		tempDir,
		"sshkeygen_success.txt",
		fmt.Appendf(nil, "priv: %s\npub: %s\nerr: %v", privKey, pubKey, err),
	)
	assert.NoError(t, err, "ssh-keygen should succeed")

	// Write plaintext file
	inputFile := filepath.Join(tempDir, "input.txt")
	assert.NoError(t, os.WriteFile(inputFile, plaintext, 0o600))

	// Prepare config
	cfg := &cmd.Config{
		DefaultRecipients: []string{pubKey},
		LogFilePath:       filepath.Join(tempDir, "cli.log"),
	}
	log := logrus.New()

	// Encrypt
	encryptedFile := filepath.Join(tempDir, "encrypted.txt")
	encryptCmd := cmd.Encrypt(cfg, log)
	err = encryptCmd.Flags().Set("input", inputFile)
	assert.NoError(t, err)
	err = encryptCmd.Flags().Set("output", encryptedFile)
	assert.NoError(t, err)
	err = encryptCmd.RunE(encryptCmd, []string{})
	writeTestResult(tempDir, "encrypt_result.txt", fmt.Appendf(nil, "err: %v", err))
	assert.NoError(t, err)
	assert.FileExists(t, encryptedFile, "encrypted file should exist")

	// Decrypt
	decryptCfg := &cmd.Config{SSHKeyPath: privKey, LogFilePath: cfg.LogFilePath}
	decryptedFile := filepath.Join(tempDir, "decrypted.txt")
	decryptCmd := cmd.Decrypt(decryptCfg, log)
	err = decryptCmd.Flags().Set("input", encryptedFile)
	assert.NoError(t, err)
	err = decryptCmd.Flags().Set("output", decryptedFile)
	assert.NoError(t, err)
	err = decryptCmd.RunE(decryptCmd, []string{})
	writeTestResult(tempDir, "decrypt_result.txt", fmt.Appendf(nil, "err: %v", err))
	assert.NoError(t, err)
	assert.FileExists(t, decryptedFile, "decrypted file should exist")

	// Compare output (decryptedFile is generated by the test and not user-controlled)
	// Ensure decryptedFile exists and is in tempDir before reading (gosec G304 mitigation)
	info, statErr := os.Stat(decryptedFile)
	assert.NoError(t, statErr, "decrypted file should exist before reading")
	assert.True(t, strings.HasPrefix(decryptedFile, tempDir), "decrypted file must be in tempDir")
	assert.Equal(t, info.Mode().Perm(), os.FileMode(0o600), "decrypted file must have 0600 permissions")

	// #nosec G304 -- decryptedFile is generated in tempDir and not user-controlled
	decrypted, err := os.ReadFile(decryptedFile)
	writeTestResult(tempDir, "decrypted.txt", decrypted)
	assert.NoError(t, err)
	assert.Equal(t, plaintext, decrypted, "decrypted output should match original plaintext")
}

func TestEncryptDecrypt_WrongKey(t *testing.T) {
	tempDir := t.TempDir()
	plaintext := []byte("Secret message for wrong key test.")

	// Generate two SSH keypairs
	_, pubKey1, err := generateSSHKeyPair(tempDir)
	assert.NoError(t, err)
	privKey2, _, err := generateSSHKeyPair(tempDir)
	assert.NoError(t, err)

	// Write plaintext file
	inputFile := filepath.Join(tempDir, "input.txt")
	assert.NoError(t, os.WriteFile(inputFile, plaintext, 0o600))

	// Encrypt with pubKey1
	cfg := &cmd.Config{
		DefaultRecipients: []string{pubKey1},
		LogFilePath:       filepath.Join(tempDir, "cli.log"),
	}
	log := logrus.New()
	encryptedFile := filepath.Join(tempDir, "encrypted.txt")
	encryptCmd := cmd.Encrypt(cfg, log)
	err = encryptCmd.Flags().Set("input", inputFile)
	assert.NoError(t, err)
	err = encryptCmd.Flags().Set("output", encryptedFile)
	assert.NoError(t, err)
	err = encryptCmd.RunE(encryptCmd, []string{})
	writeTestResult(tempDir, "encrypt_wrongkey_result.txt", fmt.Appendf(nil, "err: %v", err))
	assert.NoError(t, err)
	assert.FileExists(t, encryptedFile, "encrypted file should exist")

	// Try to decrypt with privKey2 (should fail)
	decryptCfg := &cmd.Config{SSHKeyPath: privKey2, LogFilePath: cfg.LogFilePath}
	decryptedFile := filepath.Join(tempDir, "decrypted_wrongkey.txt")
	decryptCmd := cmd.Decrypt(decryptCfg, log)
	err = decryptCmd.Flags().Set("input", encryptedFile)
	assert.NoError(t, err)
	err = decryptCmd.Flags().Set("output", decryptedFile)
	assert.NoError(t, err)
	err = decryptCmd.RunE(decryptCmd, []string{})
	writeTestResult(tempDir, "decrypt_wrongkey_result.txt", fmt.Appendf(nil, "err: %v", err))
	assert.Error(t, err, "decryption should fail with wrong key")
}

func TestEncryptDecrypt_MissingRecipient(t *testing.T) {
	tempDir := t.TempDir()
	plaintext := []byte("Secret message for missing recipient test.")

	// Write plaintext file
	inputFile := filepath.Join(tempDir, "input.txt")
	assert.NoError(t, os.WriteFile(inputFile, plaintext, 0o600))

	// Encrypt with no recipient
	cfg := &cmd.Config{
		DefaultRecipients: []string{},
		LogFilePath:       filepath.Join(tempDir, "cli.log"),
	}
	log := logrus.New()
	encryptedFile := filepath.Join(tempDir, "encrypted.txt")
	encryptCmd := cmd.Encrypt(cfg, log)
	err := encryptCmd.Flags().Set("input", inputFile)
	assert.NoError(t, err)
	err = encryptCmd.Flags().Set("output", encryptedFile)
	assert.NoError(t, err)
	err = encryptCmd.RunE(encryptCmd, []string{})
	writeTestResult(tempDir, "encrypt_missingrecipient_result.txt", fmt.Appendf(nil, "err: %v", err))
	assert.Error(t, err, "encryption should fail with no recipient")
}
