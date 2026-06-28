package openai

import (
	"reflect"
	"strings"

	"github.com/samber/oops"
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
// JSON Schema type names. Kinds absent from the map are unsupported and make
// schema generation fail loudly rather than silently coercing to a string.
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
	// Reasoning is the model's reasoning summary for this turn — the prose the
	// Responses API returns when asked for reasoning.summary, which reads far better
	// than the terse Thinking field. It is populated out-of-band from the response's
	// reasoning output item, not from the model's structured JSON reply, so it is
	// excluded from the schema and from JSON (de)serialization with json:"-" and is
	// empty when the turn returned no reasoning summary. exhaustruct:"optional"
	// because it is call metadata: existing ControllerResponse literals need not set
	// it, just as they need not invent a reasoning summary the model never returned.
	Reasoning string `json:"-" exhaustruct:"optional"`
	// Done is true once a prior turn called agent.FINAL and the answer is ready.
	Done bool `json:"done" jsonschema:"description=True iff agent.FINAL was called in a prior turn"`
}

// GenerateSchema reflects the struct type T into a JSON Schema document for the
// OpenAI Responses API structured-output contract: it marks every exported
// JSON-visible field required and sets additionalProperties to false, the strict
// subset the Responses API enforces. It returns an error when a field's type maps
// to no supported JSON Schema type, so the contract never silently coerces an
// unmappable field into a string the decoder cannot read back. It also returns
// an error if T is not a struct type rather than panicking.
func GenerateSchema[T any]() (map[string]any, error) {
	return schemaForType(reflect.TypeFor[T]())
}

// schemaForType builds the object schema for a struct type, one property per
// exported JSON-visible field, with every property required and
// additionalProperties off. Unexported fields are skipped because encoding/json
// never marshals them, and embedded unexported fields fall out the same way. It
// returns an error when typ is not a struct, or when a field's type has no
// supported JSON Schema mapping.
func schemaForType(typ reflect.Type) (map[string]any, error) {
	if typ.Kind() != reflect.Struct {
		return nil, oops.
			In("openai").
			Code("non_struct_schema_type").
			Errorf("schema type %s is not a struct", typ)
	}

	fieldCount := typ.NumField()
	properties := make(map[string]any, fieldCount)
	required := make([]string, 0, fieldCount)

	for index := range fieldCount {
		field := typ.Field(index)
		if !field.IsExported() {
			continue
		}

		name := jsonFieldName(field.Tag.Get("json"), field.Name)
		if name == "" {
			continue
		}

		schema, err := propertySchema(field.Type, field.Tag.Get("jsonschema"))
		if err != nil {
			return nil, oops.
				In("openai").
				Code("unsupported_schema_field").
				Wrapf(err, "schema field %q", name)
		}

		properties[name] = schema
		required = append(required, name)
	}

	return map[string]any{
		schemaKeyType:                 jsonTypeObject,
		schemaKeyProperties:           properties,
		schemaKeyRequired:             required,
		schemaKeyAdditionalProperties: false,
	}, nil
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

// propertySchema builds the schema fragment for one field from its type and its
// jsonschema struct tag, attaching a description when the tag supplies one. It
// returns an error when the field's type maps to no supported JSON Schema type,
// so an unmappable field is rejected rather than coerced.
func propertySchema(typ reflect.Type, schemaTag string) (map[string]any, error) {
	typeName, err := jsonType(typ.Kind())
	if err != nil {
		return nil, err
	}

	schema := map[string]any{schemaKeyType: typeName}

	if desc := tagDescription(schemaTag); desc != "" {
		schema[schemaKeyDescription] = desc
	}

	return schema, nil
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

// jsonType maps a Go reflect kind to its JSON Schema type name, returning an
// error for kinds the structured-output schema does not model so the caller
// never emits a silently coerced type the decoder cannot read back.
func jsonType(kind reflect.Kind) (string, error) {
	name, ok := jsonTypeByKind[kind]
	if !ok {
		return "", oops.
			In("openai").
			Code("unsupported_schema_kind").
			Errorf("reflect kind %s has no JSON Schema mapping", kind)
	}

	return name, nil
}
