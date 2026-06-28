package rlm

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"reflect"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/samber/mo"
	"github.com/samber/oops"

	"github.com/omarluq/anamnesis/internal/ana/repl"
	"github.com/omarluq/anamnesis/internal/openai"
)

// maxHistoryFieldBytes caps how many bytes of a single turn's captured stdout or
// rendered return value re-enter the controller's transcript. The defining RLM
// property is that only a bounded view of output re-enters context; without this
// cap a turn ending on a bare large-slice expression would render the whole
// []Entry back into history and reintroduce the context rot the loop exists to
// prevent. The bound is structural here, not left to the §14 prompt's policy.
const maxHistoryFieldBytes = 4096

// forceFinishHeader prefixes the force-finish answer the loop returns when a run ends
// before the controller signals agent.FINAL — the user canceled it or a turn's eval
// wedged and timed out. It carries no findings — the note under it does — so it stays
// honest about the run ending without a grounded conclusion.
const forceFinishHeader = "investigation incomplete"

// forceFinishReason names why the §6 loop settled: finishFinal when the model
// resolved a terminal answer through agent.FINAL, finishContinue when the loop may
// take another turn, or one of the force-finish causes. It is recorded on the
// controller and surfaced in the run's observability log so a force-finish is told
// apart from a clean finish, and its cause kept distinct.
type forceFinishReason string

const (
	finishContinue    forceFinishReason = ""
	finishFinal       forceFinishReason = "final"
	finishCtxCanceled forceFinishReason = "ctx_canceled"
	finishEvalTimeout forceFinishReason = "eval_timeout"
)

// EvalCapture is the interpreter seam the controller loop drives each turn:
// EvalContext runs the turn's generated Go against the persistent REPL session
// under a timeout and captures what it printed, and Final resolves the terminal
// answer once that code has signaled completion. The rlm package owns this
// interface; *repl.Interpreter satisfies it structurally, so the loop depends on a
// narrow contract rather than on the concrete interpreter.
type EvalCapture interface {
	// EvalContext runs src under the label name against the persistent session
	// state, guarded by an idle-progress watchdog whose window is timeout and by ctx,
	// returning the captured stdout, the final expression's value, and any evaluation
	// error. A turn that makes no progress within its idle window surfaces as an
	// eval_timed_out error carrying the partial stdout printed before it wedged; the
	// interpreter is then poisoned and must not be reused, so the loop force-finishes
	// on that fault.
	EvalContext(ctx context.Context, timeout time.Duration, name, src string) (repl.Result, error)
	// Final resolves the terminal answer once controller code has called
	// agent.FINAL or agent.FINAL_VAR: the recorded literal for FINAL, or the
	// current value of the named REPL variable for FINAL_VAR. It returns false
	// when no terminal primitive has run yet.
	Final() (string, bool)
}

// Compile-time assertion that the real interpreter satisfies the loop's seam.
var _ EvalCapture = (*repl.Interpreter)(nil)

// Controller drives the §6 turn loop for one investigation. Each turn it asks the
// controller model for the next ControllerResponse, evaluates the generated Go
// through the interpreter, records the turn into the session history, and emits a
// trace event; once the model reports Done, Run resolves the answer the controller
// signaled through agent.FINAL or agent.FINAL_VAR, enforcing the §7/§10 citation
// grounding gate before returning it. It is built from one Session and one
// EvalCapture with NewController; the zero value is not usable because both
// collaborators must be wired first.
type Controller struct {
	// session carries the controller model, history, prompts, and trace emitter
	// the loop frames every turn with.
	session *Session
	// eval runs each turn's generated Go and resolves the terminal answer.
	eval EvalCapture
	// finished records how the loop settled — finishFinal for a model-signaled
	// agent.FINAL, or the force-finish cause — so the run-end observability summary
	// can tell a clean finish from a force-finish. It is set as the loop returns.
	finished forceFinishReason
}

// NewController binds session and eval into the controller spine the turn loop
// runs on. The returned Controller drives session.Controller once per turn,
// evaluates each turn's code through eval, appends to session.History, and emits
// onto session.Emitter.
func NewController(session *Session, eval EvalCapture) *Controller {
	return &Controller{session: session, eval: eval, finished: finishContinue}
}

