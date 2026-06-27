package repl

import (
	"fmt"
	"go/token"
	"reflect"
	"strings"

	"github.com/mvm-sh/mvm/symbol"

	"github.com/omarluq/anamnesis/internal/ana/journal"
)

// CitationSink records the journal entries interpreted code attaches to the
// final answer through agent.Cite. The repl package owns this seam so a session
// forwards citations to the real citations.Store in production and to a test
// double under test. Calls accumulate; the sink validates the cited cursors
// against the session-visible queries later.
type CitationSink interface {
	// Cite records entries, by their cursor, as evidence for the final answer.
	Cite(entries []journal.Entry)
}

// finalKind records which terminal primitive, if any, interpreted code has
// called to end the investigation, so Interpreter.Final knows how to resolve the
// answer.
type finalKind uint8

const (
	// finalPending means no terminal primitive has been called yet.
	finalPending finalKind = iota
	// finalLiteral means agent.FINAL supplied the answer as a literal string.
	finalLiteral
	// finalVariable means agent.FINAL_VAR named a REPL variable holding the answer.
	finalVariable
)

// Agent is the §7 RLM primitive façade interpreted source drives to end a turn:
// FINAL and FINAL_VAR record the terminal answer and Cite attaches evidence to
// it. RegisterAgent exposes the three as the interpreted agent package, so the
// controller writes agent.FINAL("..."), agent.FINAL_VAR("var") and
// agent.Cite(entries). The zero value is not usable; build one with RegisterAgent
// so its citation sink is wired and it is bound to its interpreter.
type Agent struct {
	// sink receives the entries Cite attaches to the final answer.
	sink CitationSink
	// answer holds the literal answer once FINAL recorded it.
	answer string
	// varName holds the REPL variable name once FINAL_VAR recorded it.
	varName string
	// kind tracks which terminal primitive, if any, has been called.
	kind finalKind
}

// RegisterAgent exposes the agent RLM primitive surface to interpreter so
// controller source can call agent.FINAL, agent.FINAL_VAR and agent.Cite by
// name, forwarding citations to sink. It binds the returned Agent to interpreter:
// Interpreter.Final later resolves the terminal answer the Agent records, reading
// the named REPL variable for an answer assembled across turns by FINAL_VAR.
//
// Because mvm replaces a package's symbol table wholesale on import, RegisterAgent
// re-emits the Query and QueryBatched primitives a prior RegisterQuery installed
// alongside the terminal trio, so the agent and query surfaces coexist on the
// agent package regardless of the order the two are registered in.
func RegisterAgent(interpreter *Interpreter, sink CitationSink) *Agent {
	agent := &Agent{
		sink:    sink,
		answer:  "",
		varName: "",
		kind:    finalPending,
	}

	symbols := map[string]reflect.Value{
		"FINAL":     reflect.ValueOf(agent.recordFinal),
		"FINAL_VAR": reflect.ValueOf(agent.recordFinalVar),
		"Cite":      reflect.ValueOf(agent.cite),
	}

	mergeQuerySurface(interpreter, symbols)
	importSurface(interpreter, "agent", symbols)
	interpreter.agent = agent

	return agent
}

// recordFinal backs agent.FINAL: it records answer as the literal terminal
// answer. The controller calls it once it has the conclusion in hand.
func (agent *Agent) recordFinal(answer string) {
	agent.answer = answer
	agent.kind = finalLiteral
}

// recordFinalVar backs agent.FINAL_VAR: it records that the answer is the current
// value of the REPL variable named varname, resolved when Interpreter.Final runs.
// It is the terminal signal for an answer the controller assembled across turns.
func (agent *Agent) recordFinalVar(varname string) {
	agent.varName = varname
	agent.kind = finalVariable
}

// cite backs agent.Cite: it forwards entries to the citation sink, which
// accumulates them as evidence for the final answer.
func (agent *Agent) cite(entries []journal.Entry) {
	agent.sink.Cite(entries)
}

// resolve returns the terminal answer and true once a terminal primitive has
// been called, or the empty string and false otherwise. A FINAL_VAR answer is
// read back from interpreter as the current value of the named REPL variable.
func (agent *Agent) resolve(interpreter *Interpreter) (string, bool) {
	switch agent.kind {
	case finalLiteral:
		return agent.answer, true
	case finalVariable:
		return interpreter.variableString(agent.varName)
	case finalPending:
		return "", false
	default:
		return "", false
	}
}

// loadAgent returns the Agent bound to interpreter and true, or nil and false when
// none is bound. It centralizes the interpreter-owned binding read that both
// Interpreter.Final and the query surface's mergeAgentSurface rely on, so the
// nil-check lives in one place.
func loadAgent(interpreter *Interpreter) (*Agent, bool) {
	return interpreter.agent, interpreter.agent != nil
}

// Final returns the terminal answer the controller signaled through agent.FINAL
// or agent.FINAL_VAR and true, or the empty string and false when no agent is
// registered or no terminal primitive has run yet. For a FINAL_VAR signal it
// reads the current value of the named REPL variable, so an answer the
// controller built across turns resolves to that variable's final string.
func (interpreter *Interpreter) Final() (string, bool) {
	agent, ok := loadAgent(interpreter)
	if !ok {
		return "", false
	}

	return agent.resolve(interpreter)
}

// variableString resolves the REPL variable reference named name against the
// session state and renders its current value as a string. name must be a bound
// session variable or a dotted field selector rooted at one; it returns false for
// anything else — an unbound name, a faulting evaluation, or a value that cannot
// be read — so a FINAL_VAR pointed at a missing variable degrades to "no answer"
// rather than a panic.
func (interpreter *Interpreter) variableString(name string) (string, bool) {
	if !interpreter.boundVariableRef(name) {
		return "", false
	}

	value, err := interpreter.engine.Eval("agent_final_var", name)
	if err != nil || !value.IsValid() {
		return "", false
	}

	if value.Kind() == reflect.String {
		return value.String(), true
	}

	if value.CanInterface() {
		return fmt.Sprint(value.Interface()), true
	}

	return "", false
}

// boundVariableRef reports whether name is a variable reference — a bare session
// variable or a dotted field selector rooted at one — whose root identifier is
// actually bound as a global session variable. It gates variableString so
// FINAL_VAR resolves a real symbol rather than re-evaluating arbitrary source:
// every dot-separated segment must be a Go identifier, ruling out the calls and
// operators that would otherwise execute on each Final() call, and the root must
// name a bound variable, ruling out an unbound name the engine would otherwise
// resolve shell-style to its own bareword.
func (interpreter *Interpreter) boundVariableRef(name string) bool {
	segments := strings.Split(name, ".")
	for _, segment := range segments {
		if !token.IsIdentifier(segment) {
			return false
		}
	}

	sym, ok := interpreter.engine.Symbols[segments[0]]

	return ok && sym != nil && sym.Kind == symbol.Var
}
