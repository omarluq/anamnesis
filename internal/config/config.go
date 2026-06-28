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
	// validReasoningEfforts is the case-insensitive set of reasoning-effort tiers a
	// model role may be configured with, ordered cheapest-to-costliest. It mirrors
	// the OpenAI Responses API effort enum that openai.ParseEffort maps these names
	// onto; Validate compares the lowercased configured value against this set.
	validReasoningEfforts = []string{"none", "minimal", "low", "medium", "high", "xhigh"}
)

// Config is the fully resolved application configuration.
type Config struct {
	App     AppConfig     `json:"app" mapstructure:"app" yaml:"app"`
	Logging LoggingConfig `json:"logging" mapstructure:"logging" yaml:"logging"`
	// Reasoning carries the per-role reasoning-effort knobs. It is always populated
	// by Load (setDefaults seeds every role), so it is exhaustruct-optional: the
	// only literals that omit it are tests that exercise an unrelated field.
	Reasoning ReasoningConfig `json:"reasoning" mapstructure:"reasoning" yaml:"reasoning" exhaustruct:"optional"`
}

// ReasoningConfig tunes how hard each model role reasons. SPEC §6/§14-16 run three
// distinct gpt-5.5 roles — the controller turn loop, the bounded sub-LLM calls, and
// the post-FINAL audit judge — and each pays its reasoning cost in turn latency, so
// each is a separate knob. Higher tiers reason harder at the cost of slower turns;
// the defaults (controller/judge medium, sub low) favor responsiveness over the
// maximum-effort setting that made every turn take minutes. Each value is one of
// validReasoningEfforts, case-insensitive, resolved to the SDK enum by
// openai.ParseEffort.
type ReasoningConfig struct {
	Controller string `json:"controller" mapstructure:"controller" yaml:"controller"`
	Sub        string `json:"sub" mapstructure:"sub" yaml:"sub"`
	Judge      string `json:"judge" mapstructure:"judge" yaml:"judge"`
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
	// File is the destination log file path. An empty value resolves to the XDG
	// state default ($XDG_STATE_HOME/ana/ana.log) at logger construction.
	File string `json:"file" mapstructure:"file" yaml:"file"`
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

	return c.validateReasoning()
}

// validateReasoning checks every reasoning-effort knob names a known tier. The
// comparison lowercases the configured value so case is irrelevant ("Medium" and
// "MEDIUM" both pass), matching openai.ParseEffort, and an unknown tier surfaces as
// an oops error in the config domain naming the offending field.
func (c *Config) validateReasoning() error {
	roles := []struct {
		field string
		value string
	}{
		{field: "reasoning.controller", value: c.Reasoning.Controller},
		{field: "reasoning.sub", value: c.Reasoning.Sub},
		{field: "reasoning.judge", value: c.Reasoning.Judge},
	}
	for _, role := range roles {
		if !lo.Contains(validReasoningEfforts, strings.ToLower(role.value)) {
			return oops.In("config").Code("invalid_reasoning_effort").
				Errorf("%s must be one of %s", role.field, strings.Join(validReasoningEfforts, ", "))
		}
	}

	return nil
}
