// Tests for the two-pass workflow materialiser.
//
// The materialiser composes parser, schema, matrix, and dag layers into the
// two entry points the selection and scheduling contexts depend on:
// MaterialisePartial (cheap, trigger-and-paths only) and MaterialiseFull
// (full IR plus matrix expansion and topo verification).
//
// Tests are kept here in the package _test variant so they exercise the
// public materialiser surface only — no reaching into internals.
package authoring_test

import (
	"errors"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/staneswilson/gact/internal/authoring"
	"github.com/staneswilson/gact/internal/authoring/dag"
	"github.com/staneswilson/gact/internal/authoring/expr"
	wf "github.com/staneswilson/gact/pkg/workflow"
)

// TestMaterialise_PartialParse_ExtractsOnlyTriggersAndPaths asserts that the
// partial pass returns a Workflow whose Triggers are populated but whose
// JobsByID is nil — the documented sentinel for "partial".
func TestMaterialise_PartialParse_ExtractsOnlyTriggersAndPaths(t *testing.T) {
	src := []byte(`
on:
  push:
    paths: ['src/**']
jobs:
  test:
    runs-on: ubuntu-latest
    steps:
      - run: echo hi
`)
	partial, err := authoring.MaterialisePartial("ci.yml", src)
	if err != nil {
		t.Fatalf("MaterialisePartial: %v", err)
	}
	if partial.Triggers.Push == nil {
		t.Fatal("expected push trigger to be populated")
	}
	if got, want := partial.Triggers.Push.Paths, []string{"src/**"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("paths: got %v, want %v", got, want)
	}
	if partial.JobsByID != nil {
		t.Fatalf("partial parse must leave JobsByID nil, got %v", partial.JobsByID)
	}
	if partial.Path != "ci.yml" {
		t.Fatalf("Path: got %q, want %q", partial.Path, "ci.yml")
	}
}

// TestMaterialise_PartialParse_HandlesAllTriggerForms covers GitHub's three
// surface forms for `on:` — bare scalar, list, and mapping — to make sure
// the partial parser is structurally equivalent to the full parser's trigger
// decoder.
func TestMaterialise_PartialParse_HandlesAllTriggerForms(t *testing.T) {
	cases := []struct {
		name           string
		src            string
		assertTriggers func(t *testing.T, tr wf.Triggers)
	}{
		{
			name: "bare scalar",
			src:  "on: push\n",
			assertTriggers: func(t *testing.T, tr wf.Triggers) {
				if tr.Push == nil {
					t.Fatal("expected push to be present")
				}
			},
		},
		{
			name: "list form",
			src:  "on: [push, pull_request]\n",
			assertTriggers: func(t *testing.T, tr wf.Triggers) {
				if tr.Push == nil {
					t.Fatal("expected push to be present")
				}
				if tr.PullRequest == nil {
					t.Fatal("expected pull_request to be present")
				}
			},
		},
		{
			name: "mapping form with paths and branches",
			src: `on:
  push:
    branches: [main]
    paths: ['src/**', '!src/docs/**']
  pull_request:
    branches-ignore: [release/*]
    paths-ignore: ['*.md']
`,
			assertTriggers: func(t *testing.T, tr wf.Triggers) {
				if tr.Push == nil {
					t.Fatal("expected push to be present")
				}
				if got, want := tr.Push.Branches, []string{"main"}; !reflect.DeepEqual(got, want) {
					t.Fatalf("push.branches: got %v, want %v", got, want)
				}
				if got, want := tr.Push.Paths, []string{"src/**", "!src/docs/**"}; !reflect.DeepEqual(got, want) {
					t.Fatalf("push.paths: got %v, want %v", got, want)
				}
				if tr.PullRequest == nil {
					t.Fatal("expected pull_request to be present")
				}
				if got, want := tr.PullRequest.BranchesIgnore, []string{"release/*"}; !reflect.DeepEqual(got, want) {
					t.Fatalf("pull_request.branches-ignore: got %v, want %v", got, want)
				}
				if got, want := tr.PullRequest.PathsIgnore, []string{"*.md"}; !reflect.DeepEqual(got, want) {
					t.Fatalf("pull_request.paths-ignore: got %v, want %v", got, want)
				}
			},
		},
		{
			name: "tags and tags-ignore",
			src: `on:
  push:
    tags: ['v*']
    tags-ignore: ['v0.*']
`,
			assertTriggers: func(t *testing.T, tr wf.Triggers) {
				if tr.Push == nil {
					t.Fatal("expected push")
				}
				if got, want := tr.Push.Tags, []string{"v*"}; !reflect.DeepEqual(got, want) {
					t.Fatalf("push.tags: got %v, want %v", got, want)
				}
				if got, want := tr.Push.TagsIgnore, []string{"v0.*"}; !reflect.DeepEqual(got, want) {
					t.Fatalf("push.tags-ignore: got %v, want %v", got, want)
				}
			},
		},
		{
			name: "schedule and workflow_dispatch",
			src: `on:
  schedule:
    - cron: '0 0 * * *'
  workflow_dispatch:
`,
			assertTriggers: func(t *testing.T, tr wf.Triggers) {
				if len(tr.Schedule) != 1 || tr.Schedule[0].Cron != "0 0 * * *" {
					t.Fatalf("schedule: got %+v", tr.Schedule)
				}
				if tr.WorkflowDispatch == nil {
					t.Fatal("expected workflow_dispatch")
				}
			},
		},
	}

	for _, c := range cases {
		c := c
		t.Run(c.name, func(t *testing.T) {
			partial, err := authoring.MaterialisePartial("ci.yml", []byte(c.src))
			if err != nil {
				t.Fatalf("MaterialisePartial: %v", err)
			}
			if partial.JobsByID != nil {
				t.Fatalf("partial must not populate jobs, got %v", partial.JobsByID)
			}
			c.assertTriggers(t, partial.Triggers)
		})
	}
}

