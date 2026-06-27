package di

import (
	"path/filepath"
	"testing"

	"github.com/samber/do/v2"
	"github.com/samber/oops"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewConfigServiceLoadsConfiguredFile(t *testing.T) {
	t.Parallel()

	injector := newTestInjector(t, writeConfigFile(t, configYAML(levelDebug, formatJSON)))

	service, err := do.Invoke[*ConfigService](injector)

	require.NoError(t, err)
	require.NotNil(t, service)

	cfg := service.Get()

	require.NotNil(t, cfg)
	assert.Equal(t, "testapp", cfg.App.Name)
	assert.Equal(t, "test", cfg.App.Env)
	assert.Equal(t, levelDebug, cfg.Logging.Level)
	assert.Equal(t, "json", cfg.Logging.Format)
	assert.NoError(t, cfg.Validate())
}

func TestNewConfigServiceLoadsDefaultsWithEmptyPath(t *testing.T) {
	t.Parallel()

	injector := newTestInjector(t, "")

	service, err := NewConfigService(injector)

	require.NoError(t, err)
	require.NotNil(t, service)
	assert.NoError(t, service.Get().Validate())
}

func TestNewConfigServiceRejectsBadConfig(t *testing.T) {
	t.Parallel()

	cases := []struct {
		path     func(t *testing.T) string
		name     string
		wantCode string
	}{
		{
			name:     "missing file",
			wantCode: "read_failed",
			path: func(t *testing.T) string {
				t.Helper()

				return filepath.Join(t.TempDir(), "missing.yaml")
			},
		},
		{
			name:     "invalid env value",
			wantCode: "invalid_app_env",
			path: func(t *testing.T) string {
				t.Helper()

				return writeConfigFile(t, invalidEnvConfigYAML)
			},
		},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			injector := newTestInjector(t, testCase.path(t))

			service, err := NewConfigService(injector)

			require.Error(t, err)
			assert.Nil(t, service)

			oopsErr, ok := oops.AsOops(err)

			require.True(t, ok, "error is oops-wrapped")
			// serviceError wraps an already-oops config error; oops surfaces the
			// deepest domain/code, so the underlying config failure is reported
			// while serviceError still annotates the chain with "load config".
			assert.Equal(t, "config", oopsErr.Domain())
			assert.Equal(t, testCase.wantCode, oopsErr.Code())
			assert.ErrorContains(t, err, "load config")
		})
	}
}
