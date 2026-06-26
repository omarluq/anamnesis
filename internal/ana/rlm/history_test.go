package rlm_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/omarluq/anamnesis/internal/ana/rlm"
)

func TestRender(t *testing.T) {
	t.Parallel()

	t.Run("two_turn_golden", func(t *testing.T) {
		t.Parallel()

		history := []rlm.ControllerTurn{
			{
				Index:  0,
				Code:   "boots := journal.Boots()",
				Stdout: "",
				Retval: "",
				Err:    "",
			},
			{
				Index:  1,
				Code:   "fmt.Println(len(boots))",
				Stdout: "3\n",
				Retval: "",
				Err:    "",
			},
		}

		want := `ASSISTANT (turn 0): "boots := journal.Boots()"
TOOL OUTPUT (turn 0):
  stdout: ""
  retval: nil
  error:  nil

ASSISTANT (turn 1): "fmt.Println(len(boots))"
TOOL OUTPUT (turn 1):
  stdout: "3\n"
  retval: nil
  error:  nil
`

		assert.Equal(t, want, rlm.Render(history))
	})

	t.Run("retval_and_error_render_raw", func(t *testing.T) {
		t.Parallel()

		history := []rlm.ControllerTurn{
			{
				Index:  2,
				Code:   "agent.FINAL(answer)",
				Stdout: "done\n",
				Retval: "42",
				Err:    "boom: it failed",
			},
		}

		want := `ASSISTANT (turn 2): "agent.FINAL(answer)"
TOOL OUTPUT (turn 2):
  stdout: "done\n"
  retval: 42
  error:  boom: it failed
`

		got := rlm.Render(history)
		assert.Equal(t, want, got)
		assert.Contains(t, got, "  retval: 42\n")
		assert.Contains(t, got, "  error:  boom: it failed\n")
		assert.NotContains(t, got, "retval: nil")
	})

	t.Run("empty_history_is_empty", func(t *testing.T) {
		t.Parallel()

		assert.Empty(t, rlm.Render(nil))
	})
}

func TestRenderMultilineCode(t *testing.T) {
	t.Parallel()

	history := []rlm.ControllerTurn{
		{
			Index:  3,
			Code:   "x := journal.Boots()\nfmt.Println(len(x))",
			Stdout: "",
			Retval: "",
			Err:    "",
		},
	}

	want := `ASSISTANT (turn 3): "x := journal.Boots()\nfmt.Println(len(x))"
TOOL OUTPUT (turn 3):
  stdout: ""
  retval: nil
  error:  nil
`

	got := rlm.Render(history)
	assert.Equal(t, want, got)
	// The embedded newline must be escaped, not emitted as a raw break that
	// would spill the controller code across multiple transcript lines.
	assert.NotContains(t, got, "ASSISTANT (turn 3): x := journal.Boots()\n")
}
