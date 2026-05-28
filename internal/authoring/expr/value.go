// Package expr is the internal implementation of gact's GitHub Actions
// expression evaluator. All public-facing types live in
// github.com/staneswilson/gact/pkg/expr — see ADR-003. This package
// imports the public types and provides the lexer, parser, AST,
// evaluator, and function registry.
//
// Files in this package:
//   - value.go    Value helpers (truthiness etc. live on pkg/expr.Value;
//     here we only host fromAny and a few constructors)
//   - lexer.go    hand-rolled tokenizer
//   - parser.go   recursive-descent parser producing a *node AST
//   - eval.go     AST walker (Evaluator's evalFn is its method)
//   - context.go  EmptyContext helper and minimal interface adapter
//   - funcs.go    function dispatch registry (empty until Task 0.7)
//   - register.go init() registers the compile function with pkg/expr
package expr

import (
	"strconv"

	pubexpr "github.com/staneswilson/gact/pkg/expr"
)

// Re-export of public Kind constants so call sites inside this package
// can keep using the short names without prefixing every reference.
// These are aliases, not separate constants — they MUST stay in lockstep
// with pkg/expr.
const (
	kindNull   = pubexpr.KindNull
	kindBool   = pubexpr.KindBool
	kindNumber = pubexpr.KindNumber
	kindString = pubexpr.KindString
	kindArray  = pubexpr.KindArray
	kindObject = pubexpr.KindObject
)

// Alias for the most commonly used public type so the internal code
// reads naturally without the pubexpr.<X> prefix at every site.
type value = pubexpr.Value

// Value constructors used by the evaluator and tests. The fmt of each
// matches the (Kind, Data) contract documented in pkg/expr.
func nullValue() value            { return value{Kind: kindNull} }
func boolValue(b bool) value      { return value{Kind: kindBool, Data: b} }
func numberValue(n float64) value { return value{Kind: kindNumber, Data: n} }
func stringValue(s string) value  { return value{Kind: kindString, Data: s} }
func arrayValue(a []any) value    { return value{Kind: kindArray, Data: a} }
func objectValue(m map[string]any) value {
	return value{Kind: kindObject, Data: m}
}

// fromAny converts a freshly-decoded JSON value (or any other
// Go-native shape used by contexts) into a Value. JSON numbers always
// arrive as float64; Go callers may pass int / int64 and we accept
// both. Anything unrecognised is stringified to avoid surprising
// downstream code with an unrepresentable kind.
func fromAny(v any) value {
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
	case value:
		// Already a Value — caller passed one through fromAny by
		// mistake or convenience. Return as-is.
		return x
	}
	return stringValue("")
}

// toNumber implements GH's numeric-coercion rule used by the relational
// operators (<, <=, >, >=). It is the same logic as pkg/expr's
// numberCoerce helper but inlined here for the relational path.
func toNumber(v value) (float64, bool) {
	switch v.Kind {
	case kindNumber:
		n, _ := v.Data.(float64)
		return n, true
	case kindBool:
		if b, _ := v.Data.(bool); b {
			return 1, true
		}
		return 0, true
	case kindString:
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
