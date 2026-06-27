package terminal

import (
	"context"
	"testing"

	"github.com/gdamore/tcell/v3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// costSnapshot reports a cost pane's state after applying usage deltas.
type costSnapshot struct {
	Dollars   string
	Rows      [][]string
	TokensIn  int
	TokensOut int
}

// costProbe accumulates the (tokensIn, tokensOut, costMicros) deltas into a fresh
// cost pane and reports its tallies, formatted dollars, and rendered row texts.
func costProbe(deltas ...[3]int64) costSnapshot {
	pane := newCostPane(DefaultTheme())
	for _, delta := range deltas {
		pane.applyUsage(int(delta[0]), int(delta[1]), delta[2])
	}

	rendered := pane.rows()
	rows := make([][]string, 0, len(rendered))

	for _, row := range rendered {
		cells := make([]string, 0, len(row))
		for _, cell := range row {
			cells = append(cells, cell.Text)
		}

		rows = append(rows, cells)
	}

	return costSnapshot{
		TokensIn:  pane.tokensIn,
		TokensOut: pane.tokensOut,
		Dollars:   pane.dollars(),
		Rows:      rows,
	}
}

func TestCostDollarsFormatsMicros(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		want   string
		micros int64
	}{
		{name: "zero", want: "$0.0000", micros: 0},
		{name: "whole and a half dollars", want: "$1.5000", micros: 1_500_000},
		{name: "rounds to four places", want: "$1.2346", micros: 1_234_560},
		{name: "sub-cent", want: "$0.0005", micros: 500},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			snapshot := costProbe([3]int64{0, 0, testCase.micros})
			assert.Equal(t, testCase.want, snapshot.Dollars)
		})
	}
}

func TestCostApplyUsageAccumulates(t *testing.T) {
	t.Parallel()

	snapshot := costProbe(
		[3]int64{100, 50, 1_000_000},
		[3]int64{20, 5, 500_000},
	)

	assert.Equal(t, 120, snapshot.TokensIn)
	assert.Equal(t, 55, snapshot.TokensOut)
	assert.Equal(t, "$1.5000", snapshot.Dollars)
}

func TestCostRowsReflectTotals(t *testing.T) {
	t.Parallel()

	rows := costProbe([3]int64{100, 50, 1_500_000}).Rows
	require.Len(t, rows, 4)

	assert.Equal(t, []string{labelTokensIn, "100"}, rows[0])
	assert.Equal(t, []string{labelTokensOut, "50"}, rows[1])
	assert.Equal(t, []string{labelTotal, "150"}, rows[2])
	assert.Equal(t, []string{labelCost, "$1.5000"}, rows[3])
}

func TestCostRowsFormatLargeTokenCountsWithSeparators(t *testing.T) {
	t.Parallel()

	rows := costProbe([3]int64{1_234_000, 567, 0}).Rows
	require.Len(t, rows, 4)

	assert.Equal(t, []string{labelTokensIn, "1,234,000"}, rows[0])
	assert.Equal(t, []string{labelTokensOut, "567"}, rows[1])
	assert.Equal(t, []string{labelTotal, "1,234,567"}, rows[2])
}

func TestCostPaneDrawRendersMetricTable(t *testing.T) {
	t.Parallel()

	screen := newFakeScreen(80, 24)
	traceCh := make(chan TraceEvent)

	app := newApp(screen, RunOptions{Trace: traceCh, Controller: nil, Title: defaultTitle})

	done := make(chan error, 1)
	go func() { done <- app.loop(context.Background()) }()

	// Distinct in/out counts so each value anchors to its own row: a routing
	// regression that swapped the columns would move "40" and "60" and fail.
	sendTrace(t, traceCh, traceEvent(TraceKindUsage, "spend", 40, 60, 1_500_000, 0))
	screen.inject(tcell.NewEventKey(tcell.KeyCtrlC, "", tcell.ModNone))
	require.NoError(t, awaitLoop(t, done))

	text := screen.contents()
	assert.Contains(t, text, "Metric")
	assert.Contains(t, text, "Value")
	assert.Contains(t, text, labelTokensIn)
	assert.Contains(t, screenRow(t, text, labelTokensIn), "40")
	assert.Contains(t, screenRow(t, text, labelTokensOut), "60")
	assert.Contains(t, screenRow(t, text, labelTotal), "100")
	assert.Contains(t, screenRow(t, text, labelCost), "$1.5000")
}
