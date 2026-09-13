package cmd

import (
	"os"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// withCapturedStdout redirects os.Stdout to a temp file while fn runs, keeping
// generated completion scripts out of the test output, and returns what was
// written.
//
// Captured rather than discarded: every generator returns nil, so an
// error-only assertion cannot tell GenBashCompletion from GenZshCompletion and
// a switch that mapped every shell to the same generator passed.
func withCapturedStdout(t *testing.T, fn func() error) (string, error) {
	t.Helper()
	old := os.Stdout
	f, err := os.CreateTemp(t.TempDir(), "stdout")
	require.NoError(t, err)
	os.Stdout = f
	runErr := fn()
	os.Stdout = old
	require.NoError(t, f.Close())

	out, err := os.ReadFile(f.Name()) // #nosec G304 -- test temp path
	require.NoError(t, err)
	return string(out), runErr
}

func TestCompletion_ValidShells(t *testing.T) {
	root := &cobra.Command{Use: "a"}
	c := Completion(root)
	// Markers unique to each generator's output, so mapping a case to the wrong
	// generator fails instead of passing on a nil error. Verified against the
	// pinned cobra: each string appears in exactly one of the three scripts.
	for shell, marker := range map[string]string{
		"bash": "__start_a",
		"zsh":  "#compdef",
		"fish": "# fish completion for a",
	} {
		out, err := withCapturedStdout(t, func() error { return c.RunE(c, []string{shell}) })
		require.NoError(t, err, "shell %s should generate completion", shell)
		assert.Contains(t, out, marker, "%s must produce the %s script", shell, shell)
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
