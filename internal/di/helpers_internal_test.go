package di

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/samber/do/v2"
	"github.com/stretchr/testify/require"
)

// invalidEnvConfigYAML is a syntactically valid document whose app.env fails validation.
const invalidEnvConfigYAML = "app:\n  name: testapp\n  env: not-a-valid-env\n" +
	"logging:\n  level: debug\n  format: json\n"

// configYAML renders a valid configuration document for the given logging level and format.
func configYAML(level, format string) string {
	return fmt.Sprintf(
		"app:\n  name: testapp\n  env: test\nlogging:\n  level: %s\n  format: %s\n",
		level,
		format,
	)
}

// writeConfigFile writes content to a config.yaml inside a fresh temp dir and returns its path.
func writeConfigFile(t *testing.T, content string) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "config.yaml")
	require.NoError(t, os.WriteFile(path, []byte(content), 0o600))

	return path
}

// newTestInjector builds a real do injector with the package services registered.
func newTestInjector(t *testing.T, configPath string) do.Injector {
	t.Helper()

	injector := do.New()
	do.ProvideNamedValue(injector, ConfigPathKey, configPath)
	RegisterServices(injector)

	return injector
}
