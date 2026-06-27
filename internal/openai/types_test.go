package openai_test

import (
	"testing"

	"github.com/samber/oops"
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

	schema, err := openai.GenerateSchema[openai.ControllerResponse]()
	require.NoError(t, err)

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

// schemaFieldsSample exercises field selection: the exported field becomes a
// schema property while the unexported field must be dropped because
// encoding/json never marshals it.
type schemaFieldsSample struct {
	Visible string `json:"visible"`
	hidden  bool
}

// schemaUnsupportedSample carries a map field, a kind GenerateSchema maps to no
// JSON Schema scalar, so schema generation must fail loudly instead of coercing.
type schemaUnsupportedSample struct {
	Tags map[string]string `json:"tags"`
}

func TestGenerateSchemaSkipsUnexportedFields(t *testing.T) {
	t.Parallel()

	// Read the unexported field so the unused linter treats it as used; the
	// generated schema must still omit it because encoding/json drops it.
	_ = schemaFieldsSample{Visible: "", hidden: true}.hidden

	schema, err := openai.GenerateSchema[schemaFieldsSample]()
	require.NoError(t, err)

	properties, propertiesOK := schema[schemaPropertiesKey].(map[string]any)
	require.True(t, propertiesOK, "properties is a map[string]any")
	assert.Contains(t, properties, "visible")
	assert.NotContains(t, properties, "hidden", "unexported fields are excluded from properties")

	required, requiredOK := schema[schemaRequiredKey].([]string)
	require.True(t, requiredOK, "required is a []string")
	assert.Equal(t, []string{"visible"}, required)
	assert.NotContains(t, required, "hidden", "unexported fields are excluded from required")
}

func TestGenerateSchemaRejectsUnsupportedKind(t *testing.T) {
	t.Parallel()

	schema, err := openai.GenerateSchema[schemaUnsupportedSample]()

	require.Error(t, err)
	assert.Nil(t, schema, "no schema is returned when a field kind is unsupported")

	oopsErr, ok := oops.AsOops(err)
	require.True(t, ok, "error is oops-wrapped")
	assert.Equal(t, "openai", oopsErr.Domain())
	assert.ErrorContains(t, err, "tags", "the error names the offending field")
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