// Run executes the controller turn loop until the model reports Done, returning
// the resolved final answer. The loop is unbounded by time or turn count: it runs
// until the model signals agent.FINAL, the caller cancels the context (the user
// quits), or a turn's eval wedges and times out — the last two return the
// force-finish answer, a short honest note that it ended without a grounded
// conclusion. Each non-final turn
// evaluates the model's generated Go — recovering an over-budget agent.Query panic
// onto the turn's Err field rather than unwinding the loop — appends a
// ControllerTurn to the session history, and emits a turn trace event; the final
// turn resolves the answer the controller signaled through agent.FINAL (a literal)
// or agent.FINAL_VAR (the interpreter-resolved value of a named REPL variable),
// enforcing the §7/§10 citation grounding gate before returning it so a fabricated
// citation fails the run. It returns an error when a controller call fails, when the
// model reports Done without a resolvable answer, or when a cited cursor was never
// made visible this session. A turn whose eval times out — a non-terminating loop
// the interpreter cannot preempt — force-finishes the loop too: its abandoned
// goroutine poisons the interpreter, so the loop force-finishes with that honest
// note rather than taking another turn against a poisoned session.
func (controller *Controller) Run(ctx context.Context) (string, error) {
	for {
		select {
		case <-ctx.Done():
			return controller.forceFinish(finishCtxCanceled), nil
		default:
		}

		response, err := controller.respond(ctx)
		if err != nil {
			return "", err
		}

		if response.Done {
			return controller.resolveDone(ctx, response)
		}

		if controller.recordTurn(ctx, response) {
			return controller.forceFinish(finishEvalTimeout), nil
		}
	}
}

// FinishReason reports how the loop settled — "final" for a model-signaled
// agent.FINAL, or the force-finish cause — for the run-end observability summary. It
// is meaningful only after Run has returned.
func (controller *Controller) FinishReason() string {
	return string(controller.finished)
}

// resolveDone settles a turn the model marked Done: it records the loop as final,
// evaluates the turn's code when it carries any — some models inline the agent.FINAL
// call on the Done turn rather than the prior turn — and resolves the terminal
// answer. A clean Done turn carries no code, so the eval is skipped and resolve reads
// the answer a prior turn's agent.FINAL recorded. It returns the force-finish note
// when that inline eval times out.
func (controller *Controller) resolveDone(ctx context.Context, response openai.ControllerResponse) (string, error) {
	controller.finished = finishFinal

	if strings.TrimSpace(response.Code) != "" {
		if controller.recordTurn(ctx, response) {
			return controller.forceFinish(finishEvalTimeout), nil
		}
	}

	return controller.resolve()
}

// validateCitations enforces the §7/§10 grounding guarantee before the answer can
// render: it rejects the run when the controller cited a cursor no journal query
// returned this session, wrapping the store's verdict as an oops error in the rlm
// domain so a fabricated citation surfaces as a controller fault.
func (controller *Controller) validateCitations() error {
	if err := controller.session.Store.Validate(); err != nil {
		return oops.
			In("rlm").
			Code("controller_citation_invalid").
			Wrapf(err, "validate final citations")
	}

	return nil
}

// forceFinish assembles the §6 force-finish answer the loop renders when it stops for
// reason before the controller signals agent.FINAL: a short, honest Markdown note that
// the run ended without a grounded conclusion. It deliberately does NOT replay the
// controller's per-turn stdout — folding that raw journald output back into the answer
// is the §14 rule-1 dump the RLM loop exists to prevent, and replaying it is what made
// a force-finished run render as a wall of intermediate tool output instead of a
// report. The only detail it carries is the bounded, truthful count of turns the
// investigation spent. It records reason on the controller and logs it so the run-end
// summary can tell the cause — a user cancel or an eval timeout — apart.
func (controller *Controller) forceFinish(reason forceFinishReason) string {
	controller.finished = reason

	slog.Warn(
		"rlm force-finish",
		slog.String("reason", string(reason)),
		slog.Int("turns", len(controller.session.History)),
	)

	return forceFinishHeader + "\n\n" + controller.forceFinishNote()
}

