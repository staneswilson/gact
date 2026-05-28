// Package expr is the semver-stable public façade for gact's GitHub
// Actions expression evaluator. Implementations live in
// internal/authoring/expr; this package exposes only the contractually
// stable types and constructors.
//
// ADR-003 (Public API surface) commits to two things:
//
//  1. Types declared here — Value, Kind, Context, Evaluator — and the
//     functions/methods directly attached to them keep their shape and
//     semantics across minor versions. Adding fields, methods, or kinds
//     is allowed; renaming or removing them is not.
//  2. The internal/authoring/expr package is *not* part of the API. It
//     may change freely. archlint enforces that pkg/ never imports
//     internal/, which is also why this package does not use Go type
//     aliases to re-export internal types — the dependency direction
//     would be inverted. Instead, the canonical types are declared here
//     and the internal implementation imports them.
//
// The evaluator is constructed via New, which parses a source string
// and returns an Evaluator whose Evaluate method runs against a Context.
package expr

import (
	"fmt"
	"strconv"
)

// Kind tags the dynamic type of a Value. The set is closed; future
// additions are permitted (additive only) but must keep existing
// constants at their current numeric positions.
type Kind int

const (
	KindNull Kind = iota
	KindBool
	KindNumber
	KindString
	KindArray
	KindObject
)

// Value is the runtime value type for evaluated expressions. The Data
// field's concrete Go type is determined by Kind:
//
//	KindNull   -> nil
//	KindBool   -> bool
//	KindNumber -> float64
//	KindString -> string
//	KindArray  -> []any
//	KindObject -> map[string]any
//
// Construct Value literals directly when you need to feed values into a
// Context implementation; consumers receive them back from Evaluate.
type Value struct {
	Kind Kind
	Data any
}

// String returns a debug representation of the Value. It is intended
// for logs and error messages; user-facing template interpolation uses
// AsString, which follows GH's rendering rules.
func (v Value) String() string {
	switch v.Kind {
	case KindNull:
		return "null"
	case KindBool:
		if b, _ := v.Data.(bool); b {
			return "true"
		}
		return "false"
	case KindNumber:
		n, _ := v.Data.(float64)
		return formatNumber(n)
	case KindString:
		s, _ := v.Data.(string)
		return strconv.Quote(s)
	case KindArray:
		return fmt.Sprintf("array(%v)", v.Data)
	case KindObject:
		return fmt.Sprintf("object(%v)", v.Data)
	}
	return "unknown"
}

// IsTruthy implements GH's truthiness rules: null, false, 0, NaN and
// the empty string are falsy. Objects and arrays are always truthy,
// even when empty — matching GH's behaviour, which differs from
// Python/JS conventions for containers.
func (v Value) IsTruthy() bool {
	switch v.Kind {
	case KindNull:
		return false
	case KindBool:
		b, _ := v.Data.(bool)
		return b
	case KindNumber:
		n, _ := v.Data.(float64)
		return n != 0 && n == n // n == n filters NaN
	case KindString:
		s, _ := v.Data.(string)
		return s != ""
	case KindObject, KindArray:
		return true
	}
	return false
}

// AsString renders the Value as it would appear when interpolated into
// a GH template (${{ ... }}). Null becomes the empty string, booleans
// use the lowercase literals "true" / "false", and integers print
// without a trailing ".0".
func (v Value) AsString() string {
	switch v.Kind {
	case KindNull:
		return ""
	case KindBool:
		b, _ := v.Data.(bool)
		if b {
			return "true"
		}
		return "false"
	case KindNumber:
		n, _ := v.Data.(float64)
		return formatNumber(n)
	case KindString:
		s, _ := v.Data.(string)
		return s
	case KindObject:
		return fmt.Sprintf("%v", v.Data)
	case KindArray:
		return fmt.Sprintf("%v", v.Data)
	}
	return ""
}

// AsBool is a convenience wrapper around IsTruthy for callers whose
// intent is "give me a bool" rather than the more general truthiness
// check.
func (v Value) AsBool() bool { return v.IsTruthy() }

// Equal implements GH's `==` comparison. Same-kind comparisons go
// through Go's `==`; null is equal only to null; cross-kind comparisons
// coerce both sides to numbers when possible (the GH semantic) and are
// otherwise false.
func (v Value) Equal(o Value) bool {
	if v.Kind == KindNull || o.Kind == KindNull {
		return v.Kind == KindNull && o.Kind == KindNull
	}
	if v.Kind == o.Kind {
		switch v.Kind {
		case KindBool:
			a, _ := v.Data.(bool)
			b, _ := o.Data.(bool)
			return a == b
		case KindNumber:
			a, _ := v.Data.(float64)
			b, _ := o.Data.(float64)
			return a == b
		case KindString:
			a, _ := v.Data.(string)
			b, _ := o.Data.(string)
			return a == b
		}
		// Object / array equality is intentionally false — GH does not
		// define structural equality for these and we want callers to
		// rely on contains() etc. instead.
		return false
	}
	an, aok := numberCoerce(v)
	bn, bok := numberCoerce(o)
	if aok && bok {
		return an == bn
	}
	return false
}

