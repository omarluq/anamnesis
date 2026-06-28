package rlm

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"unicode/utf8"

	"github.com/samber/lo"
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

// forceFinishHeader prefixes the §6 force-finish answer the loop returns when it
// exhausts the turn budget or the session's wall-clock deadline before the
// controller signals agent.FINAL.
const forceFinishHeader = "investigation incomplete, partial findings"

// critiqueHeader labels the judge's critique where the loop appends it to the
// controller transcript on a §5 retry turn, so the controller can tell the audit's
// revision directive apart from its own turn output.
const critiqueHeader = "JUDGE CRITIQUE (revise the final answer and call agent.FINAL again): "

// EvalCapture is the interpreter seam the controller loop drives each turn: Eval
// runs the turn's generated Go against the persistent REPL session and captures
// what it printed, and Final resolves the terminal answer once that code has
// signaled completion. The rlm package owns this interface; *repl.Interpreter
// satisfies it structurally, so the loop depends on a narrow contract rather than
// on the concrete interpreter.
type EvalCapture interface {
	// Eval runs src under the label name against the persistent session state,
	// returning the captured stdout, the final expression's value, and any
	// evaluation error.
	Eval(name, src string) (repl.Result, error)
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
// signaled through agent.FINAL or agent.FINAL_VAR. RunAudited layers the §5
// post-FINAL judge gate on top — rejecting a fabricated citation and asking the
// judge to review, with one revision retry. It is built from one Session and one
// EvalCapture with NewController; the zero value is not usable because both
// collaborators must be wired first.
type Controller struct {
	// session carries the controller model, history, prompts, and trace emitter
	// the loop frames every turn with.
	session *Session
	// eval runs each turn's generated Go and resolves the terminal answer.
	eval EvalCapture
	// critique holds the judge's critique once the audit has spent its single §5
	// retry: the loop surfaces it to the controller on the retry turn, and its
	// presence marks the retry as spent so a second critique renders the answer.
	critique string
}

// NewController binds session and eval into the controller spine the turn loop
// runs on. The returned Controller drives session.Controller once per turn,
// evaluates each turn's code through eval, appends to session.History, and emits
// onto session.Emitter.
func NewController(session *Session, eval EvalCapture) *Controller {
	return &Controller{session: session, eval: eval, critique: ""}
}

// Run executes the controller turn loop until the model reports Done, returning
// the resolved final answer. Before every turn it consults the §6 hard budget:
// when the caller has canceled the context — the wall-time backstop — or the
// MaxTurns budget is spent, it stops and returns the force-finish answer assembled
// from the partial findings printed so far. Otherwise each non-final turn
// evaluates the model's generated Go — recovering an over-budget agent.Query panic
// onto the turn's Err field rather than unwinding the loop — appends a
// ControllerTurn to the session history, and emits a turn trace event; the final
// turn resolves the answer the controller signaled through agent.FINAL (a literal)
// or agent.FINAL_VAR (the interpreter-resolved value of a named REPL variable). It
// returns an error when a controller call fails or when the model reports Done
// without a resolvable answer. Run drives the §6 loop only; RunAudited layers the
// §5 judge gate on top.
func (controller *Controller) Run(ctx context.Context) (string, error) {
	for {
		if controller.reserveTurnOrFinish(ctx) {
			return controller.forceFinish(), nil
		}

		response, err := controller.respond(ctx)
		if err != nil {
			return "", err
		}

		if response.Done {
			return controller.resolve()
		}

		controller.recordTurn(response)
	}
}

// RunAudited runs one investigation under the §5 judge gate: it drives the §6 turn
// loop to a resolved answer, rejects a fabricated citation, and asks the §16 judge
// to review. An approving judge — or a critique that arrives after the single retry
// is already spent — renders the answer, while a first critique is recorded for the
// controller and the loop runs once more so the controller can revise against it,
// seeing that critique surfaced in its framed history. The shared session budget
// caps the revision, so the gate settles after at most one extra pass. When the §6
// wall-time backstop has already canceled the context, Run returns its force-finish
// answer and the gate is skipped: auditing on the dead context would only fail the
// judge call with a deadline error, so a timeout renders the partial answer rather
// than turning into an error. It returns an error when the loop fails, a cited cursor
// was never made visible, or the judge call fails.
func (controller *Controller) RunAudited(ctx context.Context) (string, error) {
	for {
		answer, err := controller.Run(ctx)
		if err != nil {
			return "", err
		}

		select {
		case <-ctx.Done():
			return answer, nil
		default:
		}

		settled, err := controller.reviewed(ctx, answer)
		if err != nil {
			return "", err
		}

		if settled {
			return answer, nil
		}
	}
}

// reviewed runs the post-FINAL §5 gate over a resolved answer and reports whether
// the run is settled. It rejects a fabricated citation, asks the §16 judge to
// review, and decides: an approving judge — or a critique arriving after the single
// retry is already spent — settles the run so the answer renders, while a first
// critique is recorded so the next loop surfaces it and leaves the run unsettled. It
// returns an oops error when a citation is fabricated or the judge call fails.
func (controller *Controller) reviewed(ctx context.Context, answer string) (bool, error) {
	if err := controller.validateCitations(); err != nil {
		return false, err
	}

	critique, err := controller.judge(ctx, answer)
	if err != nil {
		return false, err
	}

	if critique == "" || controller.retrySpent() {
		return true, nil
	}

	controller.recordCritique(critique)

	return false, nil
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

// judge runs the one-shot §16 audit pass over the resolved answer, framing the
// original question and the rendered investigation transcript as the grounding the
// judge weighs its verdict against. It returns the judge's critique — empty on
// approval — wrapping a judge-call failure as an oops error in the rlm domain.
func (controller *Controller) judge(ctx context.Context, answer string) (string, error) {
	critique, err := controller.session.Judge.Judge(
		ctx,
		controller.session.Question,
		answer,
		Render(controller.session.History),
	)
	if err != nil {
		return "", oops.
			In("rlm").
			Code("controller_judge_failed").
			Wrapf(err, "judge final answer")
	}

	return critique, nil
}

// recordCritique stores the judge's critique so the next controller turn sees it as
// a revision directive in its framed history, and — because a non-empty critique
// marks the single §5 retry as spent — guarantees a later critique renders the
// answer instead of looping again.
func (controller *Controller) recordCritique(critique string) {
	controller.critique = critique
}

// retrySpent reports whether the audit has already issued its single §5 judge
// retry, which it tracks by the presence of a recorded critique.
func (controller *Controller) retrySpent() bool {
	return controller.critique != ""
}

// reserveTurnOrFinish reserves the next controller turn and reports whether the
// loop must force-finish instead of taking it: the caller has canceled the
// context — the §6 wall-time backstop — or the controller has spent its MaxTurns
// budget. The turn reservation is a deliberate side effect — consuming the turn
// here is what makes a later call observe the exhaustion — so the name reads as a
// reservation, not a pure predicate, and the loop calls it exactly once per pass.
func (controller *Controller) reserveTurnOrFinish(ctx context.Context) bool {
	if ctx.Err() != nil {
		return true
	}

	return controller.session.Budget.ReserveTurn() != nil
}

// forceFinish assembles the §6 force-finish answer: the standing header on its own
// when nothing was gathered yet, or the header followed by the partial findings the
// controller printed across its recorded turns.
func (controller *Controller) forceFinish() string {
	return mo.EmptyableToOption(controller.partialFindings()).
		Map(func(findings string) (string, bool) {
			return forceFinishHeader + ": " + findings, true
		}).
		OrElse(forceFinishHeader)
}

// partialFindings joins the non-empty stdout the controller printed across its
// recorded turns into the single summary the force-finish answer carries.
func (controller *Controller) partialFindings() string {
	printed := lo.FilterMap(controller.session.History, func(turn ControllerTurn, _ int) (string, bool) {
		return turn.Stdout, turn.Stdout != ""
	})

	return strings.Join(printed, "; ")
}

// respond renders the framed history so far and asks the controller model for the
// next turn, wrapping a model-call failure as an oops error tagged with the rlm
// domain.
func (controller *Controller) respond(ctx context.Context) (openai.ControllerResponse, error) {
	history := controller.framedHistory()

	response, err := controller.session.Controller.Respond(
		ctx,
		controller.session.SystemPrompt,
		controller.session.Question,
		history,
	)
	if err != nil {
		return openai.ControllerResponse{Thinking: "", Code: "", Done: false}, oops.
			In("rlm").
			Code("controller_respond_failed").
			Wrapf(err, "controller turn request")
	}

	return response, nil
}

// framedHistory renders the §6 transcript the controller is shown for its next
// turn: the recorded turns, followed by the judge's critique as a distinct revision
// directive once the audit has spent its single retry, so the controller revises
// the answer against that feedback rather than re-deriving it blind. With no
// recorded critique it is exactly the rendered history.
func (controller *Controller) framedHistory() string {
	sections := lo.Filter(
		[]string{Render(controller.session.History), controller.critiqueDirective()},
		func(section string, _ int) bool { return section != "" },
	)

	return strings.Join(sections, "\n")
}

// critiqueDirective renders the recorded judge critique as the labeled revision
// directive the controller sees on its retry turn, or the empty string when the
// judge has not critiqued so framedHistory can omit it cleanly.
func (controller *Controller) critiqueDirective() string {
	if controller.critique == "" {
		return ""
	}

	return critiqueHeader + controller.critique
}

// recordTurn evaluates the turn's generated Go, appends the resulting
// ControllerTurn to the session history, and emits the turn's reasoning as a trace
// event. The turn index is the current history length, so it stays unique and
// monotonic even when RunAudited re-enters Run for a §5 revision pass against a
// history that already holds the earlier turns. An evaluation fault — including a
// recovered over-budget agent.Query panic — is recorded on the turn rather than
// aborting the loop, so the controller sees the error on its next turn and can
// recover.
func (controller *Controller) recordTurn(response openai.ControllerResponse) {
	index := len(controller.session.History)
	label := fmt.Sprintf("turn_%d", index)
	result, evalErr := controller.evalTurn(label, response.Code)

	controller.session.History = append(
		controller.session.History,
		newControllerTurn(index, response, result, evalErr),
	)

	controller.session.Emitter.Turn(response.Thinking)
}

// evalTurn runs the turn's generated Go through the interpreter, recovering an
// over-budget agent.Query panic (SPEC §6) into an error so a saturated sub-call or
// recursion-depth budget surfaces on the turn's Err field rather than unwinding the
// whole loop.
func (controller *Controller) evalTurn(label, code string) (result repl.Result, err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			err = oops.
				In("rlm").
				Code("controller_eval_panicked").
				Errorf("evaluate turn %s: %v", label, recovered)
		}
	}()

	return controller.eval.Eval(label, code)
}

// resolve reads the terminal answer the interpreter holds once the model reports
// Done, returning an oops error when no terminal primitive resolved an answer.
func (controller *Controller) resolve() (string, error) {
	answer, ok := controller.eval.Final()
	if !ok {
		return "", oops.
			In("rlm").
			Code("controller_missing_final").
			Errorf("controller reported done without a terminal answer")
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
		Err:    renderErr(evalErr),
		Index:  index,
	}
}

// capForHistory truncates text to maxHistoryFieldBytes on a UTF-8 rune boundary,
// appending an elision marker that records how many bytes were dropped, so an
// oversized stdout or return value cannot grow the controller's context without
// bound. Text already within the cap is returned unchanged.
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
