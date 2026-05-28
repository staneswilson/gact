// Package matrix expands a GitHub Actions strategy matrix into the concrete
// combinations a job will fan out into.
//
// # Semantics
//
// Expand mirrors GitHub's documented matrix evaluation order, summarised here
// to make the include/exclude quirks explicit:
//
//  1. Compute the cartesian product of Axes. Each combination is an ordered
//     row keyed by axis name.
//  2. Apply Exclude. An exclude row removes every cartesian-product combo it
//     matches; a subset of axes is allowed (the unspecified axes act as
//     wildcards). Excludes only operate on the cartesian product — they do
//     NOT prune combinations contributed by Include.
//  3. Apply Include. For each include row, in order:
//     a. The include EXTENDS every surviving combination whose original
//     axis values are not overwritten by the include's keys (i.e. for
//     each axis-named key in the include, the combo either lacks that
//     key, or the value matches). Original axis values are never
//     overwritten; values added by a prior include may be overwritten.
//     b. If the include extends at least one combination, it does NOT also
//     produce a standalone row.
//     c. If the include extends nothing (because at least one of its axis
//     keys would overwrite every combination), it is APPENDED as a new
//     standalone combination.
//
// The behaviour above is anchored in GitHub's official documentation:
// https://docs.github.com/en/actions/using-jobs/using-a-matrix-for-your-jobs
//
// # Ordering and determinism
//
// pkg/workflow stores Axes as a plain `map[string][]any`, whose iteration
// order is randomised by the Go runtime. Until the parser layer records an
// ordered axis representation (tracked as a follow-up — see plan §0.9), we
// sort axis names lexically inside Expand to pin down the outer-axis
// iteration order. This is the best deterministic substitute for declared
// order with the current type. Authors who depend on a specific outer-axis
// order should rename their axes accordingly until the parser surfaces
// insertion order.
package matrix

import (
	"reflect"
	"sort"

	wf "github.com/staneswilson/gact/pkg/workflow"
)

// Combination is a single concrete matrix row: a snapshot of all axis values
// (plus any keys added by include rows) for one fan-out of the job template.
type Combination map[string]any

// combo is the internal representation of an in-flight Combination plus the
// set of keys that were sourced from the cartesian product. Includes may not
// overwrite cartesian-product keys; tracking originals lets us enforce that
// without scanning the axis schema each time. The `fromInclude` flag marks
// combos that were appended by a previous include row: those rows are
// terminal and not eligible to be extended by subsequent includes, matching
// GitHub's rule that includes attach only to original matrix combinations.
type combo struct {
	values      Combination
	original    map[string]struct{}
	fromInclude bool
}

// Expand evaluates the matrix according to the documented semantics above
// and returns the resulting list of Combinations.
//
// An empty matrix (no axes, no includes, no excludes) yields exactly one
// empty combination so that the job still runs once — this mirrors GitHub's
// behaviour for a job with no `strategy:` block.
//
// Expand never returns an error in the current contract, but the signature
// reserves error-returning future cases (e.g. type-mismatched exclude rows
// once schema validation lands) without forcing a downstream signature
// change.
func Expand(m wf.Matrix) ([]Combination, error) {
	// Special case: no schema, no overrides. One empty run.
	if len(m.Axes) == 0 && len(m.Include) == 0 {
		return []Combination{{}}, nil
	}

	// 1) Cartesian product over Axes in lexical-name order.
	axisNames := sortedAxisNames(m.Axes)
	combos := cartesian(axisNames, m.Axes)

	// 2) Apply Exclude.
	combos = applyExcludes(combos, m.Exclude)

	// 3) Apply Include.
	combos = applyIncludes(combos, m.Include)

	return toCombinationList(combos), nil
}

