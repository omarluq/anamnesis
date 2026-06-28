package di

import (
	"context"
	"io"
	"log/slog"
	"path/filepath"
	"testing"

	"github.com/rs/zerolog"
	"github.com/samber/do/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/omarluq/anamnesis/internal/config"
)

func TestLevelParsing(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name        string
		input       string
		wantZerolog zerolog.Level
		wantSlog    slog.Level
	}{
		{name: "debug level", input: levelDebug, wantZerolog: zerolog.DebugLevel, wantSlog: slog.LevelDebug},
		{name: "warn level", input: levelWarn, wantZerolog: zerolog.WarnLevel, wantSlog: slog.LevelWarn},
		{name: "error level", input: levelError, wantZerolog: zerolog.ErrorLevel, wantSlog: slog.LevelError},
		{name: "info level", input: levelInfo, wantZerolog: zerolog.InfoLevel, wantSlog: slog.LevelInfo},
		{name: "unknown falls back to info", input: "trace", wantZerolog: zerolog.InfoLevel, wantSlog: slog.LevelInfo},
		{name: "empty falls back to info", input: "", wantZerolog: zerolog.InfoLevel, wantSlog: slog.LevelInfo},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, testCase.wantZerolog, parseZerologLevel(testCase.input))
			assert.Equal(t, testCase.wantSlog, slogLevel(testCase.input))
		})
	}
}

func TestNewZerologLogger(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		level  string
		format string
		want   zerolog.Level
	}{
		{name: "json format honors level", level: levelError, format: formatJSON, want: zerolog.ErrorLevel},
		{name: "pretty format honors level", level: levelWarn, format: "pretty", want: zerolog.WarnLevel},
		{name: "unknown level defaults to info", level: "trace", format: formatJSON, want: zerolog.InfoLevel},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			cfg := &config.Config{
				App:     config.AppConfig{Name: "test", Env: "test"},
				Logging: config.LoggingConfig{Level: testCase.level, Format: testCase.format, File: ""},
			}

			assert.Equal(t, testCase.want, newZerologLogger(cfg, io.Discard).GetLevel())
		})
	}
}

func TestNewLoggerServiceResolvesLoggers(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name         string
		config       string
		probe        slog.Level
		zerologLevel zerolog.Level
		enabled      bool
	}{
		{
			name:         "debug config enables debug",
			config:       configYAML(levelDebug, formatJSON),
			probe:        slog.LevelDebug,
			zerologLevel: zerolog.DebugLevel,
			enabled:      true,
		},
		{
			name:         "error config disables info",
			config:       configYAML(levelError, "pretty"),
			probe:        slog.LevelInfo,
			zerologLevel: zerolog.ErrorLevel,
			enabled:      false,
		},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			logFile := filepath.Join(t.TempDir(), "ana.log")
			document := testCase.config + "  file: " + logFile + "\n"
			injector := newTestInjector(t, writeConfigFile(t, document))

			service, err := do.Invoke[*LoggerService](injector)

			require.NoError(t, err)
			require.NotNil(t, service)
			require.NotNil(t, service.SlogLogger)
			assert.Equal(t, testCase.zerologLevel, service.ZerologLogger.GetLevel())
			assert.Equal(t, testCase.enabled, service.SlogLogger.Enabled(context.Background(), testCase.probe))
		})
	}
}
