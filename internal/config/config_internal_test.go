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

	assert.Equal(t, "ana", viperInstance.GetString("app.name"))
	assert.Equal(t, "development", viperInstance.GetString("app.env"))
	assert.Equal(t, "info", viperInstance.GetString("logging.level"))
	assert.Equal(t, "pretty", viperInstance.GetString("logging.format"))
}
