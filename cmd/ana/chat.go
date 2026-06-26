package main

import (
	"github.com/spf13/cobra"

	"github.com/omarluq/anamnesis/internal/terminal"
)

func newChatCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "chat",
		Short: "Launch the interactive chat shell",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runChat(cmd)
		},
	}
}

// runChat launches the interactive terminal chat shell; it backs both the bare
// `ana` invocation and the explicit `ana chat` subcommand.
func runChat(cmd *cobra.Command) error {
	return terminal.Run(cmd.Context(), terminal.RunOptions{Trace: nil, Title: ""})
}
