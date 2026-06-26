package main_test

import (
	"bytes"
	"testing"

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
	assert.Contains(t, buf.String(), "chat")
}
