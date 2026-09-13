package cmd

import (
	"bytes"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// runConfig runs the config subcommand named by args[0] against cfg and returns
// its stdout. Passing no args runs the bare `config` (help + current config).
func runConfig(t *testing.T, cfg *Config, args ...string) (string, error) {
	t.Helper()
	c := ConfigCmd(cfg, func(*Config) error { return nil })
	var out bytes.Buffer
	c.SetOut(&out)
	c.SetErr(&out)
	c.SetArgs(args)
	err := c.Execute()
	return out.String(), err
}

func TestConfig_BareShowsHelpAndConfig(t *testing.T) {
	out, err := runConfig(t, &Config{GitHubUser: "octocat"})
	require.NoError(t, err)
	assert.Contains(t, out, "a config set")
	assert.Contains(t, out, "github_user: octocat")
}

// A mistyped subcommand must fail, not fall through to the bare `config` banner
// and exit 0.
//
// The command has to be run through a parent here, the way a.go wires it, not
// through runConfig: cobra's legacyArgs rejects unknown args on a *root* command
// with subcommands, but returns nil once the same command has a parent. Testing
// ConfigCmd standalone therefore passes with or without the Args validator and
// proves nothing.
func TestConfig_RejectsUnknownSubcommand(t *testing.T) {
	root := &cobra.Command{Use: "a"}
	root.AddCommand(ConfigCmd(&Config{}, func(*Config) error { return nil }))
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&out)
	root.SetArgs([]string{"config", "shwo"})
	require.Error(t, root.Execute())
}

func TestConfig_Show(t *testing.T) {
	out, err := runConfig(t, &Config{SSHKeyPath: "/k"}, "show")
	require.NoError(t, err)
	assert.Contains(t, out, "ssh_key_path: /k")
}

// Driven off configKeys so a key added without a case fails the completeness
// check below rather than going untested. Under the previous hand-written form
// the name promised "each key" and log_file_path had no case at all.
func TestConfig_SetEachKey(t *testing.T) {
	cases := map[string]struct {
		value string
		check func(*testing.T, *Config)
	}{
		"ssh_key_path": {"/home/me/id_ed25519", func(t *testing.T, c *Config) {
			t.Helper()
			assert.Equal(t, "/home/me/id_ed25519", c.SSHKeyPath)
		}},
		"github_user": {"octocat", func(t *testing.T, c *Config) {
			t.Helper()
			assert.Equal(t, "octocat", c.GitHubUser)
		}},
		"cache_ttl_minutes": {"30", func(t *testing.T, c *Config) {
			t.Helper()
			assert.Equal(t, 30, c.CacheTTLMinutes)
		}},
		"default_recipients": {"a.pub,, b.pub,", func(t *testing.T, c *Config) {
			t.Helper()
			assert.Equal(t, []string{"a.pub", "b.pub"}, c.DefaultRecipients,
				"comma-split, trimmed, empties dropped")
		}},
		"log_file_path": {"/var/log/a.log", func(t *testing.T, c *Config) {
			t.Helper()
			assert.Equal(t, "/var/log/a.log", c.LogFilePath)
		}},
	}

	assert.Len(t, cases, len(configKeys), "every key in configKeys needs a case")
	for _, key := range configKeys {
		tc, ok := cases[key]
		require.True(t, ok, "configKeys lists %q with no test case", key)
		t.Run(key, func(t *testing.T) {
			cfg := &Config{}
			_, err := runConfig(t, cfg, "set", key, tc.value)
			require.NoError(t, err)
			tc.check(t, cfg)
		})
	}
}

func TestConfig_SetRejectsBadKeyAndValue(t *testing.T) {
	_, err := runConfig(t, &Config{}, "set", "nope", "x")
	assert.ErrorContains(t, err, "unknown config key")

	_, err = runConfig(t, &Config{}, "set", "cache_ttl_minutes", "notanint")
	assert.ErrorContains(t, err, "must be an integer")
}

func TestConfig_RemResetsField(t *testing.T) {
	cfg := &Config{GitHubUser: "octocat", CacheTTLMinutes: 99, DefaultRecipients: []string{"a.pub"}}
	_, err := runConfig(t, cfg, "rem", "github_user")
	require.NoError(t, err)
	assert.Empty(t, cfg.GitHubUser)

	_, err = runConfig(t, cfg, "rem", "default_recipients")
	require.NoError(t, err)
	assert.Nil(t, cfg.DefaultRecipients)

	// rem resets to the documented default (120), not 0 (which would disable caching).
	_, err = runConfig(t, cfg, "rem", "cache_ttl_minutes")
	require.NoError(t, err)
	assert.Equal(t, defaultCacheTTLMinutes, cfg.CacheTTLMinutes)
}

func TestConfig_SaveErrorPropagates(t *testing.T) {
	c := ConfigCmd(&Config{}, func(*Config) error { return assert.AnError })
	c.SetArgs([]string{"set", "github_user", "x"})
	c.SetOut(&bytes.Buffer{})
	assert.ErrorIs(t, c.Execute(), assert.AnError)
}
