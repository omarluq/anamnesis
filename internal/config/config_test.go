package config_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/samber/oops"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/omarluq/anamnesis/internal/config"
)

const (
	appName           = "ana"
	envDev            = "development"
	envProd           = "production"
	envTest           = "test"
	levelInfo         = "info"
	formatPretty      = "pretty"
	effortLow         = "low"
	effortMedium      = "medium"
	codeInvalidEffort = "invalid_reasoning_effort"
)

// validReasoning is a reasoning block every passing Validate case can reuse so each
// case exercises one field at a time, mirroring the loader defaults.
var validReasoning = config.ReasoningConfig{Controller: effortMedium, Sub: effortLow, Judge: effortMedium}

// configValidateCase is one Validate table row: a Config built from the field
// values plus the error substring and oops code Validate is expected to produce
// (both empty when the config is valid).
type configValidateCase struct {
	name      string
	appName   string
	env       string
	level     string
	format    string
	reasoning config.ReasoningConfig
	errMsg    string
	wantCode  string
}

// configValidateCases lists the Validate table out of line so TestConfigValidate
// stays within the function-length budget while still covering every validated
// field, including the three per-role reasoning knobs.
func configValidateCases() []configValidateCase {
	return []configValidateCase{
		{
			name: "valid", appName: appName, env: envDev,
			level: levelInfo, format: formatPretty, reasoning: validReasoning,
			errMsg: "", wantCode: "",
		},
		{
			name: "mixed-case reasoning", appName: appName, env: envDev,
			level: levelInfo, format: formatPretty,
			reasoning: config.ReasoningConfig{Controller: "MEDIUM", Sub: "Low", Judge: "XHigh"},
			errMsg:    "", wantCode: "",
		},
		{
			name: "missing name", appName: "", env: envDev,
			level: levelInfo, format: formatPretty, reasoning: validReasoning,
			errMsg: "app.name", wantCode: "missing_app_name",
		},
		{
			name: "bad env", appName: appName, env: "staging",
			level: levelInfo, format: formatPretty, reasoning: validReasoning,
			errMsg: "app.env", wantCode: "invalid_app_env",
		},
		{
			name: "bad level", appName: appName, env: envProd,
			level: "trace", format: "json", reasoning: validReasoning,
			errMsg: "logging.level", wantCode: "invalid_logging_level",
		},
		{
			name: "bad format", appName: appName, env: envTest,
			level: "warn", format: "xml", reasoning: validReasoning,
			errMsg: "logging.format", wantCode: "invalid_logging_format",
		},
		{
			name: "bad reasoning controller", appName: appName, env: envDev,
			level: levelInfo, format: formatPretty,
			reasoning: config.ReasoningConfig{Controller: "extreme", Sub: effortLow, Judge: effortMedium},
			errMsg:    "reasoning.controller", wantCode: codeInvalidEffort,
		},
		{
			name: "bad reasoning sub", appName: appName, env: envDev,
			level: levelInfo, format: formatPretty,
			reasoning: config.ReasoningConfig{Controller: effortMedium, Sub: "fast", Judge: effortMedium},
			errMsg:    "reasoning.sub", wantCode: codeInvalidEffort,
		},
		{
			name: "bad reasoning judge", appName: appName, env: envDev,
			level: levelInfo, format: formatPretty,
			reasoning: config.ReasoningConfig{Controller: effortMedium, Sub: effortLow, Judge: "ultra"},
			errMsg:    "reasoning.judge", wantCode: codeInvalidEffort,
		},
	}
}

func writeConfig(t *testing.T, content string) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "config.yaml")
	require.NoError(t, os.WriteFile(path, []byte(content), 0o600))

	return path
}

func TestConfigValidate(t *testing.T) {
	t.Parallel()

	for _, testCase := range configValidateCases() {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			cfg := config.Config{
				App:       config.AppConfig{Name: testCase.appName, Env: testCase.env},
				Logging:   config.LoggingConfig{Level: testCase.level, Format: testCase.format, File: ""},
				Reasoning: testCase.reasoning,
			}

			err := cfg.Validate()
			if testCase.errMsg == "" {
				require.NoError(t, err)

				return
			}

			require.ErrorContains(t, err, testCase.errMsg)

			oopsErr, ok := oops.AsOops(err)

			require.True(t, ok, "error is oops-wrapped")
			assert.Equal(t, "config", oopsErr.Domain())
			assert.Equal(t, testCase.wantCode, oopsErr.Code())
		})
	}
}

func TestLoadValidFile(t *testing.T) {
	t.Parallel()

	const content = `app:
  name: testapp
  env: production
logging:
  level: warn
  format: json
reasoning:
  controller: high
  sub: minimal
  judge: xhigh
`

	cfg, err := config.Load(writeConfig(t, content)).Get()
	require.NoError(t, err)
	assert.Equal(t, config.Config{
		App:       config.AppConfig{Name: "testapp", Env: envProd},
		Logging:   config.LoggingConfig{Level: "warn", Format: "json", File: ""},
		Reasoning: config.ReasoningConfig{Controller: "high", Sub: "minimal", Judge: "xhigh"},
	}, *cfg)
}

func TestLoadAppliesDefaults(t *testing.T) {
	t.Parallel()

	const content = "app:\n  name: customapp\n"

	cfg, err := config.Load(writeConfig(t, content)).Get()
	require.NoError(t, err)
	assert.Equal(t, config.Config{
		App:       config.AppConfig{Name: "customapp", Env: envDev},
		Logging:   config.LoggingConfig{Level: levelInfo, Format: formatPretty, File: ""},
		Reasoning: config.ReasoningConfig{Controller: effortMedium, Sub: effortLow, Judge: effortMedium},
	}, *cfg)
}

func TestLoadErrors(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name     string
		content  string
		errMsg   string
		wantCode string
		write    bool
	}{
		{
			name: "malformed yaml", content: "app: [1, 2",
			errMsg: "read config file", wantCode: "read_failed", write: true,
		},
		{
			name: "invalid values", content: "app:\n  name: ana\n  env: staging\n",
			errMsg: "app.env", wantCode: "invalid_app_env", write: true,
		},
		{name: "missing file", content: "", errMsg: "read config file", wantCode: "read_failed", write: false},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			path := filepath.Join(t.TempDir(), "config.yaml")

			if testCase.write {
				require.NoError(t, os.WriteFile(path, []byte(testCase.content), 0o600))
			}

			result := config.Load(path)
			require.True(t, result.IsError())

			err := result.Error()
			require.ErrorContains(t, err, testCase.errMsg)

			oopsErr, ok := oops.AsOops(err)

			require.True(t, ok, "error is oops-wrapped")
			assert.Equal(t, "config", oopsErr.Domain())
			assert.Equal(t, testCase.wantCode, oopsErr.Code())
		})
	}
}
