package rlm

import (
	"fmt"
	"strings"

	"github.com/samber/lo"
	"github.com/samber/mo"
)

// nilLiteral is the placeholder rendered for an empty retval or error field.
const nilLiteral = "nil"

// ControllerTurn records one controller turn as the loop observed it: the code
// the controller emitted plus the captured stdout, return value, and error from
// evaluating that code. These are the only fields the controller sees again on a
// later turn (see SPEC §6).
type ControllerTurn struct {
	// Code is the Go source the controller produced for this turn.
	Code string
	// Stdout is what the evaluated code explicitly printed.
	Stdout string
	// Retval is the host-package return summary, or empty when there is none.
	Retval string
	// Err is the evaluation error text, or empty when the turn succeeded.
	Err string
	// Index is the zero-based turn number used in the rendered labels.
	Index int
}

// Render formats history into the §6 transcript the controller is shown on a
// later turn: one ASSISTANT line and one TOOL OUTPUT block per turn, blocks
// separated by a blank line.
func Render(history []ControllerTurn) string {
	blocks := lo.Map(history, func(turn ControllerTurn, _ int) string {
		return renderTurn(turn)
	})

	return strings.Join(blocks, "\n")
}

// renderTurn renders the ASSISTANT and TOOL OUTPUT block for a single turn,
// quoting the controller code and stdout so multi-line values stay on a single
// line, and substituting nilLiteral for an absent retval or error.
func renderTurn(turn ControllerTurn) string {
	retval := mo.EmptyableToOption(turn.Retval).OrElse(nilLiteral)
	failure := mo.EmptyableToOption(turn.Err).OrElse(nilLiteral)

	return fmt.Sprintf(
		"ASSISTANT (turn %d): %q\nTOOL OUTPUT (turn %d):\n  stdout: %q\n  retval: %s\n  error:  %s\n",
		turn.Index, turn.Code, turn.Index, turn.Stdout, retval, failure,
	)
}
