package expr

import (
	"strings"
	"testing"

	pubexpr "github.com/staneswilson/gact/pkg/expr"
)

// stubContext is a minimal Context used only by these tests. It returns
// fixed values for known (scope,key) pairs and Null for anything else.
// The eventual production StaticContext (Task 0.11) will be richer but
// must satisfy the same interface.
type stubContext struct {
	data map[string]map[string]pubexpr.Value
}

func (s stubContext) Get(scope, key string) (pubexpr.Value, error) {
	if m, ok := s.data[scope]; ok {
		if v, ok := m[key]; ok {
			return v, nil
		}
	}
	return pubexpr.Value{Kind: pubexpr.KindNull}, nil
}

// evalSrc is a tiny wrapper that compiles and evaluates a source string
// against the supplied context, returning the resulting Value.
func evalSrc(t *testing.T, src string, ctx pubexpr.Context) pubexpr.Value {
	t.Helper()
	e, err := compile(src)
	if err != nil {
		t.Fatalf("compile(%q): %v", src, err)
	}
	v, err := e.evaluate(ctx)
	if err != nil {
		t.Fatalf("evaluate(%q): %v", src, err)
	}
	return v
}

// TestEval_Literals exercises every primary literal path: string, number,
// boolean (both polarities), and null.
func TestEval_Literals(t *testing.T) {
	ctx := EmptyContext()
	cases := []struct {
		src   string
		kind  pubexpr.Kind
		asStr string
	}{
		{"'hi'", pubexpr.KindString, "hi"},
		{"42", pubexpr.KindNumber, "42"},
		{"true", pubexpr.KindBool, "true"},
		{"false", pubexpr.KindBool, "false"},
		{"null", pubexpr.KindNull, ""},
	}
	for _, tc := range cases {
		t.Run(tc.src, func(t *testing.T) {
			got := evalSrc(t, tc.src, ctx)
			if got.Kind != tc.kind {
				t.Fatalf("kind = %d, want %d", got.Kind, tc.kind)
			}
			if got.AsString() != tc.asStr {
				t.Fatalf("AsString = %q, want %q", got.AsString(), tc.asStr)
			}
		})
	}
}

// TestEval_Unary verifies that ! flips truthiness and returns a bool Value.
func TestEval_Unary(t *testing.T) {
	ctx := EmptyContext()
	if !evalSrc(t, "!false", ctx).AsBool() {
		t.Fatal("!false should be true")
	}
	if evalSrc(t, "!true", ctx).AsBool() {
		t.Fatal("!true should be false")
	}
	if !evalSrc(t, "!null", ctx).AsBool() {
		t.Fatal("!null should be true (null is falsy)")
	}
}

// TestEval_BinaryOperators covers each of the six relational operators
// at least once. Numbers are sufficient — the implementation routes
// through a shared comparison helper so per-type coverage is not needed.
func TestEval_BinaryOperators(t *testing.T) {
	ctx := EmptyContext()
	cases := []struct {
		src  string
		want bool
	}{
		{"1 == 1", true},
		{"1 == 2", false},
		{"1 != 2", true},
		{"1 != 1", false},
		{"1 < 2", true},
		{"2 < 1", false},
		{"1 <= 1", true},
		{"2 <= 1", false},
		{"2 > 1", true},
		{"1 > 2", false},
		{"1 >= 1", true},
		{"1 >= 2", false},
	}
	for _, tc := range cases {
		t.Run(tc.src, func(t *testing.T) {
			if got := evalSrc(t, tc.src, ctx).AsBool(); got != tc.want {
				t.Fatalf("%s = %v, want %v", tc.src, got, tc.want)
			}
		})
	}
}

// TestEval_ShortCircuit_AND verifies that && returns the LHS unchanged
// when falsy (without touching the RHS) and the RHS when LHS is truthy.
// The "undefined.x" branch must be safe to leave unevaluated because GH
// allows expressions like `false && some.missing.thing`.
func TestEval_ShortCircuit_AND(t *testing.T) {
	ctx := EmptyContext()

	// Falsy LHS short-circuits — RHS containing an unknown function is
	// not evaluated, so this must not error.
	v := evalSrc(t, "false && unknownFn()", ctx)
	if v.Kind != pubexpr.KindBool || v.AsBool() != false {
		t.Fatalf("false && _ = %+v, want bool(false)", v)
	}

	// Truthy LHS returns the RHS value as-is (the deciding operand).
	v = evalSrc(t, "true && 'kept'", ctx)
	if v.Kind != pubexpr.KindString || v.AsString() != "kept" {
		t.Fatalf("true && 'kept' = %+v, want string 'kept'", v)
	}
}

