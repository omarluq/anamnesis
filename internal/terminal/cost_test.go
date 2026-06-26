package terminal_test

import (
	"context"
	"testing"

	"github.com/gdamore/tcell/v3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/omarluq/anamnesis/internal/terminal"
)

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

			snapshot := terminal.CostProbe([3]int64{0, 0, testCase.micros})
			assert.Equal(t, testCase.want, snapshot.Dollars)
		})
	}
}

func TestCostApplyUsageAccumulates(t *testing.T) {
	t.Parallel()

	snapshot := terminal.CostProbe(
		[3]int64{100, 50, 1_000_000},
		[3]int64{20, 5, 500_000},
	)

	assert.Equal(t, 120, snapshot.TokensIn)
	assert.Equal(t, 55, snapshot.TokensOut)
	assert.Equal(t, "$1.5000", snapshot.Dollars)
}

func TestCostRowsReflectTotals(t *testing.T) {
	t.Parallel()

	rows := terminal.CostProbe([3]int64{100, 50, 1_500_000}).Rows
	require.Len(t, rows, 4)

	assert.Equal(t, []string{"Tokens In", "100"}, rows[0])
	assert.Equal(t, []string{"Tokens Out", "50"}, rows[1])
	assert.Equal(t, []string{"Total", "150"}, rows[2])
	assert.Equal(t, []string{"Cost", "$1.5000"}, rows[3])
}

func TestCostPaneDrawRendersMetricTable(t *testing.T) {
	t.Parallel()

	screen := newFakeScreen(80, 24)
	traceCh := make(chan terminal.TraceEvent)

	app := terminal.NewApp(screen, terminal.RunOptions{Trace: traceCh, Title: terminal.DefaultTitle})

	done := make(chan error, 1)
	go func() { done <- app.Loop(context.Background()) }()

	// Distinct in/out counts so each value anchors to its own row: a routing
	// regression that swapped the columns would move "40" and "60" and fail.
	sendTrace(t, traceCh, traceEvent(terminal.TraceKindUsage, "spend", 40, 60, 1_500_000, 0))
	screen.inject(tcell.NewEventKey(tcell.KeyCtrlC, "", tcell.ModNone))
	require.NoError(t, awaitLoop(t, done))

	text := screen.contents()
	assert.Contains(t, text, "Metric")
	assert.Contains(t, text, "Value")
	assert.Contains(t, text, "Tokens In")
	assert.Contains(t, screenRow(t, text, "Tokens In"), "40")
	assert.Contains(t, screenRow(t, text, "Tokens Out"), "60")
	assert.Contains(t, screenRow(t, text, "Total"), "100")
	assert.Contains(t, screenRow(t, text, "Cost"), "$1.5000")
}
