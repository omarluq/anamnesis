package tui_test

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRectHelpers(t *testing.T) {
	t.Parallel()

	require.True(t, testRect(0, 0, 1).Empty())
	require.False(t, testRect(0, 1, 1).Empty())
}
