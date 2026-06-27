package main

import (
	"context"

	"github.com/spf13/cobra"

	"github.com/omarluq/anamnesis/internal/di"
	"github.com/omarluq/anamnesis/internal/terminal"
)

// shellRunner launches the interactive terminal shell with opts and reports the
// exit error. terminal.Run is the production launcher; a test substitutes a
// drain-only runner so it can drive the resolved controller to a FINAL without a
// live terminal screen.
type shellRunner func(ctx context.Context, opts terminal.RunOptions) error

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

// runChat launches the interactive terminal chat shell. It resolves the RLM
// controller from the runtime DI container and hands it to the shell, so a
// composer submit spawns a live investigation whose trace events stream into the
// panes. It backs both the bare `ana` invocation and the explicit `ana chat`
// subcommand.
func runChat(cmd *cobra.Command) error {
	controller, err := resolveChatController(cfgFile)
	if err != nil {
		return err
	}

	return runChatWith(cmd.Context(), controller, terminal.Run)
}

// runChatWith drives the chat shell for controller through run, the shell
// launcher. Splitting the launch from controller resolution lets a test feed a
// scripted controller to a FINAL over the same submit path without opening a real
// terminal screen.
func runChatWith(ctx context.Context, controller terminal.Controller, run shellRunner) error {
	return run(ctx, terminal.RunOptions{Trace: nil, Controller: controller, Title: ""})
}

// resolveChatController builds the runtime DI container from configPath and
// resolves the terminal Controller it provides, returning the oops-wrapped error
// from container assembly when the configuration cannot be loaded.
func resolveChatController(configPath string) (terminal.Controller, error) {
	container, err := di.NewContainer(configPath)
	if err != nil {
		return nil, err
	}

	return di.MustInvoke[terminal.Controller](container), nil
}
