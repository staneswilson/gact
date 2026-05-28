package expr

import (
	"strings"
	"testing"

	pubexpr "github.com/staneswilson/gact/pkg/expr"
)

// ----------------------------------------------------------------------------
// contains()
// ----------------------------------------------------------------------------

// TestFunc_Contains_String_FindsNeedle covers GH's substring-containment
// path: present, absent, and the surprising "empty needle is always
// present" rule that lets `contains(matrix.foo, ”)` short-circuit cleanly.
func TestFunc_Contains_String_FindsNeedle(t *testing.T) {
	ctx := EmptyContext()
	cases := []struct {
		name string
		src  string
		want bool
	}{
		{"present", "contains('hello world', 'world')", true},
		{"absent", "contains('hello', 'xyz')", false},
		{"empty needle in non-empty", "contains('x', '')", true},
		{"empty needle in empty", "contains('', '')", true},
		{"case-sensitive miss", "contains('Hello', 'hello')", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := evalSrc(t, tc.src, ctx)
			if got.Kind != pubexpr.KindBool {
				t.Fatalf("kind = %d, want bool", got.Kind)
			}
			if got.AsBool() != tc.want {
				t.Fatalf("%s = %v, want %v", tc.src, got.AsBool(), tc.want)
			}
		})
	}
}

// TestFunc_Contains_Array_ScansElements verifies the array dispatch path:
// element equality follows Value.Equal so numeric/string coercion is the
// same as the rest of the evaluator.
func TestFunc_Contains_Array_ScansElements(t *testing.T) {
	ctx := stubContext{data: map[string]map[string]pubexpr.Value{
		"matrix": {
			"strs": {Kind: pubexpr.KindArray, Data: []any{"a", "b", "c"}},
			"nums": {Kind: pubexpr.KindArray, Data: []any{1.0, 2.0, 3.0}},
			"mixed": {Kind: pubexpr.KindArray, Data: []any{
				"x", 42.0, true, nil,
			}},
		},
	}}
	cases := []struct {
		name string
		src  string
		want bool
	}{
		{"string element present", "contains(matrix.strs, 'b')", true},
		{"string element absent", "contains(matrix.strs, 'z')", false},
		{"number element present", "contains(matrix.nums, 2)", true},
		{"number element absent", "contains(matrix.nums, 4)", false},
		{"mixed string", "contains(matrix.mixed, 'x')", true},
		{"mixed bool", "contains(matrix.mixed, true)", true},
		{"mixed null", "contains(matrix.mixed, null)", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := evalSrc(t, tc.src, ctx)
			if got.AsBool() != tc.want {
				t.Fatalf("%s = %v, want %v", tc.src, got.AsBool(), tc.want)
			}
		})
	}
}

// TestFunc_Contains_NonStringNonArray_ReturnsFalse asserts the soft-fail
// path. GH does not raise an error on `contains(null, 'x')`; it simply
// reports false, matching the soft-lookup philosophy.
func TestFunc_Contains_NonStringNonArray_ReturnsFalse(t *testing.T) {
	ctx := EmptyContext()
	if v := evalSrc(t, "contains(missing.thing, 'x')", ctx); v.AsBool() {
		t.Fatal("contains(null, 'x') = true, want false")
	}
}

// ----------------------------------------------------------------------------
// startsWith() / endsWith()
// ----------------------------------------------------------------------------

// TestFunc_StartsWith_CaseSensitive is the documented GH behaviour:
// `Startswith` is case-sensitive even though the function name itself
// is case-insensitive at the dispatch layer.
func TestFunc_StartsWith_CaseSensitive(t *testing.T) {
	ctx := EmptyContext()
	cases := []struct {
		name string
		src  string
		want bool
	}{
		{"present", "startsWith('refs/heads/main', 'refs/')", true},
		{"absent", "startsWith('refs/heads/main', 'tags/')", false},
		{"empty prefix", "startsWith('hello', '')", true},
		{"case differs", "startsWith('Hello', 'hello')", false},
		{"prefix longer than s", "startsWith('hi', 'hello')", false},
		{"name case-insensitive dispatch", "STARTSWITH('hello', 'he')", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := evalSrc(t, tc.src, ctx)
			if got.AsBool() != tc.want {
				t.Fatalf("%s = %v, want %v", tc.src, got.AsBool(), tc.want)
			}
		})
	}
}

