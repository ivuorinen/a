package cmd

import (
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	"filippo.io/age"
	"filippo.io/age/agessh"
	"github.com/spf13/cobra"
)

// resolveIO determines the input and output files for the encrypt/decrypt
// commands. Input comes from --input or the first positional arg; when --output
// is omitted it is derived from the input via deriveOutput. Both must resolve to
// non-empty values and the input file must exist.
func resolveIO(cmd *cobra.Command, args []string, deriveOutput func(string) string) (input, output string, err error) {
	input, _ = cmd.Flags().GetString("input")
	if input == "" && len(args) > 0 {
		input = args[0]
	}
	output, _ = cmd.Flags().GetString("output")
	if output == "" && input != "" {
		output = deriveOutput(input)
	}
	if input == "" {
		return "", "", fmt.Errorf("input file is required")
	}
	if output == "" {
		return "", "", fmt.Errorf("output file is required")
	}
	if _, err := os.Stat(input); err != nil {
		return "", "", fmt.Errorf("input file does not exist: %w", err)
	}
	return input, output, nil
}

// Encrypt returns a cobra.Command that encrypts files using age, supporting GitHub key fetching.
func Encrypt(cfg *Config, log *slog.Logger) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "encrypt [input] [github-user]",
		Aliases: []string{"e"},
		Short:   "Encrypt a file (output defaults to <input>.age)",
		Args:    cobra.MaximumNArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			input, output, err := resolveIO(cmd, args, func(in string) string { return in + ".age" })
			if err != nil {
				return err
			}
			recipients, _ := cmd.Flags().GetStringSlice("recipient")
			ghUserFlag, _ := cmd.Flags().GetString("github-user")
			if ghUserFlag == "" && len(args) > 1 {
				ghUserFlag = args[1]
			}

			allRecipients, ghUser := collectRecipients(cfg, recipients, ghUserFlag, log)
			if len(allRecipients) == 0 {
				return fmt.Errorf("at least one recipient is required")
			}

			recips, err := parseRecipients(allRecipients)
			if err != nil {
				return err
			}

			log.Info("Encrypting file",
				"input", input,
				"output", output,
				"recipients", allRecipients,
				"githubUser", ghUser)

			if err := encryptFile(input, output, recips); err != nil {
				log.Error("Encryption failed", "error", err)
				return fmt.Errorf("encryption failed: %w", err)
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

// collectRecipients gathers recipients from config defaults, the --recipient flag,
// and (when a GitHub user is set) that user's published keys. It returns the
// recipient list and the resolved GitHub username.
func collectRecipients(
	cfg *Config,
	recipients []string,
	ghUserFlag string,
	log *slog.Logger,
) ([]string, string) {
	allRecipients := slices.Concat(cfg.DefaultRecipients, recipients)

	ghUser := ghUserFlag
	if ghUser == "" && cfg.GitHubUser != "" {
		ghUser = cfg.GitHubUser
	}

	// A valid username is safe to interpolate into the keys URL and cache filename.
	if ghUser != "" {
		if !githubUsernameRE.MatchString(ghUser) {
			log.Warn("Invalid GitHub username", "user", ghUser)
		} else {
			allRecipients = append(allRecipients, fetchGitHubKeys(cfg, ghUser, log)...)
		}
	}
	return allRecipients, ghUser
}

// githubUsernameRE matches a valid GitHub username (alphanumerics and hyphens, no
// leading/trailing hyphen, max 39 chars).
var githubUsernameRE = regexp.MustCompile(`^[a-zA-Z0-9](?:[a-zA-Z0-9-]{0,37}[a-zA-Z0-9])?$`)

// githubKeysURL builds the URL serving a user's published SSH keys. It is a package
// variable so tests can redirect it to a local server.
var githubKeysURL = func(ghUser string) string {
	return fmt.Sprintf("https://github.com/%s.keys", ghUser)
}

// keysHTTPClient bounds how long a GitHub key fetch may block; a slow or hostile
// server must not hang the CLI indefinitely.
var keysHTTPClient = &http.Client{Timeout: 30 * time.Second}

// maxKeysResponseBytes caps the GitHub .keys response we will read into memory.
// Real responses are a few KB; the cap prevents a huge/hostile response from
// exhausting memory. A user with more than this many keys is not a real case.
const maxKeysResponseBytes = 1 << 20 // 1 MiB

// fetchGitHubKeys returns the SSH public keys published at github.com/<ghUser>.keys.
//
// Results are cached under cfg.CacheDir for cfg.CacheTTLMinutes so repeated
// encryptions do not hit the network every time. A non-positive TTL or an empty
// cache dir disables caching. ghUser must already be validated by the caller.
func fetchGitHubKeys(cfg *Config, ghUser string, log *slog.Logger) []string {
	cachePath := ""
	if cfg.CacheDir != "" && cfg.CacheTTLMinutes > 0 {
		cachePath = filepath.Join(cfg.CacheDir, ghUser+".keys")
		if keys, ok := readKeyCache(cachePath, cfg.CacheTTLMinutes); ok {
			log.Debug("Using cached GitHub keys", "user", ghUser)
			return keys
		}
	}

	// #nosec G107 -- the host is fixed by githubKeysURL and ghUser is regex-validated by the caller
	resp, err := keysHTTPClient.Get(githubKeysURL(ghUser))
	if err != nil {
		log.Warn("Failed to fetch GitHub keys", "user", ghUser, "error", err)
		return nil
	}
	defer func() {
		if closeErr := resp.Body.Close(); closeErr != nil {
			log.Warn("Failed to close GitHub keys response body", "error", closeErr)
		}
	}()
	if resp.StatusCode != http.StatusOK {
		log.Warn("GitHub returned non-OK status", "status", resp.StatusCode, "user", ghUser)
		return nil
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxKeysResponseBytes))
	if err != nil {
		log.Warn("Failed to read GitHub keys response body", "error", err)
		return nil
	}
	if cachePath != "" {
		writeKeyCache(cachePath, body, log)
	}
	return parseKeyLines(string(body))
}

