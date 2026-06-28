package repl

import (
	"reflect"

	"github.com/mvm-sh/mvm/stdlib"
)

// safeStdlibPackages is the explicit allow-list of Go standard-library import
// paths the interpreter exposes to controller-generated source. It is the entire
// security boundary of the embedded VM: NewInterpreter imports ONLY these
// packages from mvm's stdlib bridge, so interpreted source can reach fmt and its
// peers but NOT os, os/exec, syscall, net, net/http, runtime, unsafe, plugin, or
// any other host-effect package. Those bridge bindings are wired to real host
// functions, so without this allow-list the controller's raw Go could read and
// write the operator's filesystem, spawn processes, or open sockets — an
// arbitrary-RCE surface, not the "read-only by construction" the host packages
// promise.
//
// Filtering the bridge is both necessary and sufficient. The dangerous packages
// exist only as native bridge bindings (mvm's stdlib.Values), so dropping them
// from the import makes them unresolvable — interpreted source cannot name them
// even with an explicit import. mvm's fallback source filesystem (the embedded
// std) serves only a handful of pure-Go packages (cmp, slices, maps, iter, log,
// path, errors, testing/quick); none can reach a host effect on its own, and any
// that transitively import an unbridged package (e.g. log -> os) simply fail to
// compile. So the source filesystem cannot be a backdoor and needs no separate
// gate.
//
// Every entry is a pure computation, text, or data package with no filesystem,
// process, network, reflection, or memory-unsafe surface. The set mirrors what
// the controller system prompt advertises (fmt, strings, strconv, time, sort,
// encoding/json) plus a few safe helpers an investigation may reach for (math,
// regexp, errors, bytes, unicode, unicode/utf8).
//
// It deliberately EXCLUDES slices, maps, and cmp. mvm has no native bridge for
// those generics-first packages; it loads them from embedded source, and that
// source imports the unsafe pseudo-package — a sandbox-escape primitive
// (unsafe.Pointer/Add/Slice) that is itself on the deny list. Admitting slices or
// maps would therefore mean re-exposing unsafe, so they are left out. No
// controller path needs them: the prompt never advertises them and all
// slices/maps use in this codebase is ordinary host Go compiled by the real
// toolchain, never interpreted.
//
// Widening this list widens the controller's blast radius. Justify every
// addition as host-effect-free, and never add a package mvm resolves from source
// without confirming its transitive imports stay inside this set.
var safeStdlibPackages = map[string]struct{}{
	"fmt":           {},
	"strings":       {},
	"strconv":       {},
	"sort":          {},
	"time":          {},
	"math":          {},
	"regexp":        {},
	"errors":        {},
	"bytes":         {},
	"unicode":       {},
	"unicode/utf8":  {},
	"encoding/json": {},
}

// allowedStdlibValues returns mvm's stdlib bridge filtered down to
// safeStdlibPackages: the per-package symbol tables NewInterpreter hands to
// engine.ImportPackageValues. Filtering here — rather than importing the whole
// stdlib.Values map — is the single enforcement point for the allow-list, so a
// package absent from safeStdlibPackages is never bound into the engine and
// cannot be named by interpreted source. Iterating the allow-list (not the
// bridge) keeps the result an exact intersection and fails closed: an entry mvm
// does not provide is silently skipped rather than widening the set.
func allowedStdlibValues() map[string]map[string]reflect.Value {
	allowed := make(map[string]map[string]reflect.Value, len(safeStdlibPackages))
	for path := range safeStdlibPackages {
		if pkg, ok := stdlib.Values[path]; ok {
			allowed[path] = pkg
		}
	}

	return allowed
}