// TestFunc_EndsWith_CaseSensitive mirrors startsWith on the suffix side.
func TestFunc_EndsWith_CaseSensitive(t *testing.T) {
	ctx := EmptyContext()
	cases := []struct {
		name string
		src  string
		want bool
	}{
		{"present", "endsWith('build.tar.gz', '.gz')", true},
		{"absent", "endsWith('build.tar.gz', '.zip')", false},
		{"empty suffix", "endsWith('hello', '')", true},
		{"case differs", "endsWith('Hello', 'LLO')", false},
		{"suffix longer than s", "endsWith('hi', 'shi')", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := evalSrc(t, tc.src, ctx)
			if got.AsBool() != tc.want {
				t.Fatalf("%s = %v, want %v", tc.src, got.AsBool(), tc.want)
			}
		})
	}
}

// ----------------------------------------------------------------------------
// format()
// ----------------------------------------------------------------------------

// TestFunc_Format_PositionalAndEscapedBraces covers each behaviour worth
// pinning: a single positional slot, multiple slots, repeated indices, and
// the `{{`/`}}` escape rule that lets GH templates emit literal braces.
func TestFunc_Format_PositionalAndEscapedBraces(t *testing.T) {
	ctx := EmptyContext()
	cases := []struct {
		name string
		src  string
		want string
	}{
		{"single", "format('hello {0}', 'world')", "hello world"},
		{"multiple", "format('{0} + {1} = {2}', 1, 2, 3)", "1 + 2 = 3"},
		{"repeated", "format('{0}{0}{0}', 'na')", "nanana"},
		{"escape open", "format('{{not a placeholder}}')", "{not a placeholder}"},
		{"escape mixed", "format('{{{0}}}', 'x')", "{x}"},
		{"empty template", "format('')", ""},
		{"no args needed", "format('plain text')", "plain text"},
		{"number stringified", "format('{0}', 42)", "42"},
		{"bool stringified", "format('{0}', true)", "true"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := evalSrc(t, tc.src, ctx)
			if got.Kind != pubexpr.KindString {
				t.Fatalf("kind = %d, want string", got.Kind)
			}
			if got.AsString() != tc.want {
				t.Fatalf("%s = %q, want %q", tc.src, got.AsString(), tc.want)
			}
		})
	}
}

// TestFunc_Format_IndexOutOfRange ensures that workflow authors who
// reference a placeholder they did not supply get a precise error.
func TestFunc_Format_IndexOutOfRange(t *testing.T) {
	e, err := compile("format('{2}', 'a', 'b')")
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	if _, err := e.evaluate(EmptyContext()); err == nil {
		t.Fatal("expected out-of-range error, got nil")
	} else if !strings.Contains(err.Error(), "out of range") {
		t.Fatalf("error %q does not mention out of range", err.Error())
	}
}

// TestFunc_Format_UnmatchedBrace_Errors detects malformed templates so
// that, for example, `format('hi {0', 'x')` does not silently succeed.
func TestFunc_Format_UnmatchedBrace_Errors(t *testing.T) {
	cases := []string{
		"format('hi {0', 'x')",
		"format('hi 0}', 'x')",
		"format('{}', 'x')",
		"format('{abc}', 'x')",
	}
	for _, src := range cases {
		t.Run(src, func(t *testing.T) {
			e, err := compile(src)
			if err != nil {
				t.Fatalf("compile: %v", err)
			}
			if _, err := e.evaluate(EmptyContext()); err == nil {
				t.Fatalf("expected error for %q, got nil", src)
			}
		})
	}
}

