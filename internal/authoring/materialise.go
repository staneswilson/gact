// Package authoring composes the parser, schema validator, matrix expander,
// and DAG topo-sort into the two materialise entry points the rest of gact
// depends on.
//
// The "two-pass" naming reflects how the selection layer drives this package:
//
//   - MaterialisePartial reads only `name` and the `on:` block. It is cheap
//     enough to call on every workflow file in the repo so the selection
//     layer can prune workflows whose triggers do not match the event before
//     paying for a full parse.
//   - MaterialiseFull does the work the rest of gact needs: parse, validate,
//     expand matrices, remap `needs:` across expansions, and verify the
//     post-expansion DAG. It runs only on the survivors of the partial pass.
//
// Both entry points are pure: no I/O, no clock, no env. Callers feed them
// bytes (typically the contents of `.github/workflows/<n>.yml`) and a value
// object of static inputs; they get an IR back.
package authoring

import (
	"fmt"
	"sort"
	"strings"

	"github.com/staneswilson/gact/internal/authoring/dag"
	"github.com/staneswilson/gact/internal/authoring/expr"
	"github.com/staneswilson/gact/internal/authoring/matrix"
	"github.com/staneswilson/gact/internal/authoring/parser"
	"github.com/staneswilson/gact/internal/authoring/schema"
	wf "github.com/staneswilson/gact/pkg/workflow"
)

// MaterialiseInputs is the value object MaterialiseFull consumes. It bundles
// the static expression-context inputs (used today by future passes such as
// reachability) with the Strict toggle.
//
// Strict promotes schema diagnostics to a wrapped error. The non-strict path
// returns them as a slice so the LSP and lint CLI can show every issue at
// once; the strict path is for the pre-push gate, where any structural issue
// should refuse the push.
type MaterialiseInputs struct {
	// StaticInputs is the static expression-evaluation context for any
	// downstream passes that want to evaluate `if:` and other expressions
	// against the materialised IR. The materialiser itself does not
	// evaluate expressions; selection (Task 2.3) and lint will.
	StaticInputs expr.StaticInputs

	// Strict toggles "fail on first schema diagnostic" semantics. Defaults
	// to false so callers see every issue at once.
	Strict bool
}

// MaterialisePartial decodes only the workflow keys needed for
// trigger-and-path-based selection. The returned Workflow has JobsByID nil
// — that nil is the contract for "partial parse, skip the jobs block".
//
// The function exists as a thin wrapper over parser.ParsePartial so that
// callers in other contexts (selection, the LSP) depend on a single package.
// Routing through the parser preserves a single source of truth for trigger
// decoding: any future change to GH's trigger surface lives in one place.
func MaterialisePartial(path string, src []byte) (wf.Workflow, error) {
	return parser.ParsePartial(path, src)
}

// MaterialiseFull parses the workflow fully, validates its structure,
// expands every matrix-bearing job, remaps `needs:` edges across the
// expansion, and verifies the resulting DAG.
//
// The returned (Workflow, diagnostics, error) triple mirrors the way the
// rest of gact treats partial failure: a workflow can have diagnostics and
// still be useful, so we hand back what we built plus the issues we saw.
// Genuine show-stoppers (parser errors, post-expansion cycles, unknown
// needs) are returned as the error.
//
// `if:` expressions are deliberately NOT evaluated here. Static pruning
// based on `if:` belongs in internal/selection/reachability (Task 2.3); the
// materialiser is pure structural transformation so that the same IR can be
// reused across selection, scheduling, and reporting without bleed-through.
func MaterialiseFull(path string, src []byte, in MaterialiseInputs) (wf.Workflow, []schema.SchemaError, error) {
	w, err := parser.Parse(path, src)
	if err != nil {
		return wf.Workflow{}, nil, fmt.Errorf("parse %s: %w", path, err)
	}

	diags := schema.Validate(w)

	expanded, err := expandMatrices(w)
	if err != nil {
		return wf.Workflow{}, diags, fmt.Errorf("expand matrices in %s: %w", path, err)
	}
	w.JobsByID = expanded

	if _, err := dag.TopoSort(w.JobsByID); err != nil {
		return wf.Workflow{}, diags, fmt.Errorf("topo sort %s: %w", path, err)
	}

	if in.Strict && len(diags) > 0 {
		return w, diags, schemaErrorsAsError(path, diags)
	}
	return w, diags, nil
}

