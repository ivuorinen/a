package cmd

import (
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"filippo.io/age"
	"filippo.io/age/agessh"
	"github.com/spf13/cobra"
	"golang.org/x/crypto/ssh"
	"golang.org/x/term"
)

// passphrasePrompt reads a passphrase for an encrypted SSH key. It is a package
// variable so tests can supply one without a terminal: under `go test` stdin is
// never a tty, so a hardcoded prompt makes the entire successful-decryption path
// for passphrase-protected keys unreachable by any test.
var passphrasePrompt = func(keyPath string) ([]byte, error) {
	fd := int(os.Stdin.Fd())
	if !term.IsTerminal(fd) {
		return nil, fmt.Errorf("key is passphrase protected and stdin is not a terminal")
	}
	fmt.Fprintf(os.Stderr, "Enter passphrase for %q: ", keyPath)
	pass, err := term.ReadPassword(fd)
	fmt.Fprintln(os.Stderr)
	if err != nil {
		return nil, fmt.Errorf("could not read passphrase for %q: %w", keyPath, err)
	}
	return pass, nil
}

// parseSSHIdentity parses an SSH private key into an age identity, prompting for
// a passphrase when the key is protected by one.
//
// agessh.ParseIdentity handles unencrypted keys only; for the rest it returns
// *ssh.PassphraseMissingError. Treating that as "wrong key" made every
// passphrase-protected key — the form ssh-keygen produces whenever its prompt is
// answered, and the form security guidance recommends — fail with "none of the
// tried SSH keys matched", sending the user to check the recipient or conclude
// the ciphertext was lost, when the file opens fine with the age CLI.
//
// The error usually carries the public key, since OpenSSH stores it unencrypted
// in the private key file; the .pub fallback covers the older formats that do not.
func parseSSHIdentity(keyPath string, pem []byte) (age.Identity, error) {
	identity, err := agessh.ParseIdentity(pem)
	var missing *ssh.PassphraseMissingError
	if !errors.As(err, &missing) {
		return identity, err
	}

	pubKey := missing.PublicKey
	if pubKey == nil {
		// #nosec G304 -- derived from keyPath, which is already a trusted key path
		pubBytes, readErr := os.ReadFile(keyPath + ".pub")
		if readErr != nil {
			return nil, fmt.Errorf(
				"key is passphrase protected and its public key is unavailable: %s not readable (%w); "+
					"convert the key with: ssh-keygen -p -m RFC4716 -f %s", keyPath+".pub", readErr, keyPath)
		}
		if pubKey, _, _, _, err = ssh.ParseAuthorizedKey(pubBytes); err != nil {
			return nil, fmt.Errorf("parsing %s: %w", keyPath+".pub", err)
		}
	}

	return agessh.NewEncryptedSSHIdentity(pubKey, pem, func() ([]byte, error) {
		return passphrasePrompt(keyPath)
	})
}

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
	identity, err := parseSSHIdentity(keyPath, pem)
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

// tryAllKeys attempts decryption with each key in turn, returning the keys it
// tried and the joined failures (nil once one succeeds).
//
// The failures are returned rather than only logged. A passphrase-protected key
// and a genuinely non-matching key are different problems with different
// remedies, and collapsing both into "none of the tried SSH keys matched" sent
// users to re-check the recipient — or conclude the ciphertext was lost — when
// the fix was to type a passphrase.
func tryAllKeys(keys []string, input, output string, log *slog.Logger) (tried []string, err error) {
	var failures []error
	for _, keyPath := range keys {
		tried = append(tried, keyPath)
		log.Info("Trying decryption with SSH key", "input", input, "output", output, "sshKey", keyPath)
		attemptErr := tryDecrypt(keyPath, output, input)
		if attemptErr == nil {
			log.Info("Decryption successful")
			return tried, nil
		}
		log.Warn("Decryption failed with key", "key", keyPath, "error", attemptErr)
		failures = append(failures, attemptErr)
	}
	// errors.Join of nothing is nil, which would read as success. No key tried is
	// never success.
	if len(failures) == 0 {
		return tried, errors.New("no SSH keys to try")
	}
	return tried, errors.Join(failures...)
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
			input, output, rest, err := resolveIO(cmd, args, decryptOutput)
			if err != nil {
				return err
			}
			// --input shifts the positional slots, so a leftover arg here means the
			// user named something this command cannot use. Ignoring it silently
			// accepted `decrypt -i a.age b.age` and decrypted only a.age.
			if len(rest) > 0 {
				return fmt.Errorf("unexpected argument %q", rest[0])
			}
			force, _ := cmd.Flags().GetBool("force")
			if err := ensureWritableOutput(output, force); err != nil {
				return err
			}
			sshKeyFlag, _ := cmd.Flags().GetString("ssh-key")

			keys := []string{selectSSHKey(sshKeyFlag, cfg)}
			if keys[0] == "" {
				if keys, err = ScanSSHPrivateKeys(); err != nil {
					return fmt.Errorf("could not scan ~/.ssh for private keys: %w", err)
				}
			}

			// An empty scan is not a failed decryption: reporting "none of the tried
			// keys matched / Tried keys: []" sent the user to ask the sender to
			// re-encrypt, when the fix is local. Keys not named id_* (github_ed25519,
			// work_rsa) land here on a file they can decrypt.
			if len(keys) == 0 {
				return errors.New(
					"no SSH private key to try: ~/.ssh holds no id_* key; " +
						"pass --ssh-key <path> or set it with: a config set ssh_key_path <path>")
			}

			tried, tryErr := tryAllKeys(keys, input, output, log)
			if tryErr != nil {
				return fmt.Errorf("decryption failed; tried %v: %w", tried, tryErr)
			}
			return nil
		},
	}
	cmd.Flags().StringP("input", "i", "", "Input file to decrypt")
	cmd.Flags().StringP("output", "o", "", "Output file for decrypted data")
	cmd.Flags().String("ssh-key", "", "SSH private key to use for decryption")
	cmd.Flags().BoolP("force", "f", false, "Overwrite the output file if it already exists")
	return cmd
}
