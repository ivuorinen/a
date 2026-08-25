package cmd

import (
	"os"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// withSuppressedStdout redirects os.Stdout to a temp file while fn runs so that
// generated completion scripts do not pollute test output.
func withSuppressedStdout(t *testing.T, fn func() error) error {
	t.Helper()
	old := os.Stdout
	f, err := os.CreateTemp(t.TempDir(), "stdout")
	require.NoError(t, err)
	os.Stdout = f
	defer func() {
		os.Stdout = old
		assert.NoError(t, f.Close())
	}()
	return fn()
}

func TestCompletion_ValidShells(t *testing.T) {
	root := &cobra.Command{Use: "a"}
	c := Completion(root)
	for _, shell := range []string{"bash", "zsh", "fish"} {
		err := withSuppressedStdout(t, func() error { return c.RunE(c, []string{shell}) })
		assert.NoError(t, err, "shell %s should generate completion", shell)
	}
}

func TestCompletion_UnknownShell(t *testing.T) {
	c := Completion(&cobra.Command{Use: "a"})
	err := c.RunE(c, []string{"powershell"})
	assert.ErrorContains(t, err, "unsupported shell")
}

func TestCompletion_RequiresExactlyOneArg(t *testing.T) {
	c := Completion(&cobra.Command{Use: "a"})
	assert.Error(t, c.Args(c, []string{}), "zero args should be rejected")
	assert.Error(t, c.Args(c, []string{"bash", "zsh"}), "two args should be rejected")
	assert.NoError(t, c.Args(c, []string{"bash"}))
}
