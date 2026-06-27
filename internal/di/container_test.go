package di_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/samber/do/v2"
	"github.com/samber/oops"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/omarluq/anamnesis/internal/di"
)

const validConfigDocument = "app:\n  name: ana\n  env: test\nlogging:\n  level: info\n  format: json\n"

// writeValidConfig writes a valid config document to a temp file and returns its path.
func writeValidConfig(t *testing.T) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "config.yaml")
	require.NoError(t, os.WriteFile(path, []byte(validConfigDocument), 0o600))

	return path
}

func TestNewContainerResolvesPublicServices(t *testing.T) {
	t.Parallel()

	container, err := di.NewContainer(writeValidConfig(t))

	require.NoError(t, err)
	require.NotNil(t, container)

	cfgService := di.MustInvoke[*di.ConfigService](container)

	require.NotNil(t, cfgService)
	assert.Same(t, cfgService, di.MustInvoke[*di.ConfigService](container))

	cfg := cfgService.Get()

	require.NotNil(t, cfg)
	assert.Equal(t, "ana", cfg.App.Name)
	require.NoError(t, cfg.Validate())

	logService := di.MustInvoke[*di.LoggerService](container)

	require.NotNil(t, logService)
	assert.NotNil(t, logService.SlogLogger)
}

func TestNewContainerErrorsOnInvalidConfig(t *testing.T) {
	t.Parallel()

	missing := filepath.Join(t.TempDir(), "missing.yaml")

	container, err := di.NewContainer(missing)

	require.Error(t, err)
	assert.Nil(t, container)

	var oopsErr oops.OopsError

	require.ErrorAs(t, err, &oopsErr)
	assert.ErrorContains(t, err, "initialize container")
}

func TestContainerShutdownWithContextSucceeds(t *testing.T) {
	t.Parallel()

	container, err := di.NewContainer(writeValidConfig(t))
	require.NoError(t, err)

	report := container.ShutdownWithContext(context.Background())

	require.NotNil(t, report)
	assert.True(t, report.Succeed)
	assert.Empty(t, report.Errors)
}

func TestRegisterServicesAllowsExternalResolution(t *testing.T) {
	t.Parallel()

	injector := do.New()
	do.ProvideNamedValue(injector, di.ConfigPathKey, writeValidConfig(t))
	di.RegisterServices(injector)

	cfgService, err := do.Invoke[*di.ConfigService](injector)

	require.NoError(t, err)
	assert.NotNil(t, cfgService)

	logService, err := do.Invoke[*di.LoggerService](injector)

	require.NoError(t, err)
	assert.NotNil(t, logService)
}