// forceFinishNote is the one-line body under the force-finish header: the bounded,
// truthful fact of how far the investigation got. With no recorded turns the run ended
// before it could gather anything; otherwise it ran that many turns without ever
// calling agent.FINAL.
func (controller *Controller) forceFinishNote() string {
	turns := len(controller.session.History)
	if turns == 0 {
		return "The investigation was canceled before it ran a single turn, so there " +
			"is nothing to report."
	}

	return fmt.Sprintf(
		"The investigation stopped after %d turn(s) without the controller calling "+
			"agent.FINAL, so no grounded conclusion is available.",
		turns,
	)
}

// respond renders the transcript so far and asks the controller model for the
// next turn, wrapping a model-call failure as an oops error tagged with the rlm
// domain.
func (controller *Controller) respond(ctx context.Context) (openai.ControllerResponse, error) {
	history := Render(controller.session.History)

	response, err := controller.session.Controller.Respond(
		ctx,
		controller.session.SystemPrompt,
		controller.session.Question,
		history,
		func(delta string) { controller.session.Emitter.ThinkingDelta(delta) },
	)
	if err != nil {
		return openai.ControllerResponse{Thinking: "", Code: "", Done: false}, oops.
			In("rlm").
			Code("controller_respond_failed").
			Wrapf(err, "controller turn request")
	}

	return response, nil
}

// recordTurn streams the turn's reasoning and code to the transcript, evaluates the
// turn's generated Go under the per-eval timeout, and appends the resulting
// ControllerTurn to the session history, reporting whether the eval timed out — the
// one fault the loop cannot continue past. It emits in execution order — the
// thinking, then the code block opening, then (after evaluation) the code block's
// captured output — so a turn that fans out via agent.Query shows its sub-call
// blocks nested between the code's start and its settled output. The turn index is
// the current history length, so it stays unique and monotonic as the loop appends
// each turn. An ordinary evaluation fault — including a recovered over-budget
// agent.Query panic — is recorded on the turn and tolerated, so the controller sees
// the error on its next turn and can recover; an eval_timed_out is different: it
// abandoned a goroutine that poisons the interpreter, so recordTurn reports true and
// the loop force-finishes rather than reusing the poisoned session.
func (controller *Controller) recordTurn(ctx context.Context, response openai.ControllerResponse) bool {
	index := len(controller.session.History)
	label := fmt.Sprintf("turn_%d", index)

	controller.session.Emitter.Thinking(thinkingTrace(response))

	hasCode := strings.TrimSpace(response.Code) != ""

	var (
		result  repl.Result
		evalErr error
	)

	// A Done turn carries no code (ControllerResponse.Code is empty), so skip the
	// interpreter entirely rather than relying on Eval("") staying a benign no-op —
	// evaluating nothing could otherwise record a spurious eval failure on the turn.
	if hasCode {
		controller.session.Emitter.CodeStart(response.Code)

		result, evalErr = controller.evalTurn(ctx, label, response.Code)
	}

	controller.session.History = append(
		controller.session.History,
		newControllerTurn(index, response, result, evalErr),
	)

	if hasCode {
		controller.session.Emitter.CodeEnd(codeOutput(result), renderErr(evalErr))
	}

	slog.InfoContext(
		ctx,
		"rlm turn",
		slog.Int("turn", index),
		slog.Bool("has_code", hasCode),
		slog.String("eval_err", renderErr(evalErr)),
	)

	return evalTimedOut(evalErr)
}

// codeOutput renders a turn's evaluation result for the transcript's code block:
// the captured stdout followed by the final expression's value. The evaluation
// error is NOT folded in — it travels as CodeEnd's separate errText so a failed turn
// renders as a red block. Empty sections are omitted so a silent turn renders an
// empty block rather than blank-line noise.
func codeOutput(result repl.Result) string {
	sections := make([]string, 0, 2)

	if stdout := strings.TrimRight(result.Stdout, "\n"); stdout != "" {
		sections = append(sections, stdout)
	}

	if retval := renderRetval(result.Retval); retval != "" {
		sections = append(sections, retval)
	}

	return strings.Join(sections, "\n")
}