// TestMaterialise_PartialParse_NameIsExtracted asserts that workflow names —
// useful for diagnostic output during selection — survive the partial pass.
func TestMaterialise_PartialParse_NameIsExtracted(t *testing.T) {
	src := []byte("name: ci\non: push\njobs:\n  t: { runs-on: linux, steps: [{ run: x }] }\n")
	partial, err := authoring.MaterialisePartial("ci.yml", src)
	if err != nil {
		t.Fatalf("MaterialisePartial: %v", err)
	}
	if partial.Name != "ci" {
		t.Fatalf("name: got %q, want %q", partial.Name, "ci")
	}
}

// TestMaterialise_PartialParse_HandlesMalformedYAML asserts that the partial
// pass still surfaces a parser error (rather than swallowing it as a "no
// triggers" result) when the YAML is structurally broken.
func TestMaterialise_PartialParse_HandlesMalformedYAML(t *testing.T) {
	src := []byte("on: { push:\n - branches:\n")
	_, err := authoring.MaterialisePartial("ci.yml", src)
	if err == nil {
		t.Fatal("expected error on malformed YAML, got nil")
	}
}

// TestMaterialise_Full_RunsAllPasses exercises a workflow that has a matrix,
// a `needs:` edge across the matrix, and a non-matrix consumer. After
// materialisation the matrix job has been expanded and the topo plan is
// usable.
func TestMaterialise_Full_RunsAllPasses(t *testing.T) {
	src := []byte(`
name: ci
on: push
jobs:
  build:
    runs-on: ubuntu-latest
    strategy:
      matrix:
        node: [18, 20]
    steps:
      - run: echo build
  test:
    needs: [build]
    runs-on: ubuntu-latest
    steps:
      - run: echo test
`)
	w, diags, err := authoring.MaterialiseFull("ci.yml", src, authoring.MaterialiseInputs{
		StaticInputs: expr.StaticInputs{Event: "push", Ref: "refs/heads/main"},
	})
	if err != nil {
		t.Fatalf("MaterialiseFull: %v (diags=%v)", err, diags)
	}
	if len(diags) != 0 {
		t.Fatalf("expected no diags, got %v", diags)
	}

	// The build job must have expanded into two synthetic jobs.
	if _, ok := w.JobsByID["build"]; ok {
		t.Fatal("post-expansion: original matrix template should be replaced by expanded jobs")
	}
	build0, ok0 := w.JobsByID["build_0"]
	build1, ok1 := w.JobsByID["build_1"]
	if !ok0 || !ok1 {
		t.Fatalf("expected build_0 and build_1 in JobsByID, got %v", jobIDs(w.JobsByID))
	}
	if build0.Matrix != nil || build1.Matrix != nil {
		t.Fatal("expanded jobs must not carry the matrix template")
	}
	if !strings.HasPrefix(build0.Name, "build (combo:") {
		t.Fatalf("build_0 Name: got %q, want prefix %q", build0.Name, "build (combo:")
	}

	// `test` must still exist (it had no matrix) and its needs must now point
	// at every expanded build_<i>.
	tj, ok := w.JobsByID["test"]
	if !ok {
		t.Fatalf("expected test to remain in JobsByID, got %v", jobIDs(w.JobsByID))
	}
	needs := append([]wf.JobID(nil), tj.Needs...)
	sortJobIDs(needs)
	want := []wf.JobID{"build_0", "build_1"}
	if !reflect.DeepEqual(needs, want) {
		t.Fatalf("test.needs after expansion: got %v, want %v", needs, want)
	}

	// Topo sort should put build_* in the first layer and test in the next.
	layers, err := dag.TopoSort(w.JobsByID)
	if err != nil {
		t.Fatalf("TopoSort after materialise: %v", err)
	}
	if len(layers) != 2 {
		t.Fatalf("layer count: got %d, want 2 (%v)", len(layers), layers)
	}
}

