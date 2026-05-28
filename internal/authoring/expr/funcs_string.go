package expr

import (
	"fmt"
	"strings"
)

// String-oriented GH expression built-ins: contains, startsWith, endsWith,
// and format. Each is registered with the shared funcRegistry at init() so
// the evaluator can dispatch on name without per-package wiring.
//
// All four mirror the documented GitHub Actions semantics; deliberate quirks
// (e.g. contains('x','') returning true) are pinned by tests in
// funcs_test.go so that future refactors do not silently drift.

func init() {
	register("contains", containsFunc)
	register("startsWith", startsWithFunc)
	register("endsWith", endsWithFunc)
	register("format", formatFunc)
}

// containsFunc dispatches on the first argument's kind. A string haystack
// triggers substring search; an array haystack triggers element-equality
// search using Value.Equal so the comparison rules match the rest of the
// evaluator. Any other kind yields false rather than an error — GH is
// permissive here, and a hard error would break expressions that probe
// soft-lookup nulls (e.g. `contains(some.missing, 'x')`).
func containsFunc(args []value) (value, error) {
	if len(args) != 2 {
		return value{}, fmt.Errorf("contains: expected 2 arguments, got %d", len(args))
	}
	haystack := args[0]
	needle := args[1]
	switch haystack.Kind {
	case kindString:
		// GH treats empty needle as always present, including in the empty
		// string. strings.Contains already returns true for an empty
		// substring in any string, so this falls out for free — but we
		// document the behaviour because it is the one place GH diverges
		// from typical "in" semantics.
		hs, _ := haystack.Data.(string)
		return boolValue(strings.Contains(hs, needle.AsString())), nil
	case kindArray:
		arr, _ := haystack.Data.([]any)
		for _, el := range arr {
			if fromAny(el).Equal(needle) {
				return boolValue(true), nil
			}
		}
		return boolValue(false), nil
	}
	return boolValue(false), nil
}

// startsWithFunc is case-sensitive per GH. Both arguments are stringified
// through AsString so callers can pass numbers or booleans (e.g.
// `startsWith(github.run_number, '1')`) without manual coercion.
func startsWithFunc(args []value) (value, error) {
	if len(args) != 2 {
		return value{}, fmt.Errorf("startsWith: expected 2 arguments, got %d", len(args))
	}
	return boolValue(strings.HasPrefix(args[0].AsString(), args[1].AsString())), nil
}

// endsWithFunc mirrors startsWithFunc on the suffix side. Same case rules,
// same coercion via AsString.
func endsWithFunc(args []value) (value, error) {
	if len(args) != 2 {
		return value{}, fmt.Errorf("endsWith: expected 2 arguments, got %d", len(args))
	}
	return boolValue(strings.HasSuffix(args[0].AsString(), args[1].AsString())), nil
}

// formatFunc implements GH's `format(template, args...)`:
//   - {N} interpolates args[N].AsString()
//   - {{ is a literal '{'; }} is a literal '}'
//   - Unmatched braces or out-of-range indices are errors so workflow
//     authors get fast feedback instead of a silent miss-render.
//
// We hand-roll the parser rather than using fmt.Sprintf — the brace-escape
// rules are GH-specific and the indices reference an external slice, not
// positional verbs.
func formatFunc(args []value) (value, error) {
	if len(args) == 0 {
		return value{}, fmt.Errorf("format: expected at least 1 argument, got 0")
	}
	template := args[0].AsString()
	params := args[1:]

	var out strings.Builder
	out.Grow(len(template))

	i := 0
	for i < len(template) {
		c := template[i]
		switch c {
		case '{':
			// '{{' escapes a literal '{'. Any other '{' starts a {N} ref.
			if i+1 < len(template) && template[i+1] == '{' {
				out.WriteByte('{')
				i += 2
				continue
			}
			// Find the matching '}' and parse the index.
			end := strings.IndexByte(template[i+1:], '}')
			if end < 0 {
				return value{}, fmt.Errorf("format: unmatched '{' at position %d", i)
			}
			idxStr := template[i+1 : i+1+end]
			if idxStr == "" {
				return value{}, fmt.Errorf("format: empty placeholder at position %d", i)
			}
			idx, err := parseFormatIndex(idxStr)
			if err != nil {
				return value{}, fmt.Errorf("format: invalid placeholder %q at position %d: %w", "{"+idxStr+"}", i, err)
			}
			if idx < 0 || idx >= len(params) {
				return value{}, fmt.Errorf("format: placeholder {%d} out of range (have %d args)", idx, len(params))
			}
			out.WriteString(params[idx].AsString())
			i += 2 + end // step past '{', idxStr, '}'
		case '}':
			// A lone '}' is only legal as '}}'.
			if i+1 < len(template) && template[i+1] == '}' {
				out.WriteByte('}')
				i += 2
				continue
			}
			return value{}, fmt.Errorf("format: unmatched '}' at position %d", i)
		default:
			out.WriteByte(c)
			i++
		}
	}
	return stringValue(out.String()), nil
}

// parseFormatIndex parses a non-negative integer placeholder. We do this
// by hand rather than via strconv.Atoi because GH only accepts ASCII
// digits — '+', '-', whitespace, or anything non-digit is rejected so the
// error message is consistent with the rest of formatFunc.
func parseFormatIndex(s string) (int, error) {
	n := 0
	for _, r := range s {
		if r < '0' || r > '9' {
			return 0, fmt.Errorf("non-digit %q in placeholder index", r)
		}
		n = n*10 + int(r-'0')
	}
	return n, nil
}