// thinkingTrace picks the text the turn's thinking trace renders: the model's
// reasoning summary when the Responses API returned one — it reads as fuller prose
// than the schema's terse Thinking field — falling back to that brief Thinking
// field when no summary was produced.
func thinkingTrace(response openai.ControllerResponse) string {
	return mo.EmptyableToOption(response.Reasoning).OrElse(response.Thinking)
}

// evalTurn runs the turn's generated Go through the interpreter under the §6 per-eval
// idle watchdog, recovering an over-budget agent.Query panic (SPEC §6) into an error so
// a saturated sub-call or recursion-depth budget surfaces on the turn's Err field
// rather than unwinding the whole loop. A turn that idles out comes back from
// EvalContext as the eval_timed_out fault, which evalTimedOut tells apart from this
// recoverable panic.
func (controller *Controller) evalTurn(ctx context.Context, label, code string) (result repl.Result, err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			err = oops.
				In("rlm").
				Code("controller_eval_panicked").
				Errorf("evaluate turn %s: %v", label, recovered)
		}
	}()

	return controller.eval.EvalContext(ctx, controller.session.Budget.PerEvalTimeout, label, code)
}

// evalTimedOut reports whether err is the interpreter's eval_timed_out signal — the
// per-eval timeout fired on a turn whose generated Go did not return. It is the one
// eval fault the loop force-finishes on rather than recovering from: the timed-out
// goroutine was abandoned and still poisons the interpreter, unlike the recoverable
// controller_eval_panicked an over-budget agent.Query raises.
func evalTimedOut(err error) bool {
	if err == nil {
		return false
	}

	var oopsErr oops.OopsError
	if errors.As(err, &oopsErr) {
		return oopsErr.Code() == repl.CodeEvalTimedOut
	}

	return false
}

// resolve reads the terminal answer the interpreter holds once the model reports
// Done and enforces the §7/§10 citation grounding gate before returning it: a run
// that cited a cursor no journal query made visible this session fails here rather
// than rendering a fabricated answer. It returns an oops error when no terminal
// primitive resolved an answer, or when validateCitations rejects the citations.
func (controller *Controller) resolve() (string, error) {
	answer, ok := controller.eval.Final()
	if !ok {
		return "", oops.
			In("rlm").
			Code("controller_missing_final").
			Errorf("controller reported done without a terminal answer")
	}

	if err := controller.validateCitations(); err != nil {
		return "", err
	}

	return answer, nil
}

// newControllerTurn assembles the §6 history record for one evaluated turn from
// the model response and the interpreter result, summarizing the return value and
// any evaluation error as the short strings the controller sees on a later turn.
func newControllerTurn(
	index int,
	response openai.ControllerResponse,
	result repl.Result,
	evalErr error,
) ControllerTurn {
	return ControllerTurn{
		Code:   response.Code,
		Stdout: capForHistory(result.Stdout),
		Retval: capForHistory(renderRetval(result.Retval)),
		Err:    capForHistory(renderErr(evalErr)),
		Index:  index,
	}
}

// capForHistory truncates text to maxHistoryFieldBytes on a UTF-8 rune boundary,
// appending an elision marker that records how many bytes were dropped, so an
// oversized stdout, return value, or error message cannot grow the controller's
// context without bound. Text already within the cap is returned unchanged.
func capForHistory(text string) string {
	if len(text) <= maxHistoryFieldBytes {
		return text
	}

	cut := maxHistoryFieldBytes
	for cut > 0 && !utf8.RuneStart(text[cut]) {
		cut--
	}

	return text[:cut] + fmt.Sprintf("\n…[%d bytes elided to bound controller context]", len(text)-cut)
}

// renderRetval summarizes a turn's final expression value as a string, returning
// the empty string when the value is absent or cannot be read so the history
// renders it as nil.
func renderRetval(value reflect.Value) string {
	if !value.IsValid() || !value.CanInterface() {
		return ""
	}

	return fmt.Sprint(value.Interface())
}

// renderErr renders an evaluation error as its message, or the empty string when
// the turn succeeded.
func renderErr(evalErr error) string {
	if evalErr == nil {
		return ""
	}

	return evalErr.Error()
}
