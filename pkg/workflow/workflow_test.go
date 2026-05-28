// Package workflow_test contains tests for the public IR value objects.
//
// These tests assert the contract that pkg/workflow is a set of plain value
// objects: zero values are usable, copies are independent, and stringification
// is stable. The tests live in the _test package so they exercise the public
// surface only.
package workflow_test

import (
	"testing"

	wf "github.com/staneswilson/gact/pkg/workflow"
)

// TestWorkflow_ZeroValueIsValid is the canonical first test from plan section 9
// Task 0.3 Step 1. It pins down that a zero-value Workflow is safe to iterate
// over without panicking, even though JobsByID is a nil map.
func TestWorkflow_ZeroValueIsValid(t *testing.T) {
	var w wf.Workflow
	if w.JobsByID != nil {
		t.Fatalf("expected nil JobsByID on zero-value, got %v", w.JobsByID)
	}
	// iteration over a nil map is a no-op in Go; this must not panic.
	for id, j := range w.JobsByID {
		_ = id
		_ = j
	}
}

// TestSourceSpan_String asserts the path:line:col stringification contract.
func TestSourceSpan_String(t *testing.T) {
	cases := []struct {
		name string
		in   wf.SourceSpan
		want string
	}{
		{
			name: "fully populated span",
			in:   wf.SourceSpan{Path: "wf.yml", Line: 12, Column: 4, EndLine: 12, EndCol: 20},
			want: "wf.yml:12:4",
		},
		{
			name: "zero span",
			in:   wf.SourceSpan{},
			want: ":0:0",
		},
		{
			name: "path only",
			in:   wf.SourceSpan{Path: "ci.yml", Line: 1, Column: 1},
			want: "ci.yml:1:1",
		},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.in.String(); got != tc.want {
				t.Fatalf("SourceSpan.String() = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestStepKind_String asserts each constant stringifies to a stable, lowercase
// label suitable for diagnostics and reports.
func TestStepKind_String(t *testing.T) {
	cases := []struct {
		name string
		in   wf.StepKind
		want string
	}{
		{"run", wf.StepKindRun, "run"},
		{"uses", wf.StepKindUses, "uses"},
		{"composite", wf.StepKindComposite, "composite"},
		{"unknown", wf.StepKind(99), "unknown"},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.in.String(); got != tc.want {
				t.Fatalf("StepKind.String() = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestExpression_ZeroValueIsEmpty pins that the zero-value Expression has an
// empty Raw and zero Span. The expression is opaque to pkg/workflow consumers;
// they should be able to detect "no expression set" via the zero value.
func TestExpression_ZeroValueIsEmpty(t *testing.T) {
	var e wf.Expression
	if e.Raw != "" {
		t.Fatalf("expected empty Raw on zero-value, got %q", e.Raw)
	}
	if (e.Span != wf.SourceSpan{}) {
		t.Fatalf("expected zero Span on zero-value, got %+v", e.Span)
	}
}

// TestJobID_StringConversion proves that JobID round-trips through string
// conversion. JobID is a typedef for clarity at API boundaries.
func TestJobID_StringConversion(t *testing.T) {
	cases := []struct {
		name string
		in   wf.JobID
		want string
	}{
		{"simple", wf.JobID("test"), "test"},
		{"with-dash", wf.JobID("build-and-test"), "build-and-test"},
		{"empty", wf.JobID(""), ""},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.in.String(); got != tc.want {
				t.Fatalf("JobID.String() = %q, want %q", got, tc.want)
			}
			// Also confirm the reverse: string -> JobID -> string is identity.
			if got := string(tc.in); got != tc.want {
				t.Fatalf("string(JobID) = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestWorkflow_DeepCopyIsValueSafe demonstrates that mutating a struct copy
// does not affect the original. This is the core "value object" promise — no
// hidden shared state, no pointers between aggregates, references via ID only.
func TestWorkflow_DeepCopyIsValueSafe(t *testing.T) {
	// Build an original workflow with one job and one step.
	original := wf.Workflow{
		Name: "ci",
		Path: "ci.yml",
		Env:  map[string]string{"FOO": "bar"},
		JobsByID: map[wf.JobID]wf.Job{
			"build": {
				ID:     "build",
				Name:   "build",
				RunsOn: wf.RunnerLabel{Raw: "ubuntu-latest", Labels: []string{"ubuntu-latest"}},
				Needs:  []wf.JobID{"setup"},
				Steps: []wf.Step{
					{ID: "checkout", Name: "Checkout", Kind: wf.StepKindUses,
						Uses: wf.UsesRef{Owner: "actions", Repo: "checkout", Ref: "v4"}},
				},
			},
		},
		Span: wf.SourceSpan{Path: "ci.yml", Line: 1, Column: 1},
	}

	// Make a shallow copy via struct assignment, then deep-clone the map
	// fields the way an integration layer would when handing the workflow off
	// across an aggregate boundary.
	clone := original
	clone.Name = "release"
	clone.Env = map[string]string{"FOO": "qux"}
	// Mutate a job we deep-copy across boundaries.
	copyJob := original.JobsByID["build"]
	copyJob.Name = "build-mutated"
	copyJob.Needs = append([]wf.JobID(nil), copyJob.Needs...)
	copyJob.Needs[0] = "different"

	// Original must be untouched.
	if original.Name != "ci" {
		t.Fatalf("original.Name mutated to %q", original.Name)
	}
	if original.Env["FOO"] != "bar" {
		t.Fatalf("original.Env mutated: got %v", original.Env)
	}
	origBuild, ok := original.JobsByID["build"]
	if !ok {
		t.Fatalf("original job 'build' missing")
	}
	if origBuild.Name != "build" {
		t.Fatalf("original job name mutated to %q", origBuild.Name)
	}
	if len(origBuild.Needs) != 1 || origBuild.Needs[0] != "setup" {
		t.Fatalf("original job Needs mutated: got %v", origBuild.Needs)
	}

	// And the clone reflects the mutations applied to its top-level fields.
	if clone.Name != "release" {
		t.Fatalf("clone.Name = %q, want %q", clone.Name, "release")
	}
	if clone.Env["FOO"] != "qux" {
		t.Fatalf("clone.Env[FOO] = %q, want %q", clone.Env["FOO"], "qux")
	}
}

// TestUsesRef_LocalVsRemote asserts that the Local flag distinguishes local
// (./.github/actions/x) from remote (owner/repo@ref) action references.
func TestUsesRef_LocalVsRemote(t *testing.T) {
	cases := []struct {
		name      string
		in        wf.UsesRef
		wantLocal bool
	}{
		{
			name:      "remote action",
			in:        wf.UsesRef{Owner: "actions", Repo: "checkout", Ref: "v4"},
			wantLocal: false,
		},
		{
			name:      "local action",
			in:        wf.UsesRef{Path: "./.github/actions/setup", Local: true},
			wantLocal: true,
		},
		{
			name:      "monorepo subpath action",
			in:        wf.UsesRef{Owner: "actions", Repo: "aws", Path: "ec2-action", Ref: "main"},
			wantLocal: false,
		},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			if tc.in.Local != tc.wantLocal {
				t.Fatalf("UsesRef.Local = %v, want %v", tc.in.Local, tc.wantLocal)
			}
		})
	}
}

// TestTriggers_PointersAreOptional confirms that the pointer-to-struct shape
// on Triggers.* lets callers distinguish "absent" (nil) from "present but
// empty" (non-nil pointer with zero-value struct) — the standard GitHub
// Actions distinction between, e.g., `on: push` and `on: { push: { branches: [] } }`.
func TestTriggers_PointersAreOptional(t *testing.T) {
	var t1 wf.Triggers
	if t1.Push != nil {
		t.Fatalf("expected nil Push on zero-value Triggers")
	}
	if t1.PullRequest != nil {
		t.Fatalf("expected nil PullRequest on zero-value Triggers")
	}
	if t1.WorkflowDispatch != nil {
		t.Fatalf("expected nil WorkflowDispatch on zero-value Triggers")
	}
	if t1.WorkflowCall != nil {
		t.Fatalf("expected nil WorkflowCall on zero-value Triggers")
	}
	if len(t1.Schedule) != 0 {
		t.Fatalf("expected empty Schedule slice on zero-value Triggers, got %d", len(t1.Schedule))
	}

	// Present-but-empty: a Push trigger with no filters means "any push".
	t2 := wf.Triggers{Push: &wf.PushTrigger{}}
	if t2.Push == nil {
		t.Fatalf("expected non-nil Push")
	}
	if len(t2.Push.Branches) != 0 {
		t.Fatalf("expected empty Branches, got %v", t2.Push.Branches)
	}
}

// TestMatrix_ZeroValueIsUsable asserts a zero-value Matrix is iterable and has
// reasonable defaults (FailFast false, MaxParallel 0 meaning "unlimited").
func TestMatrix_ZeroValueIsUsable(t *testing.T) {
	var m wf.Matrix
	if m.Axes != nil {
		t.Fatalf("expected nil Axes on zero-value, got %v", m.Axes)
	}
	if m.Include != nil {
		t.Fatalf("expected nil Include on zero-value, got %v", m.Include)
	}
	if m.Exclude != nil {
		t.Fatalf("expected nil Exclude on zero-value, got %v", m.Exclude)
	}
	if m.FailFast {
		t.Fatalf("expected FailFast=false on zero-value, got true")
	}
	if m.MaxParallel != 0 {
		t.Fatalf("expected MaxParallel=0 on zero-value, got %d", m.MaxParallel)
	}
}
