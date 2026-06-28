package tui_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/omarluq/anamnesis/internal/tui"
)

func TestSliceViewport(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		items  []int
		want   []int
		offset int
		height int
	}{
		"height larger than list returns full slice": {
			items:  []int{1, 2, 3},
			offset: 1,
			height: 9,
			want:   []int{1, 2, 3},
		},
		"zero height returns empty slice": {
			items:  []int{1, 2},
			offset: 0,
			height: 0,
			want:   []int{},
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			require.Equal(t, tt.want, tui.SliceViewport(tt.items, tt.offset, tt.height))
		})
	}
}
