package tui_test

import (
	"testing"

	"github.com/gdamore/tcell/v3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/omarluq/anamnesis/internal/tui"
)

func TestRendererFlushesCombiningRunesAndSkipsUnchangedCells(t *testing.T) {
	t.Parallel()

	screen := &cellRecordingScreen{calls: nil}
	style := tcell.StyleDefault
	renderer := tui.NewRenderer(screen)
	frame := tui.NewCellBuffer(1, 1, style)
	frame.SetContent(0, 0, 'e', []rune{'\u0301'}, style)

	renderer.Flush(frame)
	renderer.Flush(frame)

	require.Len(t, screen.calls, 1)
	require.Equal(t, 'e', screen.calls[0].primary)
	require.Equal(t, []rune{'\u0301'}, screen.calls[0].combining)
}

func TestRendererFlushWritesOnlyChangedCells(t *testing.T) {
	t.Parallel()

	screen := &recordingScreen{cells: map[[2]int]rune{}}
	style := tcell.StyleDefault
	frame := tui.NewCellBuffer(2, 1, style)
	frame.SetContent(0, 0, 'a', nil, style)
	frame.SetContent(1, 0, 'x', nil, style)

	renderer := tui.NewRenderer(screen)
	renderer.Flush(frame)

	require.Equal(t, 'a', screen.cells[[2]int{0, 0}])
	require.Equal(t, 'x', screen.cells[[2]int{1, 0}])

	// Reset the recorded writes so the second flush is observed in isolation,
	// then change a single cell. The renderer must diff against the previous
	// frame and rewrite only the changed cell, skipping the unchanged one.
	screen.cells = map[[2]int]rune{}

	frame.SetContent(1, 0, 'y', nil, style)
	renderer.Flush(frame)

	require.Len(t, screen.cells, 1)
	assert.Equal(t, 'y', screen.cells[[2]int{1, 0}])
	_, rewroteUnchanged := screen.cells[[2]int{0, 0}]
	assert.False(t, rewroteUnchanged)
}

func TestRendererFlushBlanksCellsOutsideSmallerFrame(t *testing.T) {
	t.Parallel()

	screen := &recordingScreen{cells: map[[2]int]rune{}}
	style := tcell.StyleDefault
	renderer := tui.NewRenderer(screen)

	large := tui.NewCellBuffer(3, 2, style)
	large.SetContent(2, 1, 'x', nil, style)
	renderer.Flush(large)
	require.Equal(t, 'x', screen.cells[[2]int{2, 1}])

	small := tui.NewCellBuffer(2, 1, style)
	renderer.Flush(small)

	// Cells outside the smaller frame must be blanked, not left stale.
	assert.Equal(t, ' ', screen.cells[[2]int{2, 1}])
	assert.Equal(t, ' ', screen.cells[[2]int{2, 0}])
	assert.Equal(t, ' ', screen.cells[[2]int{0, 1}])
	assert.Equal(t, ' ', screen.cells[[2]int{1, 1}])
}
