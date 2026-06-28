package config

import (
	"testing"

	"github.com/spf13/viper"
	"github.com/stretchr/testify/assert"
)

// TestSetDefaults is a white-box test: it reaches the unexported setDefaults
// directly to assert the baseline viper values backing config.Load.
func TestSetDefaults(t *testing.T) {
	t.Parallel()

	viperInstance := viper.New()
	setDefaults(viperInstance)

	defaults := []struct {
		key  string
		want string
	}{
		{key: "app.name", want: "ana"},
		{key: "app.env", want: "development"},
		{key: "logging.level", want: "info"},
		{key: "logging.format", want: "pretty"},
		{key: "reasoning.controller", want: effortMedium},
		{key: "reasoning.sub", want: effortLow},
	}
	for _, testCase := range defaults {
		assert.Equal(t, testCase.want, viperInstance.GetString(testCase.key), testCase.key)
	}
}
