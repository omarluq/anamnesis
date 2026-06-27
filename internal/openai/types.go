package openai

import (
	"reflect"
	"strings"

	"github.com/samber/lo"
)

// Model is the OpenAI model id every role runs on — controller, sub-LLM, and
// judge. It is a raw string because the SDK ships no constant for gpt-5.5. There
// is deliberately no fallback: if the supplied key lacks gpt-5.5 access the run
// fails loudly rather than silently downgrading to a weaker model.
const Model = "gpt-5.5"

// JSON Schema type names GenerateSchema emits for the Go scalar kinds it maps.
const (
	jsonTypeObject  = "object"
	jsonTypeString  = "string"
	jsonTypeBoolean = "boolean"
	jsonTypeInteger = "integer"
	jsonTypeNumber  = "number"
)

// JSON Schema document keys GenerateSchema writes.
const (
	schemaKeyType                 = "type"
	schemaKeyProperties           = "properties"
	schemaKeyRequired             = "required"
	schemaKeyAdditionalProperties = "additionalProperties"
	schemaKeyDescription          = "description"
)

// descriptionPrefix is the jsonschema struct-tag prefix that carries a field's
// human-readable description, e.g. `jsonschema:"description=..."`.
const descriptionPrefix = "description="

// jsonTypeByKind maps the Go reflect kinds GenerateSchema understands to their
// JSON Schema type names. Kinds absent from the map fall back to a string type.
var jsonTypeByKind = map[reflect.Kind]string{
	reflect.Bool:    jsonTypeBoolean,
	reflect.String:  jsonTypeString,
	reflect.Int:     jsonTypeInteger,
	reflect.Int8:    jsonTypeInteger,
	reflect.Int16:   jsonTypeInteger,
	reflect.Int32:   jsonTypeInteger,
	reflect.Int64:   jsonTypeInteger,
	reflect.Uint:    jsonTypeInteger,
	reflect.Uint8:   jsonTypeInteger,
	reflect.Uint16:  jsonTypeInteger,
	reflect.Uint32:  jsonTypeInteger,
	reflect.Uint64:  jsonTypeInteger,
	reflect.Float32: jsonTypeNumber,
	reflect.Float64: jsonTypeNumber,
}

// ControllerResponse is the structured reply the RLM controller model returns on
// every turn. The controller emits it as JSON constrained by GenerateSchema so
// the loop can read the next code block and detect termination without parsing
// free-form text.
type ControllerResponse struct {
	// Thinking is the controller's brief rationale for the next step.
	Thinking string `json:"thinking" jsonschema:"description=Brief reasoning for what to do next"`
	// Code is the Go source to evaluate this turn, empty when Done is true.
	Code string `json:"code" jsonschema:"description=Go source to evaluate, or empty if Done"`
	// Done is true once a prior turn called agent.FINAL and the answer is ready.
	Done bool `json:"done" jsonschema:"description=True iff agent.FINAL was called in a prior turn"`
}

// GenerateSchema reflects the struct type T into a JSON Schema document for the
// OpenAI Responses API structured-output contract: it marks every field required
// and sets additionalProperties to false, the strict subset the Responses API
// enforces. T must be a struct type.
func GenerateSchema[T any]() map[string]any {
	return schemaForType(reflect.TypeFor[T]())
}

// schemaForType builds the object schema for a struct type, one property per
// JSON-visible field, with every property required and additionalProperties off.
func schemaForType(typ reflect.Type) map[string]any {
	fieldCount := typ.NumField()
	properties := make(map[string]any, fieldCount)
	required := make([]string, 0, fieldCount)

	for index := range fieldCount {
		field := typ.Field(index)

		name := jsonFieldName(field.Tag.Get("json"), field.Name)
		if name == "" {
			continue
		}

		properties[name] = propertySchema(field.Type.Kind(), field.Tag.Get("jsonschema"))
		required = append(required, name)
	}

	return map[string]any{
		schemaKeyType:                 jsonTypeObject,
		schemaKeyProperties:           properties,
		schemaKeyRequired:             required,
		schemaKeyAdditionalProperties: false,
	}
}

// jsonFieldName resolves the schema property name from a field's json tag,
// falling back to goName when the tag is absent and returning "" when the field
// is excluded with a "-" tag.
func jsonFieldName(jsonTag, goName string) string {
	name, _, _ := strings.Cut(jsonTag, ",")

	if name == "-" {
		return ""
	}

	if name == "" {
		return goName
	}

	return name
}

// propertySchema builds the schema fragment for one field from its kind and its
// jsonschema struct tag, attaching a description when the tag supplies one.
func propertySchema(kind reflect.Kind, schemaTag string) map[string]any {
	schema := map[string]any{schemaKeyType: jsonType(kind)}

	if desc := tagDescription(schemaTag); desc != "" {
		schema[schemaKeyDescription] = desc
	}

	return schema
}

// tagDescription extracts the description= value from a jsonschema struct tag, or
// "" when the tag carries no description.
func tagDescription(schemaTag string) string {
	for part := range strings.SplitSeq(schemaTag, ",") {
		if desc, found := strings.CutPrefix(part, descriptionPrefix); found {
			return desc
		}
	}

	return ""
}

// jsonType maps a Go reflect kind to its JSON Schema type name, defaulting to a
// string type for kinds the schema does not model.
func jsonType(kind reflect.Kind) string {
	return lo.ValueOr(jsonTypeByKind, kind, jsonTypeString)
}