// sortedAxisNames returns the keys of axes sorted lexically. See the package
// doc for the rationale: a Go map alone cannot preserve insertion order, and
// sorting yields the next-best deterministic ordering.
func sortedAxisNames(axes map[string][]any) []string {
	names := make([]string, 0, len(axes))
	for name := range axes {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// cartesian computes the cartesian product of the named axes, iterating the
// last axis fastest so that the outer (first) axis is the slowest dimension
// — matching the order a reader scans a multi-line YAML table top-to-bottom.
//
// When `axisNames` is empty, the cartesian product is empty (zero rows). The
// empty-matrix case (no axes AND no includes) is handled separately in
// Expand so that the job still runs once; here we keep the product literal
// so that `include`-only matrices correctly contribute one row per include.
func cartesian(axisNames []string, axes map[string][]any) []combo {
	if len(axisNames) == 0 {
		return nil
	}
	// Seed with a single empty combo and extend axis-by-axis.
	current := []combo{{values: Combination{}, original: map[string]struct{}{}}}
	for _, name := range axisNames {
		values := axes[name]
		// An axis with zero values collapses the entire product to empty,
		// matching GitHub's behaviour where an empty list yields no combos.
		if len(values) == 0 {
			return nil
		}
		next := make([]combo, 0, len(current)*len(values))
		for _, c := range current {
			for _, v := range values {
				nv := copyCombination(c.values)
				nv[name] = v
				no := copyKeySet(c.original)
				no[name] = struct{}{}
				next = append(next, combo{values: nv, original: no})
			}
		}
		current = next
	}
	return current
}

// applyExcludes drops every combo that matches at least one exclude row.
// An exclude row matches a combo when every key in the exclude row is
// present in the combo with an equal value. Unspecified keys are wildcards.
// An empty exclude row would match every combo; we treat it as a no-op to
// avoid silently zeroing the matrix, which is GitHub's behaviour as well
// (the YAML schema rejects empty exclude rows in practice).
func applyExcludes(combos []combo, excludes []map[string]any) []combo {
	if len(excludes) == 0 {
		return combos
	}
	out := combos[:0]
	for _, c := range combos {
		drop := false
		for _, ex := range excludes {
			if len(ex) == 0 {
				continue
			}
			if matchesAll(c.values, ex) {
				drop = true
				break
			}
		}
		if !drop {
			out = append(out, c)
		}
	}
	return out
}

// applyIncludes extends combos with include rows that don't overwrite
// original axis values, and appends include rows that cannot extend any
// surviving combo. See the package doc for the exact semantics. Combos
// previously appended by an include row are skipped during the extend
// scan: includes attach only to original cartesian-product combinations.
func applyIncludes(combos []combo, includes []map[string]any) []combo {
	for _, inc := range includes {
		extended := false
		for i := range combos {
			if combos[i].fromInclude {
				continue
			}
			if canExtend(combos[i], inc) {
				mergeInto(&combos[i], inc)
				extended = true
			}
		}
		if !extended {
			combos = append(combos, includeAsCombo(inc))
		}
	}
	return combos
}

// canExtend reports whether `inc` can be merged into `c` without overwriting
// any of c's original (axis-defined) values. Keys not in c are always safe
// to add. Keys present in c but added by a prior include may be overwritten
// (per GitHub's "added matrix values can be overwritten" rule) and so do
// not block extension.
func canExtend(c combo, inc map[string]any) bool {
	for k, v := range inc {
		existing, present := c.values[k]
		if !present {
			continue
		}
		if _, isOriginal := c.original[k]; !isOriginal {
			// k was contributed by a prior include — overwrite is allowed.
			continue
		}
		if !reflect.DeepEqual(existing, v) {
			return false
		}
	}
	return true
}

// mergeInto applies `inc` to `c.values`. Keys already in originals are only
// reached when their values already match (canExtend guarantees this), so
// the write is a no-op for those. Newly added keys are NOT marked as
// originals — they may be overwritten by later includes.
func mergeInto(c *combo, inc map[string]any) {
	for k, v := range inc {
		c.values[k] = v
	}
}

// includeAsCombo materialises an include row as a standalone combination.
// The result is marked as fromInclude so that later include rows do not
// silently fold their values into it. Per GitHub semantics, include rows
// produce one row each when they cannot extend an original combination.
func includeAsCombo(inc map[string]any) combo {
	values := make(Combination, len(inc))
	for k, v := range inc {
		values[k] = v
	}
	return combo{values: values, original: map[string]struct{}{}, fromInclude: true}
}

// matchesAll reports whether `want` is a subset of `have` by value. Every
// key in `want` must be present in `have` with an equal value.
func matchesAll(have Combination, want map[string]any) bool {
	for k, v := range want {
		hv, ok := have[k]
		if !ok {
			return false
		}
		if !reflect.DeepEqual(hv, v) {
			return false
		}
	}
	return true
}

// copyCombination returns a shallow copy of the combination. Values are
// interface{} and assumed to be immutable scalars from the parser; this is
// the same depth of copying the IR layer uses elsewhere.
func copyCombination(c Combination) Combination {
	out := make(Combination, len(c)+1)
	for k, v := range c {
		out[k] = v
	}
	return out
}

// copyKeySet duplicates a set of axis-original keys.
func copyKeySet(s map[string]struct{}) map[string]struct{} {
	out := make(map[string]struct{}, len(s)+1)
	for k := range s {
		out[k] = struct{}{}
	}
	return out
}

// toCombinationList strips internal bookkeeping before returning to callers.
// Returning the bare combinations keeps the public surface a plain
// `[]map[string]any`-shaped value, suitable for downstream scheduling layers.
func toCombinationList(combos []combo) []Combination {
	out := make([]Combination, len(combos))
	for i, c := range combos {
		out[i] = c.values
	}
	return out
}