// TestMaterialise_Full_PropagatesSchemaErrors asserts that structural
// diagnostics surface as a returned slice rather than blocking the
// materialise. Callers (lint, the LSP) decide whether to abort.
func TestMaterialise_Full_PropagatesSchemaErrors(t *testing.T) {
	// "steps: []" violates SCHEMA-JOB-NO-STEPS. The workflow is still
	// parseable, so MaterialiseFull must return both the Workflow and the
	// diagnostic.
	src := []byte(`
on: push
jobs:
  empty:
    runs-on: ubuntu-latest
    steps: []
`)
	w, diags, err := authoring.MaterialiseFull("ci.yml", src, authoring.MaterialiseInputs{})
	if err != nil {
		t.Fatalf("MaterialiseFull: %v", err)
	}
	if len(diags) == 0 {
		t.Fatal("expected at least one schema diagnostic, got none")
	}
	if _, ok := w.JobsByID["empty"]; !ok {
		t.Fatalf("workflow should still materialise on schema error; got jobs %v", jobIDs(w.JobsByID))
	}
}

// TestMaterialise_Full_DetectsCycleAfterExpansion constructs two non-matrix
// jobs whose needs form a cycle and asserts the materialiser returns a typed
// *dag.CycleError.
func TestMaterialise_Full_DetectsCycleAfterExpansion(t *testing.T) {
	src := []byte(`
on: push
jobs:
  a:
    needs: [b]
    runs-on: linux
    steps: [{ run: echo a }]
  b:
    needs: [a]
    runs-on: linux
    steps: [{ run: echo b }]
`)
	_, _, err := authoring.MaterialiseFull("ci.yml", src, authoring.MaterialiseInputs{})
	if err == nil {
		t.Fatal("expected cycle error, got nil")
	}
	var ce *dag.CycleError
	if !errors.As(err, &ce) {
		t.Fatalf("expected *dag.CycleError, got %T: %v", err, err)
	}
}

// TestMaterialise_Full_RemapsNeedsAcrossMatrix asserts the documented GH
// behaviour: when job B `needs: [A]` and A is matrix-expanded into A_0/A_1,
// the materialised B depends on BOTH A_0 and A_1 (every B-expansion if B is
// also a matrix). GitHub does not support combo-level needs selection, so
// every consumer instance depends on every producer instance.
func TestMaterialise_Full_RemapsNeedsAcrossMatrix(t *testing.T) {
	src := []byte(`
on: push
jobs:
  a:
    runs-on: linux
    strategy:
      matrix:
        n: [1, 2]
    steps: [{ run: echo a }]
  b:
    needs: [a]
    runs-on: linux
    strategy:
      matrix:
        m: [x, y]
    steps: [{ run: echo b }]
`)
	w, _, err := authoring.MaterialiseFull("ci.yml", src, authoring.MaterialiseInputs{})
	if err != nil {
		t.Fatalf("MaterialiseFull: %v", err)
	}
	for _, id := range []wf.JobID{"b_0", "b_1"} {
		j, ok := w.JobsByID[id]
		if !ok {
			t.Fatalf("expected expanded job %s in jobs %v", id, jobIDs(w.JobsByID))
		}
		needs := append([]wf.JobID(nil), j.Needs...)
		sortJobIDs(needs)
		want := []wf.JobID{"a_0", "a_1"}
		if !reflect.DeepEqual(needs, want) {
			t.Fatalf("%s.needs: got %v, want %v", id, needs, want)
		}
	}
}

