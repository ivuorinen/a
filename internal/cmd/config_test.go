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

func TestConfig_SetEachKey(t *testing.T) {
	cfg := &Config{}
	_, err := runConfig(t, cfg, "set", "ssh_key_path", "/home/me/id_ed25519")
	require.NoError(t, err)
	assert.Equal(t, "/home/me/id_ed25519", cfg.SSHKeyPath)

	_, err = runConfig(t, cfg, "set", "github_user", "octocat")
	require.NoError(t, err)
	assert.Equal(t, "octocat", cfg.GitHubUser)

	_, err = runConfig(t, cfg, "set", "cache_ttl_minutes", "30")
	require.NoError(t, err)
	assert.Equal(t, 30, cfg.CacheTTLMinutes)

	_, err = runConfig(t, cfg, "set", "default_recipients", "a.pub,, b.pub,")
	require.NoError(t, err)
	assert.Equal(t, []string{"a.pub", "b.pub"}, cfg.DefaultRecipients, "comma-split, trimmed, empties dropped")
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
