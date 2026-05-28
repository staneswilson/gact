package expr

import (
	"fmt"
	"strings"
)

// Collection-oriented GH expression built-ins. Today this is just join();
// future additions (e.g. when GH ships them) belong here so the cluster
// stays coherent.
//
// GH's join() accepts either an array or a single string for the first
// argument; the optional second argument is the separator and defaults to
// ',' when only one argument is supplied.

func init() {
	register("join", joinFunc)
}

// joinFunc stringifies array elements via AsString and concatenates them
// with sep. If args[0] is a string (not an array), GH treats it as a
// single element — joining produces that string unchanged. Null elements
// become the empty string, matching AsString's contract.
func joinFunc(args []value) (value, error) {
	if len(args) < 1 || len(args) > 2 {
		return value{}, fmt.Errorf("join: expected 1 or 2 arguments, got %d", len(args))
	}
	sep := ","
	if len(args) == 2 {
		sep = args[1].AsString()
	}

	first := args[0]
	switch first.Kind {
	case kindArray:
		arr, _ := first.Data.([]any)
		parts := make([]string, len(arr))
		for i, el := range arr {
			parts[i] = fromAny(el).AsString()
		}
		return stringValue(strings.Join(parts, sep)), nil
	default:
		// Single-element coercion: any non-array (string, number, bool,
		// null, object) is treated as one element. No separator can ever
		// appear in the output of a single-element join.
		return stringValue(first.AsString()), nil
	}
}
