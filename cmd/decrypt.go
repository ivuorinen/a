package cmd

import (
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"filippo.io/age"
	"filippo.io/age/agessh"
	"github.com/spf13/cobra"
)

// tryDecrypt attempts to decrypt input to output using the SSH private key at
// keyPath.
//
// Plaintext is written to a 0600 temp file in the target directory and renamed
// onto output only after decryption fully succeeds. This is critical: age
// authenticates the stream incrementally, so writing straight to output would
// leave a partial, potentially group/world-readable plaintext fragment on disk
// (and destroy any pre-existing file) whenever a decrypt fails partway — a
// tampered or truncated ciphertext, a full disk, or a wrong-but-header-matching
// attempt. The temp-then-rename keeps failures from ever touching the target.
func tryDecrypt(keyPath, output, input string) (err error) {
	if keyPath == "" || output == "" || input == "" {
		return fmt.Errorf("invalid arguments for decryption: empty path")
	}
	// #nosec G304 -- keyPath comes from the --ssh-key flag, config, or a ~/.ssh scan
	pem, err := os.ReadFile(keyPath)
	if err != nil {
		return fmt.Errorf("reading key %s: %w", keyPath, err)
	}
	identity, err := agessh.ParseIdentity(pem)
	if err != nil {
		return fmt.Errorf("parsing key %s: %w", keyPath, err)
	}

	// #nosec G304 -- input path is a validated CLI flag/argument
	in, err := os.Open(input)
	if err != nil {
		return fmt.Errorf("opening input: %w", err)
	}
	defer func() { _ = in.Close() }()

	r, err := age.Decrypt(in, identity)
	if err != nil {
		return err // wrong key or not an age file
	}

	// os.CreateTemp creates the file with 0600; the plaintext is never readable
	// by group/other, even transiently.
	tmp, err := os.CreateTemp(filepath.Dir(output), ".a-decrypt-*")
	if err != nil {
		return fmt.Errorf("creating temp output: %w", err)
	}
	tmpName := tmp.Name()
	// Any failure below (including a failed rename) must remove the temp so no
	// partial plaintext lingers.
	defer func() {
		if err != nil {
			_ = os.Remove(tmpName)
		}
	}()

	if _, err = io.Copy(tmp, r); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("writing plaintext: %w", err)
	}
	if err = tmp.Close(); err != nil {
		return fmt.Errorf("closing temp output: %w", err)
	}
	if err = os.Rename(tmpName, output); err != nil {
		return fmt.Errorf("finalizing output: %w", err)
	}
	return nil
}

// selectSSHKey determines which SSH key to use based on flags and config.
func selectSSHKey(sshKeyFlag string, cfg *Config) string {
	if sshKeyFlag != "" {
		return sshKeyFlag
	}
	return cfg.SSHKeyPath
}

// tryAllKeys attempts decryption with each key in turn, returning the keys it tried
// and whether one succeeded.
func tryAllKeys(keys []string, input, output string, log *slog.Logger) (tried []string, ok bool) {
	for _, keyPath := range keys {
		tried = append(tried, keyPath)
		log.Info("Trying decryption with SSH key", "input", input, "output", output, "sshKey", keyPath)
		err := tryDecrypt(keyPath, output, input)
		if err == nil {
			log.Info("Decryption successful")
			return tried, true
		}
		log.Warn("Decryption failed with key", "key", keyPath, "error", err)
	}
	return tried, false
}

// decryptOutput derives the decrypted filename from the input: it strips a
// trailing ".age", or appends ".dec" when there is none.
func decryptOutput(input string) string {
	if base, ok := strings.CutSuffix(input, ".age"); ok {
		return base
	}
	return input + ".dec"
}

// Decrypt returns a cobra.Command that decrypts files using age, scanning local SSH keys if needed.
func Decrypt(cfg *Config, log *slog.Logger) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "decrypt [input]",
		Aliases: []string{"d"},
		Short:   "Decrypt a file (output defaults to <input> without .age)",
		Args:    cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			input, output, err := resolveIO(cmd, args, decryptOutput)
			if err != nil {
				return err
			}
			sshKeyFlag, _ := cmd.Flags().GetString("ssh-key")

			keys := []string{selectSSHKey(sshKeyFlag, cfg)}
			if keys[0] == "" {
				if keys, err = ScanSSHPrivateKeys(); err != nil {
					return fmt.Errorf("could not scan ~/.ssh for private keys: %w", err)
				}
			}

			if tried, ok := tryAllKeys(keys, input, output, log); !ok {
				return fmt.Errorf("decryption failed: none of the tried SSH keys matched\nTried keys: %v", tried)
			}
			return nil
		},
	}
	cmd.Flags().StringP("input", "i", "", "Input file to decrypt")
	cmd.Flags().StringP("output", "o", "", "Output file for decrypted data")
	cmd.Flags().String("ssh-key", "", "SSH private key to use for decryption")
	return cmd
}