// expandMatrices fans every matrix-bearing job into one concrete job per
// combination and remaps `needs:` edges so that consumers depend on the
// right producer instances.
//
// Naming convention:
//
//   - The new JobID is "<original>_<combo-index>" — a flat string keyed by
//     the order matrix.Expand returns combos. Numeric suffixes make the
//     IDs easy to grep for and keep `needs:` remapping a simple lookup.
//   - The Name (used in human-readable output) is
//     "<original> (combo: k=v, k=v)" with keys sorted lexically for
//     determinism. The original Name field is preserved when authored, but
//     overwritten to make the combo visible to logs and parity reports.
//
// Needs remapping rules (matching GitHub Actions semantics):
//
//   - GitHub does not let a job depend on a specific matrix combo of
//     another job. Therefore every consumer instance depends on every
//     producer instance: B `needs: [A]` after expansion becomes B_i
//     `needs: [A_0, A_1, ...]` for each B_i.
//   - If the producer never expanded (no matrix), the consumer keeps the
//     bare ID. The remap only touches needs whose target is in the
//     expansion table.
func expandMatrices(w wf.Workflow) (map[wf.JobID]wf.Job, error) {
	expansion := make(map[wf.JobID][]wf.JobID)
	out := make(map[wf.JobID]wf.Job, len(w.JobsByID))

	ids := sortedJobIDs(w.JobsByID)
	for _, id := range ids {
		job := w.JobsByID[id]
		if job.Matrix == nil {
			out[id] = job
			continue
		}
		combos, err := matrix.Expand(*job.Matrix)
		if err != nil {
			return nil, fmt.Errorf("matrix expansion for job %s: %w", id, err)
		}
		// An empty combo list is GitHub's "no rows survived exclude"
		// outcome. We drop the job entirely so a downstream consumer that
		// `needs:` it will surface as an unknown-need failure from
		// TopoSort — which is a more honest signal than silently keeping
		// the template.
		if len(combos) == 0 {
			continue
		}
		expandedIDs := make([]wf.JobID, 0, len(combos))
		for idx, combo := range combos {
			child := buildExpandedJob(job, idx, combo)
			out[child.ID] = child
			expandedIDs = append(expandedIDs, child.ID)
		}
		expansion[id] = expandedIDs
	}

	if len(expansion) == 0 {
		return out, nil
	}
	remapNeeds(out, expansion)
	return out, nil
}

// buildExpandedJob copies the matrix template into a concrete job for a
// single combo. The Matrix pointer is cleared on the child so that
// downstream code can rely on "Matrix != nil ⇒ template" as an invariant.
//
// The combo values themselves are not threaded into the job here — that is
// the scheduler's job, via the matrix.* expression scope at execution
// time. The materialiser only fixes the shape of the graph.
func buildExpandedJob(template wf.Job, idx int, combo matrix.Combination) wf.Job {
	child := template
	child.ID = wf.JobID(fmt.Sprintf("%s_%d", template.ID, idx))
	child.Name = formatComboName(template, combo)
	child.Matrix = nil
	// Defensive copy: Needs may be mutated later by remapNeeds, and we do
	// not want one expanded instance's remap to leak back into the
	// template via a shared backing array.
	if len(template.Needs) > 0 {
		child.Needs = append([]wf.JobID(nil), template.Needs...)
	}
	return child
}

// formatComboName renders the human-readable combo descriptor used as the
// expanded job's Name. Keys are sorted lexically so the output is stable
// regardless of map iteration order, which matters for tests and parity
// reports.
func formatComboName(template wf.Job, combo matrix.Combination) string {
	base := template.Name
	if base == "" {
		base = string(template.ID)
	}
	if len(combo) == 0 {
		return base
	}
	keys := make([]string, 0, len(combo))
	for k := range combo {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, fmt.Sprintf("%s=%v", k, combo[k]))
	}
	return fmt.Sprintf("%s (combo: %s)", base, strings.Join(parts, ", "))
}

// remapNeeds rewrites every job's `needs:` slice so that references to
// expanded producers point at the producer's child IDs. The work is done
// in two passes:
//
//  1. For non-expanded jobs (still in out under their original ID), rewrite
//     their Needs slice in place by substituting expanded IDs for any need
//     that appears in the expansion table.
//  2. For expanded jobs (out[<id>_<idx>] for some idx), do the same. Each
//     child has its own Needs slice (buildExpandedJob already deep-copied)
//     so rewriting in place is safe.
//
// The result: every consumer depends on every producer instance, with no
// dangling references to the original matrix template.
func remapNeeds(out map[wf.JobID]wf.Job, expansion map[wf.JobID][]wf.JobID) {
	for id, job := range out {
		if len(job.Needs) == 0 {
			continue
		}
		remapped := false
		newNeeds := make([]wf.JobID, 0, len(job.Needs))
		for _, need := range job.Needs {
			if replacement, ok := expansion[need]; ok {
				newNeeds = append(newNeeds, replacement...)
				remapped = true
				continue
			}
			newNeeds = append(newNeeds, need)
		}
		if remapped {
			job.Needs = newNeeds
			out[id] = job
		}
	}
}

// sortedJobIDs returns the keys of jobs lexically sorted so that the
// expansion table is built deterministically. Without this, two materialise
// calls over the same input could produce different expansion ordering and
// — worse — different child-job ordering inside the expanded map's iteration
// for downstream callers that walk it via ranged for. matrix.Expand itself
// is deterministic; we just need the outer job-order to be too.
func sortedJobIDs(jobs map[wf.JobID]wf.Job) []wf.JobID {
	ids := make([]wf.JobID, 0, len(jobs))
	for id := range jobs {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	return ids
}

// schemaErrorsAsError collapses a slice of schema diagnostics into a single
// error suitable for the Strict path. It is intentionally a join rather
// than a fancy wrapper: callers can still parse out the diagnostics from
// the message, but the typical use is to surface "this workflow has
// schema issues" without bothering to thread the slice through the call
// site.
func schemaErrorsAsError(path string, diags []schema.SchemaError) error {
	if len(diags) == 0 {
		return nil
	}
	msgs := make([]string, 0, len(diags))
	for _, d := range diags {
		msgs = append(msgs, d.Error())
	}
	return fmt.Errorf("schema diagnostics in %s: %s", path, strings.Join(msgs, "; "))
}
