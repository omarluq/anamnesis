package main_test

import (
	"bytes"
	"testing"

	"github.com/samber/lo"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	main "github.com/omarluq/anamnesis/cmd/ana"
)

func TestRootCmd_HelpListsCommands(t *testing.T) {
	t.Parallel()

	cmd := main.NewRootCmdForTest()
	buf := new(bytes.Buffer)
	cmd.SetOut(buf)
	cmd.SetErr(buf)
	cmd.SetArgs([]string{"--help"})

	err := cmd.Execute()
	require.NoError(t, err)
	assert.Contains(t, buf.String(), "ana")
	assert.True(t, lo.ContainsBy(cmd.Commands(), func(c *cobra.Command) bool {
		return c.Name() == "chat"
	}), "chat command should be registered")
}
