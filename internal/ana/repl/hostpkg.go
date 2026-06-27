package repl

import (
	"reflect"

	"github.com/samber/oops"
)

// RegisterSurface exposes a host surface to interpreted source as a Go package
// named pkg. Every method declared on the Surface interface becomes a function
// the controller can call as pkg.Method(...), so a host type such as the journal
// client surfaces in the REPL as journal.Query(...). Values cross the boundary as
// native reflect.Values, including host structs and slices of them, so a method
// returning a []Entry is rangeable in interpreted code and its fields read back.
//
// Surface must be an interface type; instantiate the type parameter explicitly,
// e.g. RegisterSurface[journal.Querier](interp, "journal", client), so that only
// the interface's own methods are exported and the concrete value's other methods
// stay hidden. It returns an oops error tagged with the repl domain if Surface is
// not an interface, declares no methods, is a nil surface, or a method it declares
// is absent on surface.
func RegisterSurface[Surface any](interpreter *Interpreter, pkg string, surface Surface) error {
	surfaceType := reflect.TypeFor[Surface]()
	if surfaceType.Kind() != reflect.Interface {
		return oops.
			In("repl").
			Code("host_surface_not_interface").
			Errorf("host surface %q must resolve to an interface, got %s", pkg, surfaceType.Kind())
	}

	funcs, err := surfaceFuncs(pkg, surfaceType, reflect.ValueOf(surface))
	if err != nil {
		return err
	}

	importSurface(interpreter, pkg, funcs)

	return nil
}

// importSurface installs symbols as the interpreted package pkg and re-resolves
// the short-name auto-imports, so controller source can reference pkg.Symbol
// without an explicit import statement. It is the shared install step behind
// RegisterSurface and HostDeps.Register.
func importSurface(interpreter *Interpreter, pkg string, symbols map[string]reflect.Value) {
	interpreter.engine.ImportPackageValues(map[string]map[string]reflect.Value{pkg: symbols})
	interpreter.engine.AutoImportPackages()
}

// typeBinding returns the symbol-table entry that exposes the Go type T to
// interpreted source as a constructible package symbol. The value is a typed nil
// pointer, the form the mvm bridge reads as a type rather than a runtime value, so
// source can write pkg.T{...} composite literals and name the type in expressions.
func typeBinding[T any]() reflect.Value {
	return reflect.ValueOf((*T)(nil))
}

// surfaceFuncs reflects every method declared on the surface interface type into
// a reflect.Value bound to value, returning the per-package symbol table that
// ImportPackageValues consumes. It iterates the interface's own method set rather
// than the concrete value's, so promoted methods on the receiver (for example a
// testify mock's helpers) never leak into the exported package. It errors when the
// surface declares no methods, the value is a nil interface, or a declared method
// is missing on value.
func surfaceFuncs(pkg string, surfaceType reflect.Type, value reflect.Value) (map[string]reflect.Value, error) {
	count := surfaceType.NumMethod()
	if count == 0 {
		return nil, oops.
			In("repl").
			Code("host_surface_empty").
			Errorf("host surface %q declares no methods to register", pkg)
	}

	// A nil surface yields a zero reflect.Value, on which MethodByName would panic;
	// report it as an oops error so the never-panic contract holds at the boundary.
	if !value.IsValid() {
		return nil, oops.
			In("repl").
			Code("host_surface_nil").
			Errorf("host surface %q is nil", pkg)
	}

	funcs := make(map[string]reflect.Value, count)

	for index := range count {
		name := surfaceType.Method(index).Name

		bound := value.MethodByName(name)
		if !bound.IsValid() {
			return nil, oops.
				In("repl").
				Code("host_surface_method_missing").
				Errorf("host surface %q is missing method %q", pkg, name)
		}

		funcs[name] = bound
	}

	return funcs, nil
}