// TestMaterialise_Full_NonMatrixSourceWithMatrixConsumer asserts the inverse:
// a non-matrix source A keeps its bare ID and every B_i depends on the single
// A. The remap only kicks in when the *source* expanded.
func TestMaterialise_Full_NonMatrixSourceWithMatrixConsumer(t *testing.T) {
	src := []byte(`
on: push
jobs:
  a:
    runs-on: linux
    steps: [{ run: echo a }]
  b:
    needs: [a]
    runs-on: linux
    strategy:
      matrix:
        m: [x, y]
    steps: [{ run: echo b }]
`)
	w, _, err := authoring.MaterialiseFull("ci.yml", src, authoring.MaterialiseInputs{})
	if err != nil {
		t.Fatalf("MaterialiseFull: %v", err)
	}
	if _, ok := w.JobsByID["a"]; !ok {
		t.Fatal("non-matrix job 'a' should retain its ID")
	}
	for _, id := range []wf.JobID{"b_0", "b_1"} {
		j, ok := w.JobsByID[id]
		if !ok {
			t.Fatalf("expected %s in jobs %v", id, jobIDs(w.JobsByID))
		}
		if !reflect.DeepEqual(j.Needs, []wf.JobID{"a"}) {
			t.Fatalf("%s.needs: got %v, want [a]", id, j.Needs)
		}
	}
}

// TestMaterialise_Full_StrictPromotesSchemaErrors verifies the documented
// Strict toggle: when set, schema diagnostics escape as a wrapped error so
// callers (e.g. pre-push gate) can refuse to run on any structural issue.
func TestMaterialise_Full_StrictPromotesSchemaErrors(t *testing.T) {
	src := []byte(`
on: push
jobs:
  empty:
    runs-on: ubuntu-latest
    steps: []
`)
	_, _, err := authoring.MaterialiseFull("ci.yml", src, authoring.MaterialiseInputs{Strict: true})
	if err == nil {
		t.Fatal("expected wrapped schema error under Strict, got nil")
	}
	if !strings.Contains(err.Error(), "SCHEMA-") {
		t.Fatalf("expected error message to reference schema code, got %q", err.Error())
	}
}

// TestMaterialise_Full_NoMatrix_TopoStillRuns verifies that the post-expansion
// topo check is run even when no job carries a matrix — protecting against
// cycles in plain workflows.
func TestMaterialise_Full_NoMatrix_TopoStillRuns(t *testing.T) {
	src := []byte(`
on: push
jobs:
  one:
    runs-on: linux
    steps: [{ run: echo 1 }]
  two:
    needs: [one]
    runs-on: linux
    steps: [{ run: echo 2 }]
`)
	w, _, err := authoring.MaterialiseFull("ci.yml", src, authoring.MaterialiseInputs{})
	if err != nil {
		t.Fatalf("MaterialiseFull: %v", err)
	}
	if _, ok := w.JobsByID["one"]; !ok {
		t.Fatal("expected 'one'")
	}
	if _, ok := w.JobsByID["two"]; !ok {
		t.Fatal("expected 'two'")
	}
}

// jobIDs returns the keys of a job map as a sorted slice for stable test
// diagnostics on failure.
func jobIDs(m map[wf.JobID]wf.Job) []wf.JobID {
	ids := make([]wf.JobID, 0, len(m))
	for id := range m {
		ids = append(ids, id)
	}
	sortJobIDs(ids)
	return ids
}

func sortJobIDs(ids []wf.JobID) {
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
}
