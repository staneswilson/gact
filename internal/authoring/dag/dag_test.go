package dag

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	wf "github.com/staneswilson/gact/pkg/workflow"
)

// helper: build a job map from id -> needs.
func mkJobs(spec map[wf.JobID][]wf.JobID) map[wf.JobID]wf.Job {
	out := make(map[wf.JobID]wf.Job, len(spec))
	for id, needs := range spec {
		out[id] = wf.Job{ID: id, Needs: append([]wf.JobID(nil), needs...)}
	}
	return out
}

func TestDAG_TopoSort_LinearChain(t *testing.T) {
	jobs := mkJobs(map[wf.JobID][]wf.JobID{
		"A": nil,
		"B": {"A"},
		"C": {"B"},
	})
	got, err := TopoSort(jobs)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := Layers{{"A"}, {"B"}, {"C"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("layers = %v, want %v", got, want)
	}
}

func TestDAG_TopoSort_DiamondParallelMiddle(t *testing.T) {
	jobs := mkJobs(map[wf.JobID][]wf.JobID{
		"A": nil,
		"B": {"A"},
		"C": {"A"},
		"D": {"B", "C"},
	})
	got, err := TopoSort(jobs)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := Layers{{"A"}, {"B", "C"}, {"D"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("layers = %v, want %v", got, want)
	}
}

func TestDAG_TopoSort_NoNeeds_AllSingleLayer(t *testing.T) {
	jobs := mkJobs(map[wf.JobID][]wf.JobID{
		"A": nil,
		"B": nil,
		"C": nil,
	})
	got, err := TopoSort(jobs)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := Layers{{"A", "B", "C"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("layers = %v, want %v", got, want)
	}
}

func TestDAG_DetectCycle_TwoNode(t *testing.T) {
	jobs := mkJobs(map[wf.JobID][]wf.JobID{
		"A": {"B"},
		"B": {"A"},
	})
	_, err := TopoSort(jobs)
	if err == nil {
		t.Fatalf("expected cycle error, got nil")
	}
	var ce *CycleError
	if !errors.As(err, &ce) {
		t.Fatalf("err is not *CycleError: %T (%v)", err, err)
	}
	// Path must include both nodes.
	seen := map[wf.JobID]bool{}
	for _, id := range ce.Path {
		seen[id] = true
	}
	if !seen["A"] || !seen["B"] {
		t.Fatalf("CycleError.Path %v missing A or B", ce.Path)
	}
	// Error string should follow `cycle in job graph: ...`.
	if !strings.HasPrefix(ce.Error(), "cycle in job graph: ") {
		t.Fatalf("CycleError.Error() = %q, want prefix %q", ce.Error(), "cycle in job graph: ")
	}
	// And the path should close back on itself.
	if !strings.Contains(ce.Error(), "->") {
		t.Fatalf("CycleError.Error() = %q, want arrows", ce.Error())
	}
}

func TestDAG_DetectCycle_LongCycle(t *testing.T) {
	jobs := mkJobs(map[wf.JobID][]wf.JobID{
		"A": {"C"},
		"B": {"A"},
		"C": {"B"},
	})
	_, err := TopoSort(jobs)
	if err == nil {
		t.Fatalf("expected cycle error, got nil")
	}
	var ce *CycleError
	if !errors.As(err, &ce) {
		t.Fatalf("err is not *CycleError: %T (%v)", err, err)
	}
	seen := map[wf.JobID]bool{}
	for _, id := range ce.Path {
		seen[id] = true
	}
	for _, want := range []wf.JobID{"A", "B", "C"} {
		if !seen[want] {
			t.Fatalf("CycleError.Path %v missing %s", ce.Path, want)
		}
	}
}

func TestDAG_DetectCycle_SelfLoop(t *testing.T) {
	jobs := mkJobs(map[wf.JobID][]wf.JobID{
		"A": {"A"},
	})
	_, err := TopoSort(jobs)
	if err == nil {
		t.Fatalf("expected cycle error, got nil")
	}
	var ce *CycleError
	if !errors.As(err, &ce) {
		t.Fatalf("err is not *CycleError: %T (%v)", err, err)
	}
	if len(ce.Path) == 0 || ce.Path[0] != "A" {
		t.Fatalf("CycleError.Path = %v, want to include A", ce.Path)
	}
	if !strings.Contains(ce.Error(), "A -> A") {
		t.Fatalf("CycleError.Error() = %q, want to contain %q", ce.Error(), "A -> A")
	}
}

func TestDAG_UnknownNeeds_ReturnsTypedError(t *testing.T) {
	jobs := mkJobs(map[wf.JobID][]wf.JobID{
		"A": {"ghost"},
	})
	_, err := TopoSort(jobs)
	if err == nil {
		t.Fatalf("expected unknown-need error, got nil")
	}
	var ue *UnknownNeedError
	if !errors.As(err, &ue) {
		t.Fatalf("err is not *UnknownNeedError: %T (%v)", err, err)
	}
	if ue.Job != "A" || ue.Need != "ghost" {
		t.Fatalf("UnknownNeedError = %+v, want Job=A Need=ghost", ue)
	}
	want := "job A needs unknown job ghost"
	if ue.Error() != want {
		t.Fatalf("UnknownNeedError.Error() = %q, want %q", ue.Error(), want)
	}
}

func TestDAG_EmptyGraph_ReturnsEmptyLayers(t *testing.T) {
	got, err := TopoSort(map[wf.JobID]wf.Job{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("got layers %v, want empty", got)
	}

	// nil map also OK.
	got2, err := TopoSort(nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got2) != 0 {
		t.Fatalf("got layers %v, want empty", got2)
	}
}

func TestDAG_TopoSort_Stability(t *testing.T) {
	jobs := mkJobs(map[wf.JobID][]wf.JobID{
		"zeta":  {"alpha"},
		"alpha": nil,
		"mu":    {"alpha"},
		"beta":  {"alpha"},
	})
	// Run many times; Go map iteration is randomised, so any nondeterminism
	// would show up across iterations.
	var first Layers
	for i := 0; i < 50; i++ {
		got, err := TopoSort(jobs)
		if err != nil {
			t.Fatalf("iter %d: unexpected error: %v", i, err)
		}
		if i == 0 {
			first = got
			continue
		}
		if !reflect.DeepEqual(got, first) {
			t.Fatalf("iter %d: layers = %v, want %v (nondeterministic)", i, got, first)
		}
	}
	// Within layer 1, expect lexicographic order.
	if len(first) < 2 {
		t.Fatalf("expected at least 2 layers, got %v", first)
	}
	wantLayer1 := []wf.JobID{"beta", "mu", "zeta"}
	if !reflect.DeepEqual(first[1], wantLayer1) {
		t.Fatalf("layer 1 = %v, want %v", first[1], wantLayer1)
	}
}

// Table-driven happy-path topo cases.
func TestDAG_TopoSort_Table(t *testing.T) {
	cases := []struct {
		name string
		jobs map[wf.JobID][]wf.JobID
		want Layers
	}{
		{
			name: "single_job",
			jobs: map[wf.JobID][]wf.JobID{"only": nil},
			want: Layers{{"only"}},
		},
		{
			name: "two_independent",
			jobs: map[wf.JobID][]wf.JobID{"a": nil, "b": nil},
			want: Layers{{"a", "b"}},
		},
		{
			name: "fan_out",
			jobs: map[wf.JobID][]wf.JobID{
				"root": nil,
				"x":    {"root"},
				"y":    {"root"},
				"z":    {"root"},
			},
			want: Layers{{"root"}, {"x", "y", "z"}},
		},
		{
			name: "fan_in",
			jobs: map[wf.JobID][]wf.JobID{
				"a":    nil,
				"b":    nil,
				"c":    nil,
				"sink": {"a", "b", "c"},
			},
			want: Layers{{"a", "b", "c"}, {"sink"}},
		},
		{
			name: "duplicate_needs_idempotent",
			jobs: map[wf.JobID][]wf.JobID{
				"a": nil,
				"b": {"a", "a"},
			},
			want: Layers{{"a"}, {"b"}},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := TopoSort(mkJobs(tc.jobs))
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("layers = %v, want %v", got, tc.want)
			}
		})
	}
}