// ----------------------------------------------------------------------------
// join()
// ----------------------------------------------------------------------------

// TestFunc_Join_ArraySeparators exercises array joining with a custom
// separator, the default separator, single-element behaviour, and the
// empty-array case (which collapses to the empty string).
func TestFunc_Join_ArraySeparators(t *testing.T) {
	ctx := stubContext{data: map[string]map[string]pubexpr.Value{
		"x": {
			"items": {Kind: pubexpr.KindArray, Data: []any{"a", "b", "c"}},
			"mixed": {Kind: pubexpr.KindArray, Data: []any{
				"x", 42.0, true, nil,
			}},
			"single": {Kind: pubexpr.KindArray, Data: []any{"only"}},
			"empty":  {Kind: pubexpr.KindArray, Data: []any{}},
		},
	}}
	cases := []struct {
		name string
		src  string
		want string
	}{
		{"comma default", "join(x.items)", "a,b,c"},
		{"custom sep", "join(x.items, ' - ')", "a - b - c"},
		{"empty sep", "join(x.items, '')", "abc"},
		{"mixed types", "join(x.mixed, '/')", "x/42/true/"},
		{"single element", "join(x.single, ',')", "only"},
		{"empty array", "join(x.empty, ',')", ""},
		{"non-array first arg", "join('solo', ',')", "solo"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := evalSrc(t, tc.src, ctx)
			if got.Kind != pubexpr.KindString {
				t.Fatalf("kind = %d, want string", got.Kind)
			}
			if got.AsString() != tc.want {
				t.Fatalf("%s = %q, want %q", tc.src, got.AsString(), tc.want)
			}
		})
	}
}

// ----------------------------------------------------------------------------
// toJSON()
// ----------------------------------------------------------------------------

// TestFunc_ToJSON_PrimitivesRoundTrip pins each leaf type's JSON output.
// Numbers must not drift to "42.0" and null must become the literal "null"
// rather than the empty string GH's AsString produces for templates.
func TestFunc_ToJSON_PrimitivesRoundTrip(t *testing.T) {
	ctx := EmptyContext()
	cases := []struct {
		name string
		src  string
		want string
	}{
		{"null", "toJSON(null)", "null"},
		{"true", "toJSON(true)", "true"},
		{"false", "toJSON(false)", "false"},
		{"integer", "toJSON(42)", "42"},
		{"float", "toJSON(3.5)", "3.5"},
		{"string", "toJSON('hi')", `"hi"`},
		{"empty string", "toJSON('')", `""`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := evalSrc(t, tc.src, ctx)
			if got.Kind != pubexpr.KindString {
				t.Fatalf("kind = %d, want string", got.Kind)
			}
			if got.AsString() != tc.want {
				t.Fatalf("%s = %q, want %q", tc.src, got.AsString(), tc.want)
			}
		})
	}
}

// TestFunc_ToJSON_ObjectKeysSorted is the canonicalisation guarantee. We
// build an object with keys deliberately out of order; the produced JSON
// must list them alphabetically so callers (and the cache-key hasher)
// see deterministic output.
func TestFunc_ToJSON_ObjectKeysSorted(t *testing.T) {
	ctx := stubContext{data: map[string]map[string]pubexpr.Value{
		"x": {
			"obj": {Kind: pubexpr.KindObject, Data: map[string]any{
				"zeta":  1.0,
				"alpha": 2.0,
				"mu":    3.0,
			}},
		},
	}}
	got := evalSrc(t, "toJSON(x.obj)", ctx).AsString()
	want := `{"alpha":2,"mu":3,"zeta":1}`
	if got != want {
		t.Fatalf("toJSON(obj) = %s, want %s", got, want)
	}
}

