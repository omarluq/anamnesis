// Package di wires the application runtime dependency graph.
package di

import (
	"github.com/samber/do/v2"
	"github.com/samber/oops"
)

// Container wraps the root injector used by the CLI runtime.
type Container struct {
	injector *do.RootScope
}

// NewContainer builds the root injector for the CLI runtime.
func NewContainer(configPath string) (*Container, error) {
	injector := do.New()
	do.ProvideNamedValue(injector, ConfigPathKey, configPath)
	RegisterServices(injector)

	if _, err := do.Invoke[*ConfigService](injector); err != nil {
		return nil, oops.
			In("di").
			Code("container_init").
			Wrapf(err, "initialize container")
	}

	// Resolve the logger eagerly so slog.SetDefault runs at startup and logging
	// is installed to its file before any service emits a record.
	if _, err := do.Invoke[*LoggerService](injector); err != nil {
		return nil, oops.
			In("di").
			Code("container_init").
			Wrapf(err, "initialize logging")
	}

	return &Container{injector: injector}, nil
}

// MustInvoke resolves a dependency and panics if it cannot be created.
func MustInvoke[T any](c *Container) T {
	return do.MustInvoke[T](c.injector)
}
