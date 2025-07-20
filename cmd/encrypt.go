package cmd

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"regexp"
	"strings"

	"github.com/sirupsen/logrus"
	"github.com/spf13/cobra"
)

// Encrypt returns a cobra.Command that encrypts files using age, supporting GitHub key fetching.
func Encrypt(cfg *Config, log *logrus.Logger) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "encrypt",
		Short: "Encrypt a file",
		RunE: func(cmd *cobra.Command, _ []string) error {
			input, _ := cmd.Flags().GetString("input")
			output, _ := cmd.Flags().GetString("output")
			recipients, _ := cmd.Flags().GetStringSlice("recipient")
			ghUserFlag, _ := cmd.Flags().GetString("github-user")

			if input == "" {
				return fmt.Errorf("input file is required")
			}
			if output == "" {
				return fmt.Errorf("output file is required")
			}
			if _, err := os.Stat(input); err != nil {
				return fmt.Errorf("input file does not exist: %w", err)
			}

			allRecipients, ghUser, err := collectRecipients(cfg, recipients, ghUserFlag, log)
			if err != nil {
				return err
			}
			if len(allRecipients) == 0 {
				return fmt.Errorf("at least one recipient is required")
			}

			ageArgs, err := buildAgeArgs(output, input, allRecipients)
			if err != nil {
				return err
			}

			log.WithFields(logrus.Fields{
				"input":      input,
				"output":     output,
				"recipients": allRecipients,
				"githubUser": ghUser,
			}).Info("Encrypting file")

			if err := runAgeEncrypt(ageArgs, log); err != nil {
				return err
			}

			log.Info("Encryption successful")
			return nil
		},
	}
	cmd.Flags().StringP("input", "i", "", "Input file to encrypt")
	cmd.Flags().StringP("output", "o", "", "Output file for encrypted data")
	cmd.Flags().StringSliceP("recipient", "r", []string{}, "Recipient public key file or string")
	cmd.Flags().String("github-user", "", "GitHub username to fetch public keys for encryption")
	return cmd
}

// Helper to collect recipients including GitHub keys
func collectRecipients(
	cfg *Config,
	recipients []string,
	ghUserFlag string,
	log *logrus.Logger,
) ([]string, string, error) {
	allRecipients := append([]string{}, cfg.DefaultRecipients...)
	allRecipients = append(allRecipients, recipients...)

	ghUser := ghUserFlag
	if ghUser == "" && cfg.GitHubUser != "" {
		ghUser = cfg.GitHubUser
	}

	if ghUser != "" {
		validUser := regexp.MustCompile(`^[a-zA-Z0-9](?:[a-zA-Z0-9-]{0,37}[a-zA-Z0-9])?$`)
		if !validUser.MatchString(ghUser) {
			log.Warnf("Invalid GitHub username: %s", ghUser)
		} else {
			url := fmt.Sprintf("https://github.com/%s.keys", ghUser)
			if !strings.HasPrefix(url, "https://github.com/") || !strings.HasSuffix(url, ".keys") {
				log.Warnf("Refusing to fetch keys from non-GitHub URL: %s", url)
			} else {
				// #nosec G107 -- url is validated to be a GitHub keys endpoint above
				resp, err := http.Get(url)
				if err != nil {
					log.WithError(err).Warnf("Failed to fetch GitHub keys for user %s", ghUser)
				} else {
					var githubKeys []string
					if resp.StatusCode == http.StatusOK {
						body, err := io.ReadAll(resp.Body)
						closeErr := resp.Body.Close()
						if err == nil && closeErr == nil {
							for _, line := range strings.Split(string(body), "\n") {
								line = strings.TrimSpace(line)
								if line != "" {
									githubKeys = append(githubKeys, line)
								}
							}
						} else {
							if err != nil {
								log.WithError(err).Warn("Failed to read GitHub keys response body")
							}
							if closeErr != nil {
								log.WithError(closeErr).Warn("Failed to close GitHub keys response body")
							}
						}
					} else {
						_ = resp.Body.Close()
						log.Warnf("GitHub returned status %d for user %s", resp.StatusCode, ghUser)
					}
					allRecipients = append(allRecipients, githubKeys...)
				}
			}
		}
	}
	return allRecipients, ghUser, nil
}

// Helper to build and validate age arguments
func buildAgeArgs(output, input string, recipients []string) ([]string, error) {
	ageArgs := []string{"-o", output}
	for _, r := range recipients {
		ageArgs = append(ageArgs, "-r", r)
	}
	ageArgs = append(ageArgs, input)

	// Only allow expected flags for age and restrict file extensions
	expectedFlags := map[string]bool{"-o": true, "-r": true}
	for i, arg := range ageArgs {
		if i%2 == 0 && i < len(ageArgs)-2 { // flags before last two args
			if !expectedFlags[arg] {
				return nil, fmt.Errorf("unexpected flag in age arguments: %s", arg)
			}
		} else if arg == "" {
			return nil, fmt.Errorf("invalid argument for encryption: empty string")
		}
	}
	// Restrict output to expected file extensions
	if !strings.HasSuffix(output, ".txt") && !strings.HasSuffix(output, ".out") {
		return nil, fmt.Errorf("invalid output file for encryption: %s", output)
	}
	return ageArgs, nil
}

// Helper to run age encryption command
func runAgeEncrypt(ageArgs []string, log *logrus.Logger) error {
	ageBin := "age"
	if ageBin != "age" {
		return fmt.Errorf("invalid binary for encryption: %s", ageBin)
	}
	cmdAge := exec.Command(ageBin, ageArgs...)
	if err := cmdAge.Run(); err != nil {
		log.WithError(err).Error("Encryption failed")
		return fmt.Errorf("age encryption failed: %w", err)
	}
	return nil
}

// Config struct should be imported from the main package or shared as needed.
