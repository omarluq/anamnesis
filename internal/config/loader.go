package config

import (
	"errors"
	"strings"

	"github.com/samber/mo"
	"github.com/samber/oops"
	"github.com/spf13/viper"
)

// Load resolves configuration from defaults, environment variables, and an optional file.
func Load(path string) mo.Result[*Config] {
	viperInstance := viper.New()
	setDefaults(viperInstance)

	viperInstance.SetEnvPrefix("ANAMNESIS")
	viperInstance.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))
	viperInstance.AutomaticEnv()

	if path != "" {
		viperInstance.SetConfigFile(path)
	} else {
		viperInstance.SetConfigName("config")
		viperInstance.SetConfigType("yaml")
		viperInstance.AddConfigPath(".")
		viperInstance.AddConfigPath("$HOME/.config/ana")
	}

	if err := viperInstance.ReadInConfig(); err != nil {
		var notFoundErr viper.ConfigFileNotFoundError
		if !errors.As(err, &notFoundErr) || path != "" {
			return mo.Err[*Config](oops.In("config").Code("read_failed").Wrapf(err, "read config file"))
		}
	}

	var cfg Config
	if err := viperInstance.Unmarshal(&cfg); err != nil {
		return mo.Err[*Config](oops.In("config").Code("unmarshal_failed").Wrapf(err, "unmarshal config"))
	}

	return mo.TupleToResult(&cfg, cfg.Validate())
}

func setDefaults(viperInstance *viper.Viper) {
	viperInstance.SetDefault("app.name", "ana")
	viperInstance.SetDefault("app.env", envDevelopment)
	viperInstance.SetDefault("logging.level", "info")
	viperInstance.SetDefault("logging.format", "pretty")
	// Reasoning defaults favor turn latency over maximum effort: the controller
	// reasons at medium, the high-volume sub-calls at low. Each is overridable per
	// role via config or the ANAMNESIS_REASONING_* env vars.
	viperInstance.SetDefault("reasoning.controller", effortMedium)
	viperInstance.SetDefault("reasoning.sub", effortLow)
}
