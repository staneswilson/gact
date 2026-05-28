// Note: "eval" here is an AST-walking interpreter for the GitHub Actions
// expression grammar — NOT a call into any language `eval()` runtime. We do not
// invoke `os/exec`, reflection, or any dynamic Go-code execution. Function
// dispatch is via an explicit allow-list (contains, startsWith, endsWith,
// format, join, toJSON, fromJSON, success, failure, always, cancelled);
// unknown identifiers fail closed with an error.
package main

import (
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// evalCtx carries the per-evaluation state. The corpus expression's "context"
// field is decoded into a map[string]any and stored on ctxRoot; status() and
// friends read from the "status" key.
type evalCtx struct {
	ctxRoot map[string]any
}

func newCtx(raw map[string]any) *evalCtx {
	if raw == nil {
		raw = map[string]any{}
	}
	return &evalCtx{ctxRoot: raw}
}

func (c *evalCtx) statusName() string {
	s, _ := c.ctxRoot["status"].(string)
	return s
}

func (c *evalCtx) eval(n *node) (Value, error) {
	if v, ok := evalLiteral(n); ok {
		return v, nil
	}
	switch n.Kind {
	case nIdent:
		return c.evalIdent(n), nil
	case nMember:
		return c.evalMember(n)
	case nIndex:
		return c.evalIndex(n)
	case nNot:
		return c.evalNot(n)
	case nAnd:
		return c.evalAnd(n)
	case nOr:
		return c.evalOr(n)
	case nEq:
		return c.evalBinary(n, func(l, r Value) Value { return boolValue(equal(l, r)) })
	case nNeq:
		return c.evalBinary(n, func(l, r Value) Value { return boolValue(!equal(l, r)) })
	case nCall:
		return c.evalCall(n.Str, n.Children)
	}
	return Value{}, fmt.Errorf("eval: unknown node kind %d", n.Kind)
}

// evalLiteral returns the immediate value for the leaf literal node kinds
// (string, number, bool, null). The second return is false for any non-leaf
// kind, signalling that the caller should fall through to the operator
// dispatch. Pulling these out keeps eval below the gocyclo threshold.
func evalLiteral(n *node) (Value, bool) {
	switch n.Kind {
	case nString:
		return stringValue(n.Str), true
	case nNumber:
		return numberValue(n.Num), true
	case nBool:
		return boolValue(n.Bool), true
	case nNull:
		return nullValue(), true
	}
	return Value{}, false
}

// evalIdent resolves a root identifier (github, runner, matrix, …) against
// the context root. Missing names surface as null, matching GH which
// treats `undefined.thing` as null in short-circuit positions.
func (c *evalCtx) evalIdent(n *node) Value {
	if v, ok := c.ctxRoot[n.Str]; ok {
		return fromAny(v)
	}
	return nullValue()
}

// evalMember evaluates `base.field`, returning null when the base is not
// an object or the field is absent.
func (c *evalCtx) evalMember(n *node) (Value, error) {
	base, err := c.eval(n.Children[0])
	if err != nil {
		return Value{}, err
	}
	return lookupField(base, n.Str), nil
}

// evalIndex evaluates `base[idx]` for both object and array bases.
func (c *evalCtx) evalIndex(n *node) (Value, error) {
	base, err := c.eval(n.Children[0])
	if err != nil {
		return Value{}, err
	}
	idx, err := c.eval(n.Children[1])
	if err != nil {
		return Value{}, err
	}
	return lookupIndex(base, idx), nil
}

// evalNot evaluates `!x`, coercing through truthy().
func (c *evalCtx) evalNot(n *node) (Value, error) {
	v, err := c.eval(n.Children[0])
	if err != nil {
		return Value{}, err
	}
	return boolValue(!v.truthy()), nil
}

// evalAnd implements GH's short-circuit `&&`: returns LHS if falsy,
// otherwise evaluates and returns RHS. This is the behaviour that lets
// `matrix.timeout && something` propagate the falsy LHS.
func (c *evalCtx) evalAnd(n *node) (Value, error) {
	left, err := c.eval(n.Children[0])
	if err != nil {
		return Value{}, err
	}
	if !left.truthy() {
		return left, nil
	}
	return c.eval(n.Children[1])
}

// evalOr implements GH's short-circuit `||`: returns LHS if truthy,
// otherwise evaluates and returns RHS. This is what enables the
// `matrix.timeout || 30` default-value pattern.
func (c *evalCtx) evalOr(n *node) (Value, error) {
	left, err := c.eval(n.Children[0])
	if err != nil {
		return Value{}, err
	}
	if left.truthy() {
		return left, nil
	}
	return c.eval(n.Children[1])
}

// evalBinary evaluates both child operands strictly (no short-circuit)
// and folds them with op. Shared by nEq and nNeq so the err-check ladder
// is written once.
func (c *evalCtx) evalBinary(n *node, op func(Value, Value) Value) (Value, error) {
	l, err := c.eval(n.Children[0])
	if err != nil {
		return Value{}, err
	}
	r, err := c.eval(n.Children[1])
	if err != nil {
		return Value{}, err
	}
	return op(l, r), nil
}

func lookupField(base Value, name string) Value {
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

func lookupIndex(base Value, idx Value) Value {
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

// callImpl is the dispatch signature for the spike's built-in functions.
// Status-aware functions (success/failure/cancelled) need access to the
// evalCtx; pure ones ignore it. Pulling them into a single shape keeps
// evalCall a flat table lookup.
type callImpl func(*evalCtx, []Value) (Value, error)

// pureFn lifts a stateless function into a callImpl by ignoring the
// context. Most built-ins land here.
func pureFn(f func([]Value) (Value, error)) callImpl {
	return func(_ *evalCtx, v []Value) (Value, error) { return f(v) }
}

// statusFn answers "is the current job status equal to want?" using the
// evalCtx-supplied status string. Shared by success/failure/cancelled.
func statusFn(want string) callImpl {
	return func(c *evalCtx, _ []Value) (Value, error) {
		return boolValue(c.statusName() == want), nil
	}
}

// callRegistry resolves function names (lowercased) to their callImpl.
// Keeping this as a package-level table means evalCall is a simple
// "evaluate args, look up, dispatch" and its cyclo stays well under the
// threshold even as we add more built-ins.
var callRegistry = map[string]callImpl{
	"contains":   pureFn(fnContains),
	"startswith": pureFn(fnStartsWith),
	"endswith":   pureFn(fnEndsWith),
	"format":     pureFn(fnFormat),
	"join":       pureFn(fnJoin),
	"tojson":     pureFn(fnToJSON),
	"fromjson":   pureFn(fnFromJSON),
	"success":    statusFn("success"),
	"failure":    statusFn("failure"),
	"cancelled":  statusFn("cancelled"),
	"always":     func(*evalCtx, []Value) (Value, error) { return boolValue(true), nil },
	"hashfiles": func(*evalCtx, []Value) (Value, error) {
		// Excluded by design — see README.
		return Value{}, fmt.Errorf("eval: hashFiles is not supported in the spike")
	},
}

func (c *evalCtx) evalCall(name string, args []*node) (Value, error) {
	vals, err := c.evalArgs(args)
	if err != nil {
		return Value{}, err
	}
	fn, ok := callRegistry[strings.ToLower(name)]
	if !ok {
		return Value{}, fmt.Errorf("eval: unknown function %q", name)
	}
	return fn(c, vals)
}

// evalArgs eagerly evaluates each argument. None of the supported
// functions short-circuit, so this is correct and simpler than per-
// function deferral. A nil-arg call returns an empty slice, not nil,
// so downstream functions can range without a guard.
func (c *evalCtx) evalArgs(args []*node) ([]Value, error) {
	vals := make([]Value, len(args))
	for i, a := range args {
		v, err := c.eval(a)
		if err != nil {
			return nil, err
		}
		vals[i] = v
	}
	return vals, nil
}

// ---- function implementations -------------------------------------------

func fnContains(args []Value) (Value, error) {
	if len(args) != 2 {
		return Value{}, fmt.Errorf("contains: want 2 args, got %d", len(args))
	}
	hay, needle := args[0], args[1]
	switch hay.Kind {
	case kindArray:
		arr, _ := hay.Data.([]any)
		for _, item := range arr {
			if equal(fromAny(item), needle) {
				return boolValue(true), nil
			}
		}
		return boolValue(false), nil
	default:
		return boolValue(strings.Contains(hay.asString(), needle.asString())), nil
	}
}

func fnStartsWith(args []Value) (Value, error) {
	if len(args) != 2 {
		return Value{}, fmt.Errorf("startsWith: want 2 args, got %d", len(args))
	}
	return boolValue(strings.HasPrefix(args[0].asString(), args[1].asString())), nil
}

func fnEndsWith(args []Value) (Value, error) {
	if len(args) != 2 {
		return Value{}, fmt.Errorf("endsWith: want 2 args, got %d", len(args))
	}
	return boolValue(strings.HasSuffix(args[0].asString(), args[1].asString())), nil
}

func fnFormat(args []Value) (Value, error) {
	if len(args) == 0 {
		return Value{}, fmt.Errorf("format: missing template arg")
	}
	if args[0].Kind != kindString {
		return Value{}, fmt.Errorf("format: template must be string, got %s", args[0].kindName())
	}
	tmpl, _ := args[0].Data.(string)
	// GH's format() uses {0} {1} ... placeholders. Escapes are {{ and }} for
	// literal braces. We implement the basic substitution; the corpus avoids
	// pathological escape cases. Per-byte dispatch lives in formatStep so this
	// driver stays a simple cursor loop.
	var b strings.Builder
	i := 0
	for i < len(tmpl) {
		next, err := formatStep(tmpl, i, args, &b)
		if err != nil {
			return Value{}, err
		}
		i = next
	}
	return stringValue(b.String()), nil
}

// formatStep advances one logical lexeme of the format template at index i,
// writing the result into b and returning the next index. The three branches
// are: doubled '{{' / '}}' escapes, a '{N}' placeholder, and any other byte
// as a literal. A lone '}' is intentionally treated as a literal byte to
// match the spike's prior behaviour (production code is stricter).
func formatStep(tmpl string, i int, args []Value, b *strings.Builder) (int, error) {
	c := tmpl[i]
	if c == '{' {
		if isDoubled(tmpl, i, '{') {
			b.WriteByte('{')
			return i + 2, nil
		}
		return writeFormatPlaceholder(tmpl, i, args, b)
	}
	if c == '}' && isDoubled(tmpl, i, '}') {
		b.WriteByte('}')
		return i + 2, nil
	}
	b.WriteByte(c)
	return i + 1, nil
}

// writeFormatPlaceholder parses a "{N}" placeholder starting at tmpl[i] and
// writes the stringified Nth argument. It returns the index just past the
// closing '}'. Errors surface as "format: bad placeholder…" / "format:
// placeholder {N} has no argument", matching the inline behaviour before the
// split.
func writeFormatPlaceholder(tmpl string, i int, args []Value, b *strings.Builder) (int, error) {
	j := i + 1
	for j < len(tmpl) && tmpl[j] >= '0' && tmpl[j] <= '9' {
		j++
	}
	if j == i+1 || j >= len(tmpl) || tmpl[j] != '}' {
		return 0, fmt.Errorf("format: bad placeholder near pos %d", i)
	}
	n, _ := strconv.Atoi(tmpl[i+1 : j])
	argIdx := n + 1 // args[0] is the template
	if argIdx >= len(args) {
		return 0, fmt.Errorf("format: placeholder {%d} has no argument", n)
	}
	b.WriteString(args[argIdx].asString())
	return j + 1, nil
}

// isDoubled reports whether tmpl[i+1] also equals c — used to detect the
// '{{' and '}}' brace-escape sequences without repeating the bounds check.
func isDoubled(tmpl string, i int, c byte) bool {
	return i+1 < len(tmpl) && tmpl[i+1] == c
}

func fnJoin(args []Value) (Value, error) {
	if len(args) < 1 || len(args) > 2 {
		return Value{}, fmt.Errorf("join: want 1 or 2 args, got %d", len(args))
	}
	sep := ","
	if len(args) == 2 {
		sep = args[1].asString()
	}
	if args[0].Kind != kindArray {
		// GH coerces a single value to a one-element array.
		return stringValue(args[0].asString()), nil
	}
	arr, _ := args[0].Data.([]any)
	parts := make([]string, len(arr))
	for i, item := range arr {
		parts[i] = fromAny(item).asString()
	}
	return stringValue(strings.Join(parts, sep)), nil
}

func fnToJSON(args []Value) (Value, error) {
	if len(args) != 1 {
		return Value{}, fmt.Errorf("toJSON: want 1 arg, got %d", len(args))
	}
	s, err := canonicalJSON(args[0])
	if err != nil {
		return Value{}, err
	}
	return stringValue(s), nil
}

func fnFromJSON(args []Value) (Value, error) {
	if len(args) != 1 {
		return Value{}, fmt.Errorf("fromJSON: want 1 arg, got %d", len(args))
	}
	if args[0].Kind != kindString {
		return Value{}, fmt.Errorf("fromJSON: arg must be string, got %s", args[0].kindName())
	}
	var v any
	s, _ := args[0].Data.(string)
	if err := json.Unmarshal([]byte(s), &v); err != nil {
		return Value{}, fmt.Errorf("fromJSON: %w", err)
	}
	return fromAny(v), nil
}

// canonicalJSON produces a deterministic JSON encoding — object keys sorted —
// so the corpus can assert byte-for-byte equality.
func canonicalJSON(v Value) (string, error) {
	return canonicalEncode(toJSONable(v))
}

func toJSONable(v Value) any {
	switch v.Kind {
	case kindNull:
		return nil
	case kindBool:
		b, _ := v.Data.(bool)
		return b
	case kindNumber:
		n, _ := v.Data.(float64)
		return n
	case kindString:
		s, _ := v.Data.(string)
		return s
	case kindObject:
		out := map[string]any{}
		m, _ := v.Data.(map[string]any)
		for k, raw := range m {
			out[k] = toJSONable(fromAny(raw))
		}
		return out
	case kindArray:
		arr, _ := v.Data.([]any)
		out := make([]any, len(arr))
		for i, raw := range arr {
			out[i] = toJSONable(fromAny(raw))
		}
		return out
	}
	return nil
}

// canonicalEncode dispatches on the dynamic type of v. Container types delegate
// to canonicalEncodeMap / canonicalEncodeSlice so this function stays a flat
// type-switch and the per-container bookkeeping has one home each.
func canonicalEncode(v any) (string, error) {
	switch x := v.(type) {
	case nil:
		return "null", nil
	case bool:
		if x {
			return "true", nil
		}
		return "false", nil
	case float64:
		return formatNumber(x), nil
	case string:
		b, err := json.Marshal(x)
		if err != nil {
			return "", err
		}
		return string(b), nil
	case map[string]any:
		return canonicalEncodeMap(x)
	case []any:
		return canonicalEncodeSlice(x)
	}
	return "", fmt.Errorf("canonicalEncode: unsupported type %T", v)
}

// canonicalEncodeMap emits a JSON object with sorted string keys. Sorting
// is what makes the encoding deterministic across runs — Go's map iteration
// order is randomised, so the corpus would otherwise see diff-noise from
// toJSON of identical structures.
func canonicalEncodeMap(x map[string]any) (string, error) {
	keys := make([]string, 0, len(x))
	for k := range x {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	b.WriteByte('{')
	for i, k := range keys {
		if i > 0 {
			b.WriteByte(',')
		}
		kb, _ := json.Marshal(k)
		b.Write(kb)
		b.WriteByte(':')
		s, err := canonicalEncode(x[k])
		if err != nil {
			return "", err
		}
		b.WriteString(s)
	}
	b.WriteByte('}')
	return b.String(), nil
}

// canonicalEncodeSlice emits a JSON array in element order. Arrays already
// have a defined order in Go, so the only invariant we maintain here is
// using the same per-element canonicalEncode recursion as maps.
func canonicalEncodeSlice(x []any) (string, error) {
	var b strings.Builder
	b.WriteByte('[')
	for i, item := range x {
		if i > 0 {
			b.WriteByte(',')
		}
		s, err := canonicalEncode(item)
		if err != nil {
			return "", err
		}
		b.WriteString(s)
	}
	b.WriteByte(']')
	return b.String(), nil
}
