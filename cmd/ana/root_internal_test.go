package main

import (
	"bytes"
	"os"
	"testing"

	"github.com/samber/lo"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/omarluq/anamnesis/internal/config"
	"github.com/omarluq/anamnesis/internal/vinfo"
)

const (
	keyAppName   = "app.name"
	keyAppEnv    = "app.env"
	keyLogLevel  = "logging.level"
	keyLogFormat = "logging.format"
	keyLogFile   = "logging.file"

	valAppName     = "ana"
	valAppEnv      = "production"
	valLogLevel    = "info"
	valLogFormat   = "json"
	valLogFile     = "/var/log/ana.log"
	envDevelopment = "development"

	keyReasoningController = "reasoning.controller"
	keyReasoningSub        = "reasoning.sub"

	valReasoningController = "medium"
	valReasoningSub        = "low"
)

func TestRootCmdHelpListsCommands(t *testing.T) {
	t.Parallel()

	cmd := newRootCmd()
	buf := new(bytes.Buffer)
	cmd.SetOut(buf)
	cmd.SetErr(buf)
	cmd.SetArgs([]string{"--help"})

	err := cmd.Execute()
	require.NoError(t, err)
	assert.Contains(t, buf.String(), "ana")

	registered := lo.Map(cmd.Commands(), func(c *cobra.Command, _ int) string { return c.Name() })
	expected := []string{newChatCmd().Name(), newConfigCmd().Name(), newVersionCmd().Name()}
	assert.Subset(t, registered, expected)
}

func TestVersionCmdPrintsBuildInfo(t *testing.T) {
	t.Parallel()

	cmd := newVersionCmd()
	buf := new(bytes.Buffer)
	cmd.SetOut(buf)

	err := cmd.Execute()
	require.NoError(t, err)
	assert.Equal(t, vinfo.String()+"\n", buf.String())
}

func TestResolveEnv(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		env      string
		fallback string
		expected string
	}{
		{name: "empty falls back to default", env: "", fallback: envDevelopment, expected: envDevelopment},
		{name: "explicit value takes precedence", env: valAppEnv, fallback: envDevelopment, expected: valAppEnv},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, testCase.expected, resolveEnv(testCase.env, testCase.fallback))
		})
	}
}

func TestUpperEnvKeys(t *testing.T) {
	t.Parallel()

	entries := []configEntry{
		{key: keyAppName, value: valAppName},
		{key: keyLogLevel, value: valLogLevel},
	}

	assert.Equal(t,
		[]string{"ANAMNESIS_APP_NAME", "ANAMNESIS_LOGGING_LEVEL"},
		upperEnvKeys("ANAMNESIS", entries),
	)
}

func TestConfigEntries(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{
		App:     config.AppConfig{Name: valAppName, Env: valAppEnv},
		Logging: config.LoggingConfig{Level: valLogLevel, Format: valLogFormat, File: valLogFile},
		Reasoning: config.ReasoningConfig{
			Controller: valReasoningController,
			Sub:        valReasoningSub,
		},
	}

	lookup := lo.SliceToMap(configEntries(cfg), func(e configEntry) (string, string) {
		return e.key, e.value
	})

	assert.Equal(t, map[string]string{
		keyAppName:             valAppName,
		keyAppEnv:              valAppEnv,
		keyLogLevel:            valLogLevel,
		keyLogFormat:           valLogFormat,
		keyLogFile:             valLogFile,
		keyReasoningController: valReasoningController,
		keyReasoningSub:        valReasoningSub,
	}, lookup)
}

func TestPrintLine(t *testing.T) {
	t.Parallel()

	buf := new(bytes.Buffer)

	err := printLine(buf, "value: %d", 42)
	require.NoError(t, err)
	assert.Equal(t, "value: 42\n", buf.String())
}

func TestPrintLineWrapsWriteError(t *testing.T) {
	t.Parallel()

	file, err := os.CreateTemp(t.TempDir(), "printline-*.txt")
	require.NoError(t, err)
	require.NoError(t, file.Close())

	err = printLine(file, "boom")
	require.ErrorContains(t, err, "write output")
}
