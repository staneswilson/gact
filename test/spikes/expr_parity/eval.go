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
	switch n.Kind {
	case nString:
		return stringValue(n.Str), nil
	case nNumber:
		return numberValue(n.Num), nil
	case nBool:
		return boolValue(n.Bool), nil
	case nNull:
		return nullValue(), nil
	case nIdent:
		// Root identifier — look up in the context root. Missing root names
		// surface as null, matching GH which treats `undefined.thing` as null
		// in short-circuit positions.
		if v, ok := c.ctxRoot[n.Str]; ok {
			return fromAny(v), nil
		}
		return nullValue(), nil
	case nMember:
		base, err := c.eval(n.Children[0])
		if err != nil {
			return Value{}, err
		}
		return lookupField(base, n.Str), nil
	case nIndex:
		base, err := c.eval(n.Children[0])
		if err != nil {
			return Value{}, err
		}
		idx, err := c.eval(n.Children[1])
		if err != nil {
			return Value{}, err
		}
		return lookupIndex(base, idx), nil
	case nNot:
		v, err := c.eval(n.Children[0])
		if err != nil {
			return Value{}, err
		}
		return boolValue(!v.truthy()), nil
	case nAnd:
		// GH's `&&` returns LHS if falsy, otherwise RHS (JS-style). This is
		// what enables the `matrix.timeout || 30` default-value pattern.
		left, err := c.eval(n.Children[0])
		if err != nil {
			return Value{}, err
		}
		if !left.truthy() {
			return left, nil
		}
		return c.eval(n.Children[1])
	case nOr:
		left, err := c.eval(n.Children[0])
		if err != nil {
			return Value{}, err
		}
		if left.truthy() {
			return left, nil
		}
		return c.eval(n.Children[1])
	case nEq:
		l, err := c.eval(n.Children[0])
		if err != nil {
			return Value{}, err
		}
		r, err := c.eval(n.Children[1])
		if err != nil {
			return Value{}, err
		}
		return boolValue(equal(l, r)), nil
	case nNeq:
		l, err := c.eval(n.Children[0])
		if err != nil {
			return Value{}, err
		}
		r, err := c.eval(n.Children[1])
		if err != nil {
			return Value{}, err
		}
		return boolValue(!equal(l, r)), nil
	case nCall:
		return c.evalCall(n.Str, n.Children)
	}
	return Value{}, fmt.Errorf("eval: unknown node kind %d", n.Kind)
}

func lookupField(base Value, name string) Value {
	if base.Kind != kindObject {
		return nullValue()
	}
	m := base.Data.(map[string]any)
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
		return lookupField(base, idx.Data.(string))
	case kindArray:
		n, ok := toNumber(idx)
		if !ok {
			return nullValue()
		}
		arr := base.Data.([]any)
		i := int(n)
		if i < 0 || i >= len(arr) {
			return nullValue()
		}
		return fromAny(arr[i])
	}
	return nullValue()
}

func (c *evalCtx) evalCall(name string, args []*node) (Value, error) {
	// Evaluate all arguments up front. None of the supported functions
	// short-circuit, so this is correct and simpler than per-function deferral.
	vals := make([]Value, len(args))
	for i, a := range args {
		v, err := c.eval(a)
		if err != nil {
			return Value{}, err
		}
		vals[i] = v
	}
	switch strings.ToLower(name) {
	case "contains":
		return fnContains(vals)
	case "startswith":
		return fnStartsWith(vals)
	case "endswith":
		return fnEndsWith(vals)
	case "format":
		return fnFormat(vals)
	case "join":
		return fnJoin(vals)
	case "tojson":
		return fnToJSON(vals)
	case "fromjson":
		return fnFromJSON(vals)
	case "success":
		return boolValue(c.statusName() == "success"), nil
	case "failure":
		return boolValue(c.statusName() == "failure"), nil
	case "always":
		return boolValue(true), nil
	case "cancelled":
		return boolValue(c.statusName() == "cancelled"), nil
	case "hashfiles":
		// Excluded by design — see README.
		return Value{}, fmt.Errorf("eval: hashFiles is not supported in the spike")
	}
	return Value{}, fmt.Errorf("eval: unknown function %q", name)
}

// ---- function implementations -------------------------------------------

func fnContains(args []Value) (Value, error) {
	if len(args) != 2 {
		return Value{}, fmt.Errorf("contains: want 2 args, got %d", len(args))
	}
	hay, needle := args[0], args[1]
	switch hay.Kind {
	case kindArray:
		for _, item := range hay.Data.([]any) {
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
	tmpl := args[0].Data.(string)
	// GH's format() uses {0} {1} ... placeholders. Escapes are {{ and }} for
	// literal braces. We implement the basic substitution; the corpus avoids
	// pathological escape cases.
	var b strings.Builder
	i := 0
	for i < len(tmpl) {
		c := tmpl[i]
		if c == '{' {
			if i+1 < len(tmpl) && tmpl[i+1] == '{' {
				b.WriteByte('{')
				i += 2
				continue
			}
			// parse the digits
			j := i + 1
			for j < len(tmpl) && tmpl[j] >= '0' && tmpl[j] <= '9' {
				j++
			}
			if j == i+1 || j >= len(tmpl) || tmpl[j] != '}' {
				return Value{}, fmt.Errorf("format: bad placeholder near pos %d", i)
			}
			n, _ := strconv.Atoi(tmpl[i+1 : j])
			argIdx := n + 1 // args[0] is the template
			if argIdx >= len(args) {
				return Value{}, fmt.Errorf("format: placeholder {%d} has no argument", n)
			}
			b.WriteString(args[argIdx].asString())
			i = j + 1
			continue
		}
		if c == '}' && i+1 < len(tmpl) && tmpl[i+1] == '}' {
			b.WriteByte('}')
			i += 2
			continue
		}
		b.WriteByte(c)
		i++
	}
	return stringValue(b.String()), nil
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
	arr := args[0].Data.([]any)
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
	if err := json.Unmarshal([]byte(args[0].Data.(string)), &v); err != nil {
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
		return v.Data.(bool)
	case kindNumber:
		return v.Data.(float64)
	case kindString:
		return v.Data.(string)
	case kindObject:
		out := map[string]any{}
		for k, raw := range v.Data.(map[string]any) {
			out[k] = toJSONable(fromAny(raw))
		}
		return out
	case kindArray:
		arr := v.Data.([]any)
		out := make([]any, len(arr))
		for i, raw := range arr {
			out[i] = toJSONable(fromAny(raw))
		}
		return out
	}
	return nil
}

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
	case []any:
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
	return "", fmt.Errorf("canonicalEncode: unsupported type %T", v)
}
