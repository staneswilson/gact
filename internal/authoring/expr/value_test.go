package expr

import (
	"testing"

	pubexpr "github.com/staneswilson/gact/pkg/expr"
)

// TestValue_IsTruthy mirrors GH's truthiness rules: false, null, 0, NaN and
// empty string are falsy; everything else is truthy. Each row is a single
// value-vs-expected pair so a failure points cleanly at the offending row.
func TestValue_IsTruthy(t *testing.T) {
	cases := []struct {
		name string
		in   pubexpr.Value
		want bool
	}{
		{"null", pubexpr.Value{Kind: pubexpr.KindNull}, false},
		{"false", pubexpr.Value{Kind: pubexpr.KindBool, Data: false}, false},
		{"true", pubexpr.Value{Kind: pubexpr.KindBool, Data: true}, true},
		{"zero", pubexpr.Value{Kind: pubexpr.KindNumber, Data: 0.0}, false},
		{"one", pubexpr.Value{Kind: pubexpr.KindNumber, Data: 1.0}, true},
		{"empty string", pubexpr.Value{Kind: pubexpr.KindString, Data: ""}, false},
		{"non-empty string", pubexpr.Value{Kind: pubexpr.KindString, Data: "x"}, true},
		{"object", pubexpr.Value{Kind: pubexpr.KindObject, Data: map[string]any{}}, true},
		{"array", pubexpr.Value{Kind: pubexpr.KindArray, Data: []any{}}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.in.IsTruthy(); got != tc.want {
				t.Fatalf("IsTruthy = %v, want %v", got, tc.want)
			}
		})
	}
}

// TestValue_AsString covers the GH-template stringification: null → "",
// booleans lowercase, integers without trailing ".0".
func TestValue_AsString(t *testing.T) {
	cases := []struct {
		name string
		in   pubexpr.Value
		want string
	}{
		{"null", pubexpr.Value{Kind: pubexpr.KindNull}, ""},
		{"true", pubexpr.Value{Kind: pubexpr.KindBool, Data: true}, "true"},
		{"false", pubexpr.Value{Kind: pubexpr.KindBool, Data: false}, "false"},
		{"integer", pubexpr.Value{Kind: pubexpr.KindNumber, Data: 42.0}, "42"},
		{"float", pubexpr.Value{Kind: pubexpr.KindNumber, Data: 3.5}, "3.5"},
		{"string", pubexpr.Value{Kind: pubexpr.KindString, Data: "hi"}, "hi"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.in.AsString(); got != tc.want {
				t.Fatalf("AsString = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestValue_AsBool exposes the same truthiness logic via the public boolean
// accessor — kept as its own method so callers do not need to know about
// IsTruthy when their intent is "give me a bool".
func TestValue_AsBool(t *testing.T) {
	if !(pubexpr.Value{Kind: pubexpr.KindBool, Data: true}).AsBool() {
		t.Fatal("AsBool(true) = false")
	}
	if (pubexpr.Value{Kind: pubexpr.KindBool, Data: false}).AsBool() {
		t.Fatal("AsBool(false) = true")
	}
	if (pubexpr.Value{Kind: pubexpr.KindNull}).AsBool() {
		t.Fatal("AsBool(null) = true")
	}
}

// TestValue_Equal validates GH's == semantics: matched kinds compare
// directly; cross-kind compares are number-coerced (truthy bool → 1, "42"
// → 42); null is equal only to null.
func TestValue_Equal(t *testing.T) {
	num := func(n float64) pubexpr.Value {
		return pubexpr.Value{Kind: pubexpr.KindNumber, Data: n}
	}
	str := func(s string) pubexpr.Value {
		return pubexpr.Value{Kind: pubexpr.KindString, Data: s}
	}
	bv := func(b bool) pubexpr.Value {
		return pubexpr.Value{Kind: pubexpr.KindBool, Data: b}
	}
	null := pubexpr.Value{Kind: pubexpr.KindNull}

	cases := []struct {
		name string
		a, b pubexpr.Value
		want bool
	}{
		{"null==null", null, null, true},
		{"null!=zero", null, num(0), false},
		{"num==num", num(2), num(2), true},
		{"num!=num", num(2), num(3), false},
		{"str==str", str("x"), str("x"), true},
		{"bool==bool", bv(true), bv(true), true},
		{"num==strNumeric", num(42), str("42"), true},
		{"num!=strNonNumeric", num(42), str("abc"), false},
		{"bool==numOne", bv(true), num(1), true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.a.Equal(tc.b); got != tc.want {
				t.Fatalf("Equal(%v,%v) = %v, want %v", tc.a, tc.b, got, tc.want)
			}
		})
	}
}

// TestValue_String checks the diagnostic Stringer output. We do not pin the
// exact format because it is for debugging; we only guarantee non-empty
// output for each kind so panics or empty results stand out.
func TestValue_String(t *testing.T) {
	vals := []pubexpr.Value{
		{Kind: pubexpr.KindNull},
		{Kind: pubexpr.KindBool, Data: true},
		{Kind: pubexpr.KindNumber, Data: 7.0},
		{Kind: pubexpr.KindString, Data: "x"},
		{Kind: pubexpr.KindArray, Data: []any{1}},
		{Kind: pubexpr.KindObject, Data: map[string]any{"k": "v"}},
	}
	for _, v := range vals {
		if v.String() == "" {
			t.Fatalf("Value{Kind:%d}.String() = empty", v.Kind)
		}
	}
}

// TestFromAny exercises the JSON-shaped-input adapter used by context
// implementations. JSON numbers arrive as float64; we also cover the int
// and int64 paths because Go callers (StaticContext, Task 0.11) construct
// values directly.
func TestFromAny(t *testing.T) {
	cases := []struct {
		name string
		in   any
		want pubexpr.Kind
	}{
		{"nil", nil, pubexpr.KindNull},
		{"bool", true, pubexpr.KindBool},
		{"float64", 1.5, pubexpr.KindNumber},
		{"int", 7, pubexpr.KindNumber},
		{"int64", int64(7), pubexpr.KindNumber},
		{"string", "x", pubexpr.KindString},
		{"map", map[string]any{}, pubexpr.KindObject},
		{"slice", []any{}, pubexpr.KindArray},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := fromAny(tc.in)
			if got.Kind != tc.want {
				t.Fatalf("fromAny(%v).Kind = %d, want %d", tc.in, got.Kind, tc.want)
			}
		})
	}
}