// TestEval_ShortCircuit_OR mirrors the AND case: truthy LHS returns
// immediately; falsy LHS falls through to the RHS. The `'' || 30`
// case exercises the GH `value || default` idiom.
func TestEval_ShortCircuit_OR(t *testing.T) {
	ctx := EmptyContext()

	v := evalSrc(t, "true || unknownFn()", ctx)
	if v.Kind != pubexpr.KindBool || v.AsBool() != true {
		t.Fatalf("true || _ = %+v, want bool(true)", v)
	}

	v = evalSrc(t, "false || 'kept'", ctx)
	if v.Kind != pubexpr.KindString || v.AsString() != "kept" {
		t.Fatalf("false || 'kept' = %+v, want string 'kept'", v)
	}

	// `'' || 30` returns the deciding operand 30 as a number.
	v = evalSrc(t, "'' || 30", ctx)
	if v.Kind != pubexpr.KindNumber || v.AsString() != "30" {
		t.Fatalf("'' || 30 = %+v, want number 30", v)
	}
}

// TestEval_ContextLookup pulls a known value out of a stub context and
// confirms that nested .field navigation works against object data.
func TestEval_ContextLookup(t *testing.T) {
	ctx := stubContext{data: map[string]map[string]pubexpr.Value{
		"github": {
			"ref": {Kind: pubexpr.KindString, Data: "refs/heads/main"},
			"event": {Kind: pubexpr.KindObject, Data: map[string]any{
				"action": "opened",
			}},
		},
	}}
	if v := evalSrc(t, "github.ref", ctx); v.AsString() != "refs/heads/main" {
		t.Fatalf("github.ref = %q, want refs/heads/main", v.AsString())
	}
	if v := evalSrc(t, "github.event.action", ctx); v.AsString() != "opened" {
		t.Fatalf("github.event.action = %q, want opened", v.AsString())
	}
}

// TestEval_MissingKeysReturnNull is the soft-lookup contract documented in
// the design: missing root identifiers and missing object fields evaluate
// to Null rather than raising errors. The lint pass surfaces these
// differently — that path is not exercised here.
func TestEval_MissingKeysReturnNull(t *testing.T) {
	ctx := EmptyContext()
	cases := []string{
		"nothing.here",
		"some.deeply.nested.thing",
		"github.event.missing",
	}
	for _, src := range cases {
		t.Run(src, func(t *testing.T) {
			v := evalSrc(t, src, ctx)
			if v.Kind != pubexpr.KindNull {
				t.Fatalf("%s.Kind = %d, want KindNull", src, v.Kind)
			}
		})
	}
}

// TestEval_UnknownFunction expects a clear error because Task 0.7 hasn't
// installed any functions yet — the registry is empty but the dispatch
// path itself must work so the user sees "unknown function …" not a panic.
func TestEval_UnknownFunction(t *testing.T) {
	e, err := compile("doesNotExist(1, 2)")
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	_, err = e.evaluate(EmptyContext())
	if err == nil {
		t.Fatal("expected error for unknown function, got nil")
	}
	if !strings.Contains(strings.ToLower(err.Error()), "unknown function") {
		t.Fatalf("error %q does not contain 'unknown function'", err.Error())
	}
}

// TestEval_IndexLookup checks the [] postfix against both objects (string
// keys) and arrays (integer indices). Out-of-range index → Null.
func TestEval_IndexLookup(t *testing.T) {
	ctx := stubContext{data: map[string]map[string]pubexpr.Value{
		"data": {
			"items": {Kind: pubexpr.KindArray, Data: []any{"a", "b", "c"}},
			"obj":   {Kind: pubexpr.KindObject, Data: map[string]any{"k": "v"}},
		},
	}}
	if v := evalSrc(t, "data.items[1]", ctx); v.AsString() != "b" {
		t.Fatalf("items[1] = %q, want b", v.AsString())
	}
	if v := evalSrc(t, "data.items[99]", ctx); v.Kind != pubexpr.KindNull {
		t.Fatalf("items[99].Kind = %d, want KindNull", v.Kind)
	}
	if v := evalSrc(t, "data.obj['k']", ctx); v.AsString() != "v" {
		t.Fatalf("obj['k'] = %q, want v", v.AsString())
	}
}
