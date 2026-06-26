package main

import "github.com/spf13/cobra"

var cfgFile string

func newRootCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:           "ana",
		Short:         "ana is a command line tool",
		SilenceUsage:  true,
		SilenceErrors: true,
		Args:          cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runChat(cmd)
		},
	}

	cmd.PersistentFlags().StringVar(&cfgFile, "config", "", "config file path")
	cmd.AddCommand(newChatCmd())
	cmd.AddCommand(newConfigCmd())
	cmd.AddCommand(newVersionCmd())

	return cmd
}