// TestFunc_ToJSON_NestedShape covers an object with a nested object and
// array — exactly the shape `github.event` typically takes. The nested
// keys must also be sorted.
func TestFunc_ToJSON_NestedShape(t *testing.T) {
	ctx := stubContext{data: map[string]map[string]pubexpr.Value{
		"github": {
			"event": {Kind: pubexpr.KindObject, Data: map[string]any{
				"pr": map[string]any{
					"number": 17.0,
					"author": "octo",
				},
				"labels": []any{"bug", "needs-triage"},
			}},
		},
	}}
	got := evalSrc(t, "toJSON(github.event)", ctx).AsString()
	want := `{"labels":["bug","needs-triage"],"pr":{"author":"octo","number":17}}`
	if got != want {
		t.Fatalf("toJSON(nested) = %s, want %s", got, want)
	}
}

// ----------------------------------------------------------------------------
// fromJSON()
// ----------------------------------------------------------------------------

// TestFunc_FromJSON_AllKinds parses each JSON leaf and asserts the
// resulting Value.Kind. We do not assert the data field directly — the
// AsString rendering is enough to prove the value reached us correctly.
func TestFunc_FromJSON_AllKinds(t *testing.T) {
	ctx := EmptyContext()
	cases := []struct {
		name string
		src  string
		kind pubexpr.Kind
		want string
	}{
		{"null", `fromJSON('null')`, pubexpr.KindNull, ""},
		{"true", `fromJSON('true')`, pubexpr.KindBool, "true"},
		{"false", `fromJSON('false')`, pubexpr.KindBool, "false"},
		{"integer", `fromJSON('42')`, pubexpr.KindNumber, "42"},
		{"float", `fromJSON('3.5')`, pubexpr.KindNumber, "3.5"},
		{"string", `fromJSON('"hi"')`, pubexpr.KindString, "hi"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := evalSrc(t, tc.src, ctx)
			if got.Kind != tc.kind {
				t.Fatalf("kind = %d, want %d", got.Kind, tc.kind)
			}
			if got.AsString() != tc.want {
				t.Fatalf("AsString = %q, want %q", got.AsString(), tc.want)
			}
		})
	}
}

// TestFunc_FromJSON_Object parses an object literal and lets the
// evaluator navigate into it via .field. That proves the resulting Value
// is genuinely an object the rest of the engine can consume.
func TestFunc_FromJSON_Object(t *testing.T) {
	ctx := EmptyContext()
	v := evalSrc(t, `fromJSON('{"name":"octo","age":42}').name`, ctx)
	if v.AsString() != "octo" {
		t.Fatalf("name = %q, want octo", v.AsString())
	}
	v = evalSrc(t, `fromJSON('{"name":"octo","age":42}').age`, ctx)
	if v.AsString() != "42" {
		t.Fatalf("age = %q, want 42", v.AsString())
	}
}

// TestFunc_FromJSON_Array uses [] indexing to prove array decoding works
// end-to-end with the evaluator's index-lookup path.
func TestFunc_FromJSON_Array(t *testing.T) {
	ctx := EmptyContext()
	v := evalSrc(t, `fromJSON('[10, 20, 30]')[1]`, ctx)
	if v.AsString() != "20" {
		t.Fatalf("arr[1] = %q, want 20", v.AsString())
	}
}

// TestFunc_FromJSON_RoundTrip composes toJSON and fromJSON to confirm
// the pair is lossless for the JSON-representable subset.
func TestFunc_FromJSON_RoundTrip(t *testing.T) {
	ctx := stubContext{data: map[string]map[string]pubexpr.Value{
		"x": {
			"obj": {Kind: pubexpr.KindObject, Data: map[string]any{
				"a": 1.0,
				"b": "two",
				"c": []any{true, false, nil},
			}},
		},
	}}
	v := evalSrc(t, "fromJSON(toJSON(x.obj)).b", ctx)
	if v.AsString() != "two" {
		t.Fatalf("round-trip .b = %q, want two", v.AsString())
	}
}

