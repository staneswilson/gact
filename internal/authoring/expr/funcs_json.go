package expr

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
)

// JSON-oriented GH expression built-ins: toJSON and fromJSON.
//
// toJSON renders a Value as canonical JSON with object keys sorted in
// lexicographic order. fromJSON parses a JSON string back into a Value.
// Together they form a lossless round-trip for any JSON-representable
// shape (null / bool / number / string / array / object); booleans and
// numbers survive cleanly because Value's underlying Go types
// (bool, float64) already match JSON's data model.
//
// Object keys: encoding/json sorts the keys of map[string]X automatically
// when marshalling, which gives us GH's "canonical" guarantee for free.
// We assert this property in tests so a future stdlib change cannot
// silently regress the contract.

func init() {
	register("toJSON", toJSONFunc)
	register("fromJSON", fromJSONFunc)
}

// toJSONFunc serialises a Value to a canonical JSON string. We unwrap to
// the underlying Go data so json.Marshal can do its normal thing — sorting
// map keys, escaping control characters, and emitting the smallest valid
// numeric representation.
//
// Null becomes the literal "null"; booleans become "true"/"false";
// numbers print without trailing zeros (json.Marshal uses
// strconv.FormatFloat under the hood with the same rule we apply in
// formatNumber).
func toJSONFunc(args []value) (value, error) {
	if len(args) != 1 {
		return value{}, fmt.Errorf("toJSON: expected 1 argument, got %d", len(args))
	}
	raw := unwrapForJSON(args[0])

	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(raw); err != nil {
		return value{}, fmt.Errorf("toJSON: %w", err)
	}
	// Encoder appends a trailing newline; strip it so the output is the
	// pure JSON document GH callers expect to template into other places.
	out := buf.Bytes()
	if n := len(out); n > 0 && out[n-1] == '\n' {
		out = out[:n-1]
	}
	return stringValue(string(out)), nil
}

// unwrapForJSON converts a Value into the Go-native shape json.Marshal
// expects. KindNumber's Data is already float64; KindString a string;
// arrays and objects already carry the right map/slice types. We walk
// nested arrays and objects so any embedded Value (which a hand-built
// context might supply) is unwrapped too.
func unwrapForJSON(v value) any {
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
	case kindArray:
		arr, _ := v.Data.([]any)
		out := make([]any, len(arr))
		for i, el := range arr {
			out[i] = unwrapAny(el)
		}
		return out
	case kindObject:
		m, _ := v.Data.(map[string]any)
		out := make(map[string]any, len(m))
		for k, el := range m {
			out[k] = unwrapAny(el)
		}
		return out
	}
	return nil
}

// unwrapAny is the recursion helper for unwrapForJSON. Most elements are
// already plain Go types; only when a Value sneaks into the data graph
// (e.g. from a context implementation) do we need to recurse.
func unwrapAny(x any) any {
	switch t := x.(type) {
	case value:
		return unwrapForJSON(t)
	case map[string]any:
		out := make(map[string]any, len(t))
		for k, el := range t {
			out[k] = unwrapAny(el)
		}
		return out
	case []any:
		out := make([]any, len(t))
		for i, el := range t {
			out[i] = unwrapAny(el)
		}
		return out
	}
	return x
}

// fromJSONFunc parses a JSON string into a Value. The argument is
// stringified via AsString so callers may pass either a literal string
// expression or a Value that happens to be a string. Invalid JSON yields
// a wrapped error mentioning the offending offset, which is more useful
// than a bare "invalid character".
func fromJSONFunc(args []value) (value, error) {
	if len(args) != 1 {
		return value{}, fmt.Errorf("fromJSON: expected 1 argument, got %d", len(args))
	}
	src := args[0].AsString()

	dec := json.NewDecoder(bytes.NewReader([]byte(src)))
	dec.UseNumber()
	var raw any
	if err := dec.Decode(&raw); err != nil {
		return value{}, fmt.Errorf("fromJSON: invalid JSON: %w", err)
	}
	// Reject trailing tokens so '{}garbage' is a hard error instead of
	// silently returning the leading object. Decoder.More() reports on
	// in-progress collections, not stream EOF, so we explicitly attempt
	// another decode and require io.EOF as the only acceptable result.
	var trail any
	if err := dec.Decode(&trail); !errors.Is(err, io.EOF) {
		if err != nil {
			return value{}, fmt.Errorf("fromJSON: unexpected trailing content: %w", err)
		}
		return value{}, fmt.Errorf("fromJSON: unexpected trailing content after JSON value")
	}
	return jsonToValue(raw), nil
}

// jsonToValue maps the json.Decoder's raw output (with UseNumber so we
// can detect integers vs floats) into the Value tree. JSON null becomes
// KindNull; everything else lands in its natural slot.
func jsonToValue(raw any) value {
	switch t := raw.(type) {
	case nil:
		return nullValue()
	case bool:
		return boolValue(t)
	case json.Number:
		// Always coerce numbers to float64 — that is Value's contract,
		// and round-tripping through formatNumber still prints integers
		// without trailing ".0".
		f, err := t.Float64()
		if err != nil {
			return stringValue(t.String())
		}
		return numberValue(f)
	case string:
		return stringValue(t)
	case []any:
		out := make([]any, len(t))
		for i, el := range t {
			out[i] = jsonToValue(el)
		}
		return arrayValue(out)
	case map[string]any:
		out := make(map[string]any, len(t))
		for k, el := range t {
			out[k] = jsonToValue(el)
		}
		return objectValue(out)
	}
	return nullValue()
}
