// Package mapsutil provides small map-copy helpers used across internal packages.
package mapsutil

import (
	"maps"

	"github.com/samber/lo"
)

// CloneOrEmpty returns a copy of string values, or an initialized empty map for nil input.
func CloneOrEmpty[V any](values map[string]V) map[string]V {
	if values == nil {
		return map[string]V{}
	}

	return maps.Clone(values)
}

// ClonePreserveNil returns a copy of string values, or nil for nil input.
// maps.Clone already preserves nil, so this is a thin, intent-revealing alias
// that contrasts with CloneOrEmpty and CloneOrNil.
func ClonePreserveNil[V any](values map[string]V) map[string]V {
	return maps.Clone(values)
}

// CloneOrNil returns a copy of string values, or nil for nil or empty input.
func CloneOrNil[V any](values map[string]V) map[string]V {
	if len(values) == 0 {
		return nil
	}

	return maps.Clone(values)
}

// IntMapToAnyMap copies integer map values into a JSON-friendly any map.
// A nil input is normalized to a non-nil empty map, so the result is never nil.
func IntMapToAnyMap(values map[string]int) map[string]any {
	return lo.MapValues(values, func(value int, _ string) any {
		return value
	})
}