// TestFunc_FromJSON_InvalidJSON_Errors must fail with a message clearly
// signalling the parse problem, so users debugging a malformed Step
// `outputs:` snippet get an actionable error.
func TestFunc_FromJSON_InvalidJSON_Errors(t *testing.T) {
	cases := []string{
		`fromJSON('{not json}')`,
		`fromJSON('')`,
		`fromJSON('{')`,
		`fromJSON('}}')`,
	}
	for _, src := range cases {
		t.Run(src, func(t *testing.T) {
			e, err := compile(src)
			if err != nil {
				t.Fatalf("compile: %v", err)
			}
			if _, err := e.evaluate(EmptyContext()); err == nil {
				t.Fatalf("expected error for %q, got nil", src)
			}
		})
	}
}

// TestFunc_FromJSON_TrailingContent_Errors guards against the silent
// accept of "{}stuff" — the second-Decode + io.EOF check should catch it.
func TestFunc_FromJSON_TrailingContent_Errors(t *testing.T) {
	e, err := compile(`fromJSON('{} garbage')`)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	if _, err := e.evaluate(EmptyContext()); err == nil {
		t.Fatal("expected trailing-content error, got nil")
	}
}

// ----------------------------------------------------------------------------
// status functions
// ----------------------------------------------------------------------------

// TestFunc_StatusFunctions_Defaults pins the parse-time constants we
// install in funcs_status.go. The scheduler will override success() /
// failure() / cancelled() with state-aware variants in P1; if those
// override hooks ever fail to land, this test still passes — it asserts
// only the documented placeholder behaviour.
func TestFunc_StatusFunctions_Defaults(t *testing.T) {
	ctx := EmptyContext()
	cases := []struct {
		src  string
		want bool
	}{
		{"always()", true},
		{"success()", true},
		{"failure()", false},
		{"cancelled()", false},
	}
	for _, tc := range cases {
		t.Run(tc.src, func(t *testing.T) {
			got := evalSrc(t, tc.src, ctx)
			if got.Kind != pubexpr.KindBool {
				t.Fatalf("kind = %d, want bool", got.Kind)
			}
			if got.AsBool() != tc.want {
				t.Fatalf("%s = %v, want %v", tc.src, got.AsBool(), tc.want)
			}
		})
	}
}

// TestFunc_StatusFunctions_RejectArgs ensures we surface obvious caller
// bugs rather than silently accepting them. GH itself rejects
// `success(true)`; we do the same.
func TestFunc_StatusFunctions_RejectArgs(t *testing.T) {
	cases := []string{
		"always(1)",
		"success(1)",
		"failure(1)",
		"cancelled(1)",
	}
	for _, src := range cases {
		t.Run(src, func(t *testing.T) {
			e, err := compile(src)
			if err != nil {
				t.Fatalf("compile: %v", err)
			}
			if _, err := e.evaluate(EmptyContext()); err == nil {
				t.Fatalf("expected arity error for %q, got nil", src)
			}
		})
	}
}

// ----------------------------------------------------------------------------
// argument-arity errors for the value-shape built-ins
// ----------------------------------------------------------------------------

// TestFuncs_ArityErrors documents the wrong-argument-count contract for
// each non-status built-in. The error messages do not have to be
// identical — only the presence of the error matters here.
func TestFuncs_ArityErrors(t *testing.T) {
	cases := []string{
		"contains('a')",
		"contains('a','b','c')",
		"startsWith('a')",
		"endsWith('a')",
		"format()",
		"join()",
		"join('a',',','extra')",
		"toJSON()",
		"toJSON(1, 2)",
		"fromJSON()",
		"fromJSON('1', '2')",
	}
	for _, src := range cases {
		t.Run(src, func(t *testing.T) {
			e, err := compile(src)
			if err != nil {
				t.Fatalf("compile: %v", err)
			}
			if _, err := e.evaluate(EmptyContext()); err == nil {
				t.Fatalf("expected arity error for %q, got nil", src)
			}
		})
	}
}
