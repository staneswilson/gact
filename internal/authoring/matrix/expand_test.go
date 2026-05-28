// Package matrix_test exercises the public Expand contract.
//
// Tests live in the _test package so they only touch the exported surface.
// Cases mirror examples from GitHub's matrix documentation
// (https://docs.github.com/en/actions/using-jobs/using-a-matrix-for-your-jobs)
// so that quirky semantics (include-extends, exclude-then-include order) are
// pinned down by name.
package matrix_test

import (
	"reflect"
	"testing"

	"github.com/staneswilson/gact/internal/authoring/matrix"
	wf "github.com/staneswilson/gact/pkg/workflow"
)

// TestMatrix_Expand_SimpleCartesian: 2 axes of 2 each → 4 combos in declared
// order. Because axis names are sorted lexically (documented gap until the
// parser tracks insertion order), the outer axis is "os" before "version".
func TestMatrix_Expand_SimpleCartesian(t *testing.T) {
	m := wf.Matrix{
		Axes: map[string][]any{
			"os":      {"linux", "darwin"},
			"version": {"1.20", "1.21"},
		},
	}
	got, err := matrix.Expand(m)
	if err != nil {
		t.Fatalf("Expand returned error: %v", err)
	}
	want := []matrix.Combination{
		{"os": "linux", "version": "1.20"},
		{"os": "linux", "version": "1.21"},
		{"os": "darwin", "version": "1.20"},
		{"os": "darwin", "version": "1.21"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Expand cartesian:\n got  %v\n want %v", got, want)
	}
}

// TestMatrix_Expand_SingleAxis: a lone axis of N values → N combos.
func TestMatrix_Expand_SingleAxis(t *testing.T) {
	m := wf.Matrix{
		Axes: map[string][]any{
			"version": {"1.20", "1.21", "1.22"},
		},
	}
	got, err := matrix.Expand(m)
	if err != nil {
		t.Fatalf("Expand returned error: %v", err)
	}
	want := []matrix.Combination{
		{"version": "1.20"},
		{"version": "1.21"},
		{"version": "1.22"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Expand single axis:\n got  %v\n want %v", got, want)
	}
}

// TestMatrix_Expand_EmptyMatrix_ReturnsSingleEmptyCombo: GitHub treats an
// empty matrix as one execution with no matrix bindings, so the job still
// runs once.
func TestMatrix_Expand_EmptyMatrix_ReturnsSingleEmptyCombo(t *testing.T) {
	var m wf.Matrix
	got, err := matrix.Expand(m)
	if err != nil {
		t.Fatalf("Expand returned error: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("expected single empty combo, got %d combos: %v", len(got), got)
	}
	if len(got[0]) != 0 {
		t.Fatalf("expected empty combination, got %v", got[0])
	}
}

// TestMatrix_Expand_Includes_AddsNewCombination: an include row whose keys
// don't fully match the axis schema is appended as a brand-new combo.
func TestMatrix_Expand_Includes_AddsNewCombination(t *testing.T) {
	m := wf.Matrix{
		Axes: map[string][]any{
			"os": {"linux", "darwin"},
		},
		Include: []map[string]any{
			{"os": "windows", "extra": "patched"},
		},
	}
	got, err := matrix.Expand(m)
	if err != nil {
		t.Fatalf("Expand returned error: %v", err)
	}
	want := []matrix.Combination{
		{"os": "linux"},
		{"os": "darwin"},
		{"os": "windows", "extra": "patched"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Expand include adds new:\n got  %v\n want %v", got, want)
	}
}

// TestMatrix_Expand_Includes_ExtendsExistingCombo: an include row whose axis
// keys (the subset of keys that name declared axes) all match an existing
// combo only contributes its non-axis keys to that combo. No new row is
// produced. This is GitHub's documented quirk.
func TestMatrix_Expand_Includes_ExtendsExistingCombo(t *testing.T) {
	m := wf.Matrix{
		Axes: map[string][]any{
			"os":      {"linux", "darwin"},
			"version": {"1.20", "1.21"},
		},
		Include: []map[string]any{
			{"os": "linux", "version": "1.21", "experimental": true},
		},
	}
	got, err := matrix.Expand(m)
	if err != nil {
		t.Fatalf("Expand returned error: %v", err)
	}
	want := []matrix.Combination{
		{"os": "linux", "version": "1.20"},
		{"os": "linux", "version": "1.21", "experimental": true},
		{"os": "darwin", "version": "1.20"},
		{"os": "darwin", "version": "1.21"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Expand include extends existing:\n got  %v\n want %v", got, want)
	}
}

// TestMatrix_Expand_Excludes_RemovesCombination: an exclude row that fully
// matches an existing combo on every named axis removes that combo.
func TestMatrix_Expand_Excludes_RemovesCombination(t *testing.T) {
	m := wf.Matrix{
		Axes: map[string][]any{
			"os":      {"linux", "darwin"},
			"version": {"1.20", "1.21"},
		},
		Exclude: []map[string]any{
			{"os": "darwin", "version": "1.20"},
		},
	}
	got, err := matrix.Expand(m)
	if err != nil {
		t.Fatalf("Expand returned error: %v", err)
	}
	want := []matrix.Combination{
		{"os": "linux", "version": "1.20"},
		{"os": "linux", "version": "1.21"},
		{"os": "darwin", "version": "1.21"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Expand exclude removes:\n got  %v\n want %v", got, want)
	}
}

// TestMatrix_Expand_Excludes_PartialMatchRemovesAll: an exclude row that only
// names a subset of axes removes every combo where the named keys match.
func TestMatrix_Expand_Excludes_PartialMatchRemovesAll(t *testing.T) {
	m := wf.Matrix{
		Axes: map[string][]any{
			"os":      {"linux", "darwin"},
			"version": {"1.20", "1.21"},
		},
		Exclude: []map[string]any{
			{"os": "linux"},
		},
	}
	got, err := matrix.Expand(m)
	if err != nil {
		t.Fatalf("Expand returned error: %v", err)
	}
	want := []matrix.Combination{
		{"os": "darwin", "version": "1.20"},
		{"os": "darwin", "version": "1.21"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Expand exclude partial:\n got  %v\n want %v", got, want)
	}
}

// TestMatrix_Expand_Exclude_Then_Include_PreservesIncluded: GitHub applies
// excludes against the cartesian product only, then appends includes. So an
// include row that would have matched an exclude must still survive.
func TestMatrix_Expand_Exclude_Then_Include_PreservesIncluded(t *testing.T) {
	m := wf.Matrix{
		Axes: map[string][]any{
			"os":      {"linux", "darwin"},
			"version": {"1.20", "1.21"},
		},
		Exclude: []map[string]any{
			// Cartesian-product row that excludes (linux, 1.21).
			{"os": "linux", "version": "1.21"},
		},
		Include: []map[string]any{
			// New combo with a key not in the axis schema → appended, even
			// though it shares (os, version) with the excluded row.
			{"os": "linux", "version": "1.21", "experimental": true},
		},
	}
	got, err := matrix.Expand(m)
	if err != nil {
		t.Fatalf("Expand returned error: %v", err)
	}
	want := []matrix.Combination{
		{"os": "linux", "version": "1.20"},
		{"os": "darwin", "version": "1.20"},
		{"os": "darwin", "version": "1.21"},
		{"os": "linux", "version": "1.21", "experimental": true},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Expand exclude-then-include preserves include:\n got  %v\n want %v", got, want)
	}
}

// TestMatrix_Expand_IncludesOnly_OneRowPerInclude: with no axes declared,
// each include row becomes its own combination. Verifies that the empty
// product is not silently merged into a single super-row.
func TestMatrix_Expand_IncludesOnly_OneRowPerInclude(t *testing.T) {
	m := wf.Matrix{
		Include: []map[string]any{
			{"os": "linux", "go": "1.20"},
			{"os": "darwin", "go": "1.21"},
		},
	}
	got, err := matrix.Expand(m)
	if err != nil {
		t.Fatalf("Expand returned error: %v", err)
	}
	want := []matrix.Combination{
		{"os": "linux", "go": "1.20"},
		{"os": "darwin", "go": "1.21"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Expand includes-only:\n got  %v\n want %v", got, want)
	}
}

// TestMatrix_Expand_DeterministicOrder: the same input must produce the same
// output across repeated calls. Because Axes is a Go map (random iteration
// order), Expand sorts axis names lexically to fix the outer-axis order.
func TestMatrix_Expand_DeterministicOrder(t *testing.T) {
	m := wf.Matrix{
		Axes: map[string][]any{
			"alpha": {1, 2},
			"beta":  {"a", "b"},
			"gamma": {true, false},
		},
		Include: []map[string]any{
			{"alpha": 9, "beta": "x", "gamma": true},
		},
		Exclude: []map[string]any{
			{"alpha": 1, "gamma": false},
		},
	}
	first, err := matrix.Expand(m)
	if err != nil {
		t.Fatalf("Expand first call returned error: %v", err)
	}
	for i := 0; i < 25; i++ {
		next, err := matrix.Expand(m)
		if err != nil {
			t.Fatalf("Expand iter %d returned error: %v", i, err)
		}
		if !reflect.DeepEqual(first, next) {
			t.Fatalf("Expand non-deterministic at iter %d:\n first %v\n next  %v", i, first, next)
		}
	}
}
