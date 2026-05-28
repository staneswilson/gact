// Package dag computes the job execution order for a Workflow.
//
// A Workflow's jobs form a directed graph whose edges flow from each job to
// the jobs it `needs:`. The scheduler runs jobs layer by layer: every job in
// layer N has all of its dependencies satisfied by layers 0..N-1, so the
// scheduler can fan out an entire layer in parallel.
//
// TopoSort implements Kahn's algorithm. Within a single layer it sorts JobIDs
// lexically so callers get a deterministic order regardless of Go's
// randomised map iteration — this matters for diagnostics, logs, and tests.
//
// Errors are typed so callers can branch on them with errors.As:
//
//   - CycleError      — the graph contains a cycle; Path traces it.
//   - UnknownNeedError — a job references a needs target that isn't in the map.
//
// The package is pure: no I/O, no goroutines, no shared state.
package dag

import (
	"fmt"
	"sort"
	"strings"

	wf "github.com/staneswilson/gact/pkg/workflow"
)

// Layers is the execution plan produced by TopoSort. Each inner slice holds
// jobs that can run concurrently; the scheduler advances layer by layer.
type Layers [][]wf.JobID

// CycleError reports that the job graph contains a cycle. Path lists the jobs
// participating in the cycle in traversal order, with the start node repeated
// at the end so the cycle reads as a closed loop in diagnostics.
type CycleError struct {
	Path []wf.JobID
}

// Error renders the cycle as "cycle in job graph: a -> b -> c -> a".
func (e *CycleError) Error() string {
	parts := make([]string, 0, len(e.Path))
	for _, id := range e.Path {
		parts = append(parts, id.String())
	}
	return "cycle in job graph: " + strings.Join(parts, " -> ")
}

// UnknownNeedError reports that Job's `needs:` list references a JobID that
// does not appear in the workflow's job map.
type UnknownNeedError struct {
	Job  wf.JobID
	Need wf.JobID
}

// Error renders as "job <Job> needs unknown job <Need>".
func (e *UnknownNeedError) Error() string {
	return fmt.Sprintf("job %s needs unknown job %s", e.Job, e.Need)
}

// TopoSort returns the layered execution plan for jobs using Kahn's algorithm.
//
// A nil or empty input yields a nil-or-empty Layers slice with no error —
// callers that drive a scheduler can treat "no jobs" as a no-op.
//
// Within each layer JobIDs are sorted lexicographically so the output is
// stable across runs despite Go's randomised map iteration.
func TopoSort(jobs map[wf.JobID]wf.Job) (Layers, error) {
	if len(jobs) == 0 {
		return Layers{}, nil
	}

	// inDegree[id] = number of unresolved dependencies for id.
	inDegree := make(map[wf.JobID]int, len(jobs))
	// dependents[id] = jobs that need id; used to decrement in-degrees as we
	// finalise layers.
	dependents := make(map[wf.JobID][]wf.JobID, len(jobs))

	// Initialise structures, dedupe duplicate needs, and validate references.
	for id, job := range jobs {
		if _, ok := inDegree[id]; !ok {
			inDegree[id] = 0
		}
		seen := make(map[wf.JobID]struct{}, len(job.Needs))
		for _, need := range job.Needs {
			if _, ok := jobs[need]; !ok {
				return nil, &UnknownNeedError{Job: id, Need: need}
			}
			if _, dup := seen[need]; dup {
				continue
			}
			seen[need] = struct{}{}
			inDegree[id]++
			dependents[need] = append(dependents[need], id)
		}
	}

	// Seed the first layer with every zero-in-degree job, lexically sorted.
	ready := make([]wf.JobID, 0)
	for id, deg := range inDegree {
		if deg == 0 {
			ready = append(ready, id)
		}
	}
	sortJobIDs(ready)

	var layers Layers
	processed := 0

	for len(ready) > 0 {
		// Snapshot this layer.
		layer := make([]wf.JobID, len(ready))
		copy(layer, ready)
		layers = append(layers, layer)
		processed += len(layer)

		next := make([]wf.JobID, 0)
		for _, id := range layer {
			// Sort dependents for deterministic traversal even though the
			// next layer is re-sorted below — being defensive keeps the
			// algorithm independent of map iteration order.
			deps := dependents[id]
			for _, dep := range deps {
				inDegree[dep]--
				if inDegree[dep] == 0 {
					next = append(next, dep)
				}
			}
		}
		sortJobIDs(next)
		ready = next
	}

	if processed != len(jobs) {
		// Some jobs still have non-zero in-degree — there's a cycle.
		return nil, buildCycleError(jobs, inDegree)
	}

	return layers, nil
}

// buildCycleError walks the residual graph (nodes whose in-degree never hit
// zero) to extract one concrete cycle and returns it as *CycleError.
//
// A job remains in `remaining` iff at least one of its `needs:` is also in
// `remaining`. We therefore can keep stepping forward along `needs` edges
// and are guaranteed to revisit a node — that revisit is the cycle.
func buildCycleError(jobs map[wf.JobID]wf.Job, inDegree map[wf.JobID]int) *CycleError {
	remaining := make(map[wf.JobID]struct{}, len(jobs))
	ids := make([]wf.JobID, 0)
	for id, deg := range inDegree {
		if deg > 0 {
			remaining[id] = struct{}{}
			ids = append(ids, id)
		}
	}
	sortJobIDs(ids)
	if len(ids) == 0 {
		// Defensive: caller only invokes us when there's a cycle.
		return &CycleError{}
	}

	// Walk from the lexicographically smallest remaining node for determinism.
	start := ids[0]
	path := []wf.JobID{start}
	indexOf := map[wf.JobID]int{start: 0}
	cur := start

	for {
		// Pick the lexicographically smallest unresolved need of `cur`.
		needs := append([]wf.JobID(nil), jobs[cur].Needs...)
		sortJobIDs(needs)
		var nextID wf.JobID
		found := false
		for _, n := range needs {
			if _, ok := remaining[n]; ok {
				nextID = n
				found = true
				break
			}
		}
		if !found {
			// Unreachable in practice: a node in `remaining` always has at
			// least one need still in `remaining`. Return what we have.
			return &CycleError{Path: append(path, start)}
		}
		if idx, ok := indexOf[nextID]; ok {
			loop := append([]wf.JobID(nil), path[idx:]...)
			loop = append(loop, nextID)
			return &CycleError{Path: loop}
		}
		indexOf[nextID] = len(path)
		path = append(path, nextID)
		cur = nextID
	}
}

// sortJobIDs sorts a slice of JobIDs in place by their string form.
func sortJobIDs(ids []wf.JobID) {
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
}
