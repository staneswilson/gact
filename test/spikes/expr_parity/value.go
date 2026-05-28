package main

import (
	"fmt"
	"strconv"
)

// Value is the spike's runtime value type for the expression evaluator.
//
// GitHub Actions expressions are dynamically typed: a property lookup can yield
// a string, number, boolean, null, object, or array. We keep the data backing
// store as `any` and use Kind to discriminate. This mirrors the design we will
// likely take into production (Task 0.6) unless the spike reveals a structural
// problem with `any`-based dispatch — that judgement is the spike's real output.
//
// Notes on GH semantics encoded here:
//   - Null and "missing property" are the same Value (kindNull). GH's expression
//     engine returns null for any missing nested property, and never panics.
//   - Booleans printed via ${{ }} become the lowercase literals "true" / "false".
//   - Numbers print without a decimal when they round-trip to an integer
//     (e.g. 42 not 42.0). We use strconv.FormatFloat with -1 precision and 'f'
//     format, mirroring the way GH templates render numeric outputs.
//   - Objects and arrays produce no useful template output and surface as
//     "Object" / their JSON when GH's templating coerces them. The spike only
//     compares against `expected` for expressions that resolve to scalar/JSON.
type Value struct {
	Kind kind
	Data any
}

type kind int

const (
	kindNull kind = iota
	kindBool
	kindNumber
	kindString
	kindObject
	kindArray
)

func nullValue() Value             { return Value{Kind: kindNull} }
func boolValue(b bool) Value       { return Value{Kind: kindBool, Data: b} }
func numberValue(n float64) Value  { return Value{Kind: kindNumber, Data: n} }
func stringValue(s string) Value   { return Value{Kind: kindString, Data: s} }
func objectValue(m map[string]any) Value { return Value{Kind: kindObject, Data: m} }
func arrayValue(a []any) Value     { return Value{Kind: kindArray, Data: a} }

// fromAny converts a value freshly decoded from JSON into a Value.
// JSON numbers are always float64; we keep that representation.
func fromAny(v any) Value {
	switch x := v.(type) {
	case nil:
		return nullValue()
	case bool:
		return boolValue(x)
	case float64:
		return numberValue(x)
	case int:
		return numberValue(float64(x))
	case int64:
		return numberValue(float64(x))
	case string:
		return stringValue(x)
	case map[string]any:
		return objectValue(x)
	case []any:
		return arrayValue(x)
	default:
		return stringValue(fmt.Sprintf("%v", x))
	}
}

// truthy implements GH's truthiness rules. These are the same as JS roughly:
// false, null, 0, NaN, empty string are falsy; everything else truthy.
func (v Value) truthy() bool {
	switch v.Kind {
	case kindNull:
		return false
	case kindBool:
		return v.Data.(bool)
	case kindNumber:
		n := v.Data.(float64)
		return n != 0 && n == n // also rejects NaN
	case kindString:
		return v.Data.(string) != ""
	case kindObject, kindArray:
		return true
	}
	return false
}

// asString renders the Value as it would appear when interpolated into a
// template (GH ${{ }} substitution).
func (v Value) asString() string {
	switch v.Kind {
	case kindNull:
		return ""
	case kindBool:
		if v.Data.(bool) {
			return "true"
		}
		return "false"
	case kindNumber:
		return formatNumber(v.Data.(float64))
	case kindString:
		return v.Data.(string)
	case kindObject:
		// Templating would print "Object" — for the spike we serialize JSON
		// for diagnostics. Corpus expressions should not rely on this path.
		return fmt.Sprintf("%v", v.Data)
	case kindArray:
		return fmt.Sprintf("%v", v.Data)
	}
	return ""
}

// kindName returns the lowercase label used in corpus `expected_kind`.
func (v Value) kindName() string {
	switch v.Kind {
	case kindNull:
		return "null"
	case kindBool:
		return "boolean"
	case kindNumber:
		return "number"
	case kindString:
		return "string"
	case kindObject:
		return "object"
	case kindArray:
		return "array"
	}
	return "unknown"
}

func formatNumber(n float64) string {
	// Integers print without trailing ".0"; floats keep their decimal.
	if n == float64(int64(n)) {
		return strconv.FormatInt(int64(n), 10)
	}
	return strconv.FormatFloat(n, 'f', -1, 64)
}

// equal implements `==` / `!=` with GH's coercion rules. The reference
// behaviour: if both sides are the same kind, compare directly; otherwise GH
// coerces numerically when possible — comparing a string to a number triggers
// a parse attempt on the string; comparison against null is true only for
// null/null. The spike sticks to the most common, well-documented cases.
func equal(a, b Value) bool {
	if a.Kind == kindNull || b.Kind == kindNull {
		return a.Kind == kindNull && b.Kind == kindNull
	}
	if a.Kind == b.Kind {
		switch a.Kind {
		case kindBool:
			return a.Data.(bool) == b.Data.(bool)
		case kindNumber:
			return a.Data.(float64) == b.Data.(float64)
		case kindString:
			return a.Data.(string) == b.Data.(string)
		}
		// Object / array equality is undefined territory in the spike.
		return false
	}
	// Cross-type: coerce to number when one side is number-ish.
	if an, ok := toNumber(a); ok {
		if bn, ok := toNumber(b); ok {
			return an == bn
		}
	}
	return false
}

func toNumber(v Value) (float64, bool) {
	switch v.Kind {
	case kindNumber:
		return v.Data.(float64), true
	case kindBool:
		if v.Data.(bool) {
			return 1, true
		}
		return 0, true
	case kindString:
		s := v.Data.(string)
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
