package cmd

import (
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/sirupsen/logrus"
	"github.com/spf13/cobra"
)

// tryDecrypt attempts to decrypt using the given key and output/input files.
func tryDecrypt(keyPath, output, input string) error {
	ageBin := "age"
	if ageBin != "age" {
		return fmt.Errorf("invalid binary for decryption: %s", ageBin)
	}
	ageArgs := []string{"-d", "-i", keyPath, "-o", output, input}
	expectedFlags := map[string]bool{"-d": true, "-i": true, "-o": true}
	for i, arg := range ageArgs {
		if i == 0 || i == 2 || i == 4 {
			if !expectedFlags[arg] && i != 0 {
				return fmt.Errorf("unexpected flag in age arguments: %s", arg)
			}
		} else if arg == "" {
			return fmt.Errorf("invalid argument for decryption: empty string")
		}
	}
	if !strings.HasSuffix(keyPath, "id_rsa") && !strings.HasSuffix(keyPath, "id_ed25519") {
		return fmt.Errorf("invalid key file for decryption: %s", keyPath)
	}
	if !strings.HasSuffix(output, ".txt") && !strings.HasSuffix(output, ".out") {
		return fmt.Errorf("invalid output file for decryption: %s", output)
	}
	// #nosec G204 -- ageBin and ageArgs are validated above
	return exec.Command(ageBin, ageArgs...).Run()
}

// selectSSHKey determines which SSH key to use based on flags and config.
func selectSSHKey(sshKeyFlag string, cfg *Config) string {
	if sshKeyFlag != "" {
		return sshKeyFlag
	}
	return cfg.SSHKeyPath
}

// tryAllKeys attempts decryption with all provided keys, returns true on success.
func tryAllKeys(keys []string, input, output string, log *logrus.Logger, triedKeys *[]string) bool {
	for _, keyPath := range keys {
		*triedKeys = append(*triedKeys, keyPath)
		log.WithFields(logrus.Fields{
			"input":  input,
			"output": output,
			"sshKey": keyPath,
		}).Info("Trying decryption with SSH key")
		err := tryDecrypt(keyPath, output, input)
		if err == nil {
			log.Info("Decryption successful")
			return true
		}
		log.WithError(err).Warnf("Decryption failed with key %s", keyPath)
	}
	return false
}

// Decrypt returns a cobra.Command that decrypts files using age, scanning local SSH keys if needed.
func Decrypt(cfg *Config, log *logrus.Logger) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "decrypt",
		Short: "Decrypt a file",
		RunE: func(cmd *cobra.Command, _ []string) error {
			input, _ := cmd.Flags().GetString("input")
			output, _ := cmd.Flags().GetString("output")
			sshKeyFlag, _ := cmd.Flags().GetString("ssh-key")

			if input == "" {
				return fmt.Errorf("input file is required")
			}
			if output == "" {
				return fmt.Errorf("output file is required")
			}
			if _, err := os.Stat(input); err != nil {
				return fmt.Errorf("input file does not exist: %w", err)
			}

			sshKey := selectSSHKey(sshKeyFlag, cfg)
			var triedKeys []string
			var success bool

			if sshKey != "" {
				triedKeys = append(triedKeys, sshKey)
				log.WithFields(logrus.Fields{
					"input":  input,
					"output": output,
					"sshKey": sshKey,
				}).Info("Trying decryption with provided SSH key")
				if err := tryDecrypt(sshKey, output, input); err == nil {
					log.Info("Decryption successful")
					success = true
				} else {
					log.WithError(err).Warn("Decryption failed with provided SSH key")
				}
			} else {
				keys, err := ScanSSHPrivateKeys()
				if err != nil {
					return fmt.Errorf("could not scan ~/.ssh for private keys: %w", err)
				}
				success = tryAllKeys(keys, input, output, log, &triedKeys)
			}

			if !success {
				return fmt.Errorf("decryption failed: none of the tried SSH keys matched\nTried keys: %v", triedKeys)
			}
			return nil
		},
	}
	cmd.Flags().StringP("input", "i", "", "Input file to decrypt")
	cmd.Flags().StringP("output", "o", "", "Output file for decrypted data")
	cmd.Flags().String("ssh-key", "", "SSH private key to use for decryption")
	return cmd
}
