// Note: "eval" here is an AST-walking interpreter for the GitHub Actions
// expression grammar — NOT a call into any language `eval()` runtime.
// There is no reflection, no `os/exec`, no dynamic code execution.
// Function dispatch goes through the explicit registry in funcs.go so
// unknown identifiers fail closed.
package expr

import (
	"fmt"

	pubexpr "github.com/staneswilson/gact/pkg/expr"
)

// evaluator pairs a parsed AST with the static configuration needed to
// run it. We keep this type unexported because callers go through the
// public Evaluator (in pkg/expr) — see register.go for the wiring.
type evaluator struct {
	root *node
}

// compile parses src and returns an evaluator ready for repeated calls
// to evaluate. The function is exported (via the public package's
// RegisterCompiler hook) so pkg/expr.New can delegate here without
// importing internal/ directly.
func compile(src string) (*evaluator, error) {
	root, err := parseExpr(src)
	if err != nil {
		return nil, err
	}
	return &evaluator{root: root}, nil
}

// evaluate runs the AST against ctx. The returned Value uses the same
// (Kind, Data) contract as pkg/expr.Value because the alias chain in
// value.go makes them the same type.
func (e *evaluator) evaluate(c pubexpr.Context) (value, error) {
	if c == nil {
		c = EmptyContext()
	}
	return walk(e.root, c)
}

// walk is the AST visitor. It dispatches on node kind to dedicated
// per-kind helpers. The split between member-on-ident (a context lookup)
// and member-on-value (a field navigation) — see walkMember — is what
// makes `github.event.action` work without the context implementation
// knowing about nested paths.
func walk(n *node, c pubexpr.Context) (value, error) {
	if v, ok := walkAtom(n); ok {
		return v, nil
	}
	switch n.Kind {
	case nMember:
		return walkMember(n, c)
	case nIndex:
		return walkIndex(n, c)
	case nNot:
		return walkNot(n, c)
	case nAnd:
		return walkAnd(n, c)
	case nOr:
		return walkOr(n, c)
	case nEq, nNeq:
		return walkEquality(n, c)
	case nLt, nLte, nGt, nGte:
		return walkRelational(n, c)
	case nCall:
		return walkCall(n, c)
	}
	return value{}, fmt.Errorf("eval: unknown node kind %d", n.Kind)
}

// walkAtom handles AST nodes whose value depends only on the node itself,
// not on the evaluation context. A bare identifier at the root surfaces as
// Null because GH expressions usually pair identifiers with a `.field`;
// `${{ github }}` alone yields the empty string in templates, which Null
// serialises to.
func walkAtom(n *node) (value, bool) {
	switch n.Kind {
	case nString:
		return stringValue(n.Str), true
	case nNumber:
		return numberValue(n.Num), true
	case nBool:
		return boolValue(n.Bool), true
	case nNull, nIdent:
		return nullValue(), true
	}
	return value{}, false
}

// walkMember handles `.field` access. If the LHS is a bare identifier we
// route to the context's Get(scope, key) hook; otherwise we walk the LHS
// to a value and read the field off it.
func walkMember(n *node, c pubexpr.Context) (value, error) {
	child := n.Children[0]
	if child.Kind == nIdent {
		v, err := c.Get(child.Str, n.Str)
		if err != nil {
			return value{}, err
		}
		return v, nil
	}
	base, err := walk(child, c)
	if err != nil {
		return value{}, err
	}
	return lookupField(base, n.Str), nil
}

// walkIndex handles `[...]` access on arrays (numeric index) and objects
// (string key). Out-of-range indices and missing keys yield Null.
func walkIndex(n *node, c pubexpr.Context) (value, error) {
	base, err := walk(n.Children[0], c)
	if err != nil {
		return value{}, err
	}
	idx, err := walk(n.Children[1], c)
	if err != nil {
		return value{}, err
	}
	return lookupIndex(base, idx), nil
}

// walkNot evaluates `!expr` using GH's truthiness rules.
func walkNot(n *node, c pubexpr.Context) (value, error) {
	v, err := walk(n.Children[0], c)
	if err != nil {
		return value{}, err
	}
	return boolValue(!v.IsTruthy()), nil
}

