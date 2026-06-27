package openai_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/omarluq/anamnesis/internal/openai"
)

// schema document keys mirrored from the openai package's JSON Schema output.
const (
	schemaTypeKey       = "type"
	schemaPropertiesKey = "properties"
	schemaRequiredKey   = "required"
	schemaAdditionalKey = "additionalProperties"
)

func TestModelConstant(t *testing.T) {
	t.Parallel()

	assert.Equal(t, "gpt-5.5", openai.Model)
}

func TestGenerateSchemaControllerResponse(t *testing.T) {
	t.Parallel()

	schema := openai.GenerateSchema[openai.ControllerResponse]()

	t.Run("is a strict object", func(t *testing.T) {
		t.Parallel()

		assert.Equal(t, "object", schema[schemaTypeKey])

		additional, ok := schema[schemaAdditionalKey].(bool)
		require.True(t, ok, "additionalProperties is a bool")
		assert.False(t, additional, "additionalProperties is false for the strict subset")
	})

	t.Run("requires every field", func(t *testing.T) {
		t.Parallel()

		required, ok := schema[schemaRequiredKey].([]string)
		require.True(t, ok, "required is a []string")
		assert.ElementsMatch(t, []string{"thinking", "code", "done"}, required)
	})

	t.Run("types each property", func(t *testing.T) {
		t.Parallel()

		properties, ok := schema[schemaPropertiesKey].(map[string]any)
		require.True(t, ok, "properties is a map[string]any")
		require.Contains(t, properties, "thinking")
		require.Contains(t, properties, "code")
		require.Contains(t, properties, "done")

		assert.Equal(t, "string", propertyType(t, properties, "thinking"))
		assert.Equal(t, "string", propertyType(t, properties, "code"))
		assert.Equal(t, "boolean", propertyType(t, properties, "done"))
	})
}

// propertyType returns the JSON Schema "type" of the named property, failing the
// test when the property or its type is missing or malformed.
func propertyType(t *testing.T, properties map[string]any, name string) string {
	t.Helper()

	property, ok := properties[name].(map[string]any)
	require.True(t, ok, "property %q is a map[string]any", name)

	typ, ok := property[schemaTypeKey].(string)
	require.True(t, ok, "property %q has a string type", name)

	return typ
}