// parseKeyLines splits a GitHub .keys response into non-empty, trimmed key lines.
func parseKeyLines(body string) []string {
	var keys []string
	for line := range strings.SplitSeq(body, "\n") {
		if line = strings.TrimSpace(line); line != "" {
			keys = append(keys, line)
		}
	}
	return keys
}

// readKeyCache returns cached keys when cachePath exists and is younger than
// ttlMinutes. The boolean is false when the cache is missing, stale, or unreadable.
func readKeyCache(cachePath string, ttlMinutes int) ([]string, bool) {
	info, err := os.Stat(cachePath)
	if err != nil {
		return nil, false
	}
	if time.Since(info.ModTime()) > time.Duration(ttlMinutes)*time.Minute {
		return nil, false
	}
	// #nosec G304 -- cachePath is cfg.CacheDir (os.UserCacheDir-derived) joined with a validated GitHub username
	data, err := os.ReadFile(cachePath)
	if err != nil {
		return nil, false
	}
	return parseKeyLines(string(data)), true
}

// writeKeyCache stores the raw .keys response; failures are non-fatal (best effort).
func writeKeyCache(cachePath string, body []byte, log *slog.Logger) {
	// #nosec G703 -- cachePath is cfg.CacheDir (os.UserCacheDir-derived) joined with a validated GitHub username
	if err := os.WriteFile(cachePath, body, 0o600); err != nil {
		log.Warn("Failed to cache GitHub keys", "path", cachePath, "error", err)
	}
}

// parseRecipients resolves each input into one or more age recipients. An input
// is either a public-key file (an SSH .pub file or a recipients file, one key per
// line) or a literal recipient string (an "ssh-..." key line or an "age1..." key).
func parseRecipients(inputs []string) ([]age.Recipient, error) {
	var recipients []age.Recipient
	for _, in := range inputs {
		if in == "" {
			return nil, fmt.Errorf("invalid argument for encryption: empty recipient")
		}
		lines := []string{in}
		// #nosec G304 G703 -- recipient path is user-provided by design (config/flags/GitHub)
		if info, statErr := os.Stat(in); statErr == nil && !info.IsDir() {
			// #nosec G304 G703 -- recipient path is user-provided by design (config/flags/GitHub)
			data, readErr := os.ReadFile(in)
			if readErr != nil {
				return nil, fmt.Errorf("reading recipient file %s: %w", in, readErr)
			}
			lines = strings.Split(string(data), "\n")
		}
		for _, line := range lines {
			if line = strings.TrimSpace(line); line == "" || strings.HasPrefix(line, "#") {
				continue
			}
			r, err := parseRecipient(line)
			if err != nil {
				return nil, fmt.Errorf("invalid recipient %q: %w", line, err)
			}
			recipients = append(recipients, r)
		}
	}
	if len(recipients) == 0 {
		return nil, fmt.Errorf("no valid recipients found")
	}
	return recipients, nil
}

// parseRecipient parses a single recipient line: an SSH public key ("ssh-...") or
// a native age recipient ("age1...").
func parseRecipient(s string) (age.Recipient, error) {
	if strings.HasPrefix(s, "ssh-") {
		return agessh.ParseRecipient(s)
	}
	return age.ParseX25519Recipient(s)
}

// encryptFile encrypts input to output for the given recipients, writing the age
// file with 0600 permissions.
//
// It writes to a temp file in the target directory and renames onto output only
// after encryption fully succeeds (mirrors tryDecrypt): a failed or partial
// encryption never truncates a pre-existing file or leaves a half-written .age at
// the target path.
func encryptFile(input, output string, recipients []age.Recipient) (err error) {
	// #nosec G304 -- input path is a validated CLI flag/argument
	in, err := os.Open(input)
	if err != nil {
		return fmt.Errorf("opening input: %w", err)
	}
	defer func() { _ = in.Close() }()

	// os.CreateTemp creates the file with 0600.
	tmp, err := os.CreateTemp(filepath.Dir(output), ".a-encrypt-*")
	if err != nil {
		return fmt.Errorf("creating temp output: %w", err)
	}
	tmpName := tmp.Name()
	defer func() {
		if err != nil {
			_ = os.Remove(tmpName)
		}
	}()

	w, err := age.Encrypt(tmp, recipients...)
	if err != nil {
		_ = tmp.Close()
		return fmt.Errorf("initializing encryption: %w", err)
	}
	if _, err = io.Copy(w, in); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("writing ciphertext: %w", err)
	}
	// Close the age writer first (flushes the final chunk), then the temp file.
	if err = w.Close(); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("finalizing encryption: %w", err)
	}
	if err = tmp.Close(); err != nil {
		return fmt.Errorf("closing temp output: %w", err)
	}
	if err = os.Rename(tmpName, output); err != nil {
		return fmt.Errorf("finalizing output: %w", err)
	}
	return nil
}
