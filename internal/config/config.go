// Package config loads and validates application configuration.
package config

import (
	"strings"

	"github.com/samber/lo"
	"github.com/samber/oops"
)

// Recognized application environments; envDevelopment is also the default.
const (
	envDevelopment = "development"
	envTest        = "test"
	envProduction  = "production"
)

// Allowed values per validated field, kept as the single source of truth shared
// by Validate's membership checks and the error messages it produces.
var (
	validEnvs       = []string{envDevelopment, envTest, envProduction}
	validLogLevels  = []string{"debug", "info", "warn", "error"}
	validLogFormats = []string{"pretty", "json"}
)

// Config is the fully resolved application configuration.
type Config struct {
	App     AppConfig     `json:"app" mapstructure:"app" yaml:"app"`
	Logging LoggingConfig `json:"logging" mapstructure:"logging" yaml:"logging"`
}

// AppConfig contains application identity and environment settings.
type AppConfig struct {
	Name string `json:"name" mapstructure:"name" yaml:"name"`
	Env  string `json:"env" mapstructure:"env" yaml:"env"`
}

// LoggingConfig contains runtime logging settings.
type LoggingConfig struct {
	Level  string `json:"level" mapstructure:"level" yaml:"level"`
	Format string `json:"format" mapstructure:"format" yaml:"format"`
}

// Validate ensures the configuration is internally consistent.
func (c *Config) Validate() error {
	if c.App.Name == "" {
		return oops.In("config").Code("missing_app_name").Errorf("app.name is required")
	}

	rules := []struct {
		field string
		value string
		code  string
		valid []string
	}{
		{field: "app.env", value: c.App.Env, code: "invalid_app_env", valid: validEnvs},
		{field: "logging.level", value: c.Logging.Level, code: "invalid_logging_level", valid: validLogLevels},
		{field: "logging.format", value: c.Logging.Format, code: "invalid_logging_format", valid: validLogFormats},
	}
	for _, rule := range rules {
		if !lo.Contains(rule.valid, rule.value) {
			return oops.In("config").Code(rule.code).
				Errorf("%s must be one of %s", rule.field, strings.Join(rule.valid, ", "))
		}
	}

	return nil
}

// IsDev reports whether the application is running in development mode.
func (c *Config) IsDev() bool {
	return c.App.Env == envDevelopment
}