// walkAnd implements GH's `&&` short-circuit: returns the LHS unchanged
// when it is falsy, otherwise evaluates and returns the RHS. The unchanged
// LHS lets `false && undefined.thing` succeed without evaluating the RHS.
func walkAnd(n *node, c pubexpr.Context) (value, error) {
	left, err := walk(n.Children[0], c)
	if err != nil {
		return value{}, err
	}
	if !left.IsTruthy() {
		return left, nil
	}
	return walk(n.Children[1], c)
}

// walkOr implements GH's `||` short-circuit: returns the LHS unchanged
// when it is truthy, otherwise evaluates and returns the RHS. The
// default-value idiom `matrix.timeout || 30` relies on the RHS being
// returned as-is — not coerced to a bool.
func walkOr(n *node, c pubexpr.Context) (value, error) {
	left, err := walk(n.Children[0], c)
	if err != nil {
		return value{}, err
	}
	if left.IsTruthy() {
		return left, nil
	}
	return walk(n.Children[1], c)
}

// walkEquality evaluates `==` and `!=`. Equality semantics live in
// value.Equal — this helper only wires up evaluation of both sides and
// inverts the result for `!=`.
func walkEquality(n *node, c pubexpr.Context) (value, error) {
	l, err := walk(n.Children[0], c)
	if err != nil {
		return value{}, err
	}
	r, err := walk(n.Children[1], c)
	if err != nil {
		return value{}, err
	}
	eq := l.Equal(r)
	if n.Kind == nNeq {
		eq = !eq
	}
	return boolValue(eq), nil
}

// walkCall evaluates every argument and dispatches to the function
// registry by name.
func walkCall(n *node, c pubexpr.Context) (value, error) {
	args := make([]value, len(n.Children))
	for i, a := range n.Children {
		v, err := walk(a, c)
		if err != nil {
			return value{}, err
		}
		args[i] = v
	}
	return callFunction(n.Str, args)
}

// walkRelational evaluates <, <=, >, >= by coercing both sides to
// numbers per GH semantics. If either side cannot be coerced, the
// comparison is false — this matches the spec's behaviour where
// comparing non-numeric strings yields false rather than an error.
func walkRelational(n *node, c pubexpr.Context) (value, error) {
	l, err := walk(n.Children[0], c)
	if err != nil {
		return value{}, err
	}
	r, err := walk(n.Children[1], c)
	if err != nil {
		return value{}, err
	}
	ln, lok := toNumber(l)
	rn, rok := toNumber(r)
	if !lok || !rok {
		return boolValue(false), nil
	}
	switch n.Kind {
	case nLt:
		return boolValue(ln < rn), nil
	case nLte:
		return boolValue(ln <= rn), nil
	case nGt:
		return boolValue(ln > rn), nil
	case nGte:
		return boolValue(ln >= rn), nil
	}
	return value{}, fmt.Errorf("eval: bad relational kind %d", n.Kind)
}

// lookupField reads a field off an object Value. Anything that is not an
// object — including arrays — yields Null. Missing keys also yield Null,
// matching GH's "soft" lookup; the static lint pass produces warnings
// via a different code path.
func lookupField(base value, name string) value {
	if base.Kind != kindObject {
		return nullValue()
	}
	m, _ := base.Data.(map[string]any)
	v, ok := m[name]
	if !ok {
		return nullValue()
	}
	return fromAny(v)
}

// lookupIndex reads an element off an array (by integer index) or an
// object (by string key). Anything else, or an out-of-range index, is
// Null.
func lookupIndex(base value, idx value) value {
	switch base.Kind {
	case kindObject:
		if idx.Kind != kindString {
			return nullValue()
		}
		s, _ := idx.Data.(string)
		return lookupField(base, s)
	case kindArray:
		n, ok := toNumber(idx)
		if !ok {
			return nullValue()
		}
		arr, _ := base.Data.([]any)
		i := int(n)
		if i < 0 || i >= len(arr) {
			return nullValue()
		}
		return fromAny(arr[i])
	}
	return nullValue()
}