// formatNumber prints integers without a trailing ".0" and otherwise
// preserves the natural decimal representation. This matches the way
// GH renders numeric template output.
func formatNumber(n float64) string {
	if n == float64(int64(n)) {
		return strconv.FormatInt(int64(n), 10)
	}
	return strconv.FormatFloat(n, 'f', -1, 64)
}

// numberCoerce implements GH's "promote to number" rule used by Equal
// and the relational operators when the two sides disagree on kind.
// Booleans become 1/0; strings parse via strconv; everything else fails.
func numberCoerce(v Value) (float64, bool) {
	switch v.Kind {
	case KindNumber:
		n, _ := v.Data.(float64)
		return n, true
	case KindBool:
		if b, _ := v.Data.(bool); b {
			return 1, true
		}
		return 0, true
	case KindString:
		s, _ := v.Data.(string)
		if s == "" {
			return 0, true
		}
		n, err := strconv.ParseFloat(s, 64)
		if err != nil {
			return 0, false
		}
		return n, true
	}
	return 0, false
}

// Context resolves identifiers during expression evaluation. The two
// arguments are (scope, key) where scope is the root identifier
// (e.g. "github", "matrix", "runner") and key is the first member
// reference under it. Implementations return Null for unknown
// (scope, key) pairs — the soft-lookup semantic — and reserve errors
// for genuine failures (e.g. an adapter could not reach an upstream
// data source).
//
// StaticContext (Task 0.11) and a live RuntimeContext (scheduling)
// implement this interface; consumers are free to provide their own.
type Context interface {
	Get(scope, key string) (Value, error)
}

// NoContext returns a Context that responds Null to every lookup. It is
// the right choice for evaluating literal expressions, smoke-testing,
// and tests that do not exercise context-dependent paths.
func NoContext() Context { return emptyContext{} }

type emptyContext struct{}

func (emptyContext) Get(_, _ string) (Value, error) {
	return Value{Kind: KindNull}, nil
}

// Evaluator is the compiled form of an expression. New parses the source
// string once and stores a closure that runs the AST walker on demand.
// The internal package supplies the evalFn via newEvaluator; this struct
// is intentionally opaque so the AST representation can evolve without
// breaking the public API.
type Evaluator struct {
	src    string
	evalFn func(Context) (Value, error)
}

// Source returns the original expression text passed to New. Useful for
// diagnostic output where the caller has lost track of the source.
func (e *Evaluator) Source() string {
	if e == nil {
		return ""
	}
	return e.src
}

// Evaluate runs the compiled expression against ctx. A nil ctx behaves
// like NoContext().
func (e *Evaluator) Evaluate(ctx Context) (Value, error) {
	if e == nil || e.evalFn == nil {
		return Value{}, fmt.Errorf("expr: nil evaluator")
	}
	if ctx == nil {
		ctx = NoContext()
	}
	return e.evalFn(ctx)
}

// compiler is set by the internal package's init() to wire the
// implementation into the façade. We use this indirection because the
// pkg/ package cannot import internal/ (archlint rule) — so the data
// flow must run the other way: internal/authoring/expr imports
// pkg/expr at init time and registers its compile function here.
var compiler func(src string) (func(Context) (Value, error), error)

// RegisterCompiler is called by the internal package's init() to install
// the implementation. It is exported (and not in an internal-only build
// tag) only so the internal package can call it — third-party code has
// no reason to invoke this and doing so will overwrite the installed
// implementation, which is undefined behaviour.
func RegisterCompiler(c func(src string) (func(Context) (Value, error), error)) {
	compiler = c
}

// New parses src and returns an Evaluator ready to Evaluate against a
// Context. Errors describe lexical or syntactic problems with src and
// include byte offsets where helpful.
func New(src string) (*Evaluator, error) {
	if compiler == nil {
		return nil, fmt.Errorf("expr: implementation not registered (import _ \"github.com/staneswilson/gact/internal/authoring/expr\" is required)")
	}
	fn, err := compiler(src)
	if err != nil {
		return nil, err
	}
	return &Evaluator{src: src, evalFn: fn}, nil
}
