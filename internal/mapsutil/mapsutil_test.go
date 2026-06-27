package mapsutil_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/omarluq/anamnesis/internal/mapsutil"
)

const testMapKey = "key"

func TestCloneOrEmpty(t *testing.T) {
	t.Parallel()

	t.Run("nil input yields non-nil empty map", func(t *testing.T) {
		t.Parallel()

		empty := mapsutil.CloneOrEmpty(map[string]string(nil))
		require.NotNil(t, empty)
		assert.Empty(t, empty)
	})

	t.Run("clone is independent of original", func(t *testing.T) {
		t.Parallel()

		original := map[string]string{testMapKey: "value"}
		cloned := mapsutil.CloneOrEmpty(original)
		require.NotNil(t, cloned)

		cloned[testMapKey] = "changed"
		assert.Equal(t, "value", original[testMapKey])
	})
}

func TestClonePreserveNil(t *testing.T) {
	t.Parallel()

	t.Run("nil input stays nil", func(t *testing.T) {
		t.Parallel()

		assert.Nil(t, mapsutil.ClonePreserveNil(map[string]int(nil)))
	})

	t.Run("empty input yields non-nil empty map", func(t *testing.T) {
		t.Parallel()

		empty := mapsutil.ClonePreserveNil(map[string]int{})
		require.NotNil(t, empty)
		assert.Empty(t, empty)
	})

	t.Run("clone is independent of original", func(t *testing.T) {
		t.Parallel()

		original := map[string]int{testMapKey: 1}
		cloned := mapsutil.ClonePreserveNil(original)
		require.NotNil(t, cloned)

		cloned[testMapKey] = 2
		assert.Equal(t, 1, original[testMapKey])
	})
}

func TestCloneOrNil(t *testing.T) {
	t.Parallel()

	t.Run("nil input stays nil", func(t *testing.T) {
		t.Parallel()

		assert.Nil(t, mapsutil.CloneOrNil(map[string]int(nil)))
	})

	t.Run("empty input collapses to nil", func(t *testing.T) {
		t.Parallel()

		assert.Nil(t, mapsutil.CloneOrNil(map[string]int{}))
	})

	t.Run("clone is independent of original", func(t *testing.T) {
		t.Parallel()

		original := map[string]int{testMapKey: 1}
		cloned := mapsutil.CloneOrNil(original)
		require.NotNil(t, cloned)

		cloned[testMapKey] = 2
		assert.Equal(t, 1, original[testMapKey])
	})
}

func TestIntMapToAnyMap(t *testing.T) {
	t.Parallel()

	t.Run("nil input yields non-nil empty map", func(t *testing.T) {
		t.Parallel()

		result := mapsutil.IntMapToAnyMap(map[string]int(nil))
		require.NotNil(t, result)
		assert.Empty(t, result)
	})

	t.Run("empty input yields non-nil empty map", func(t *testing.T) {
		t.Parallel()

		result := mapsutil.IntMapToAnyMap(map[string]int{})
		require.NotNil(t, result)
		assert.Empty(t, result)
	})

	t.Run("clone is independent and values widen to any", func(t *testing.T) {
		t.Parallel()

		original := map[string]int{"a": 1}
		cloned := mapsutil.IntMapToAnyMap(original)
		require.NotNil(t, cloned)

		cloned["a"] = 2

		assert.Equal(t, 1, original["a"])
		assert.Equal(t, map[string]any{"a": 2}, cloned)
	})
}
