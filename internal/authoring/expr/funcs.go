package expr

import (
	"fmt"
	"strings"
)

// funcImpl is the signature of a built-in GH expression function.
// Functions take their evaluated arguments and return a Value or a
// descriptive error. Short-circuiting at the argument level is not
// supported because no GH built-in needs it — `&&` and `||` are
// handled by the evaluator directly.
type funcImpl func(args []value) (value, error)

// funcRegistry maps lower-cased function names (GH names are
// case-insensitive) to their implementations. It is intentionally
// empty in this task — Task 0.7 ships contains/startsWith/format/etc.
// We expose the registry here so:
//
//  1. The evaluator can dispatch on `name(args)` without restructuring
//     once Task 0.7 lands.
//  2. Adding a function in Task 0.7 is a one-liner: register it.
//
// Functions added later should be pure (no I/O, no time, no random).
// hashFiles() is the planned exception, and it lives in a separate
// adapter package per ADR-002.
var funcRegistry = map[string]funcImpl{}

// register adds (or replaces) a function. Used by Task 0.7's init()
// functions; we expose it now so the dispatch path is real.
func register(name string, fn funcImpl) {
	funcRegistry[strings.ToLower(name)] = fn
}

// callFunction looks up name in the registry and invokes it. Unknown
// names yield a clear "unknown function" error — fail-closed by design
// so a typo in a workflow does not silently produce a Null.
func callFunction(name string, args []value) (value, error) {
	fn, ok := funcRegistry[strings.ToLower(name)]
	if !ok {
		return value{}, fmt.Errorf("expr: unknown function %q", name)
	}
	return fn(args)
}
