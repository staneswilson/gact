package schema

import (
	"strings"
	"testing"

	wf "github.com/staneswilson/gact/pkg/workflow"
)

// findCode reports whether the diagnostics slice contains an entry whose Code
// matches the supplied value. It is used by table tests to assert that a
// particular violation surfaces without imposing strict ordering on the
// returned slice.
func findCode(errs []SchemaError, code string) *SchemaError {
	for i := range errs {
		if errs[i].Code == code {
			return &errs[i]
		}
	}
	return nil
}

// happyPathWorkflow returns a minimal but complete Workflow that exercises
// every required surface (triggers, jobs, runs-on, steps with both run and
// uses kinds). Validate must return an empty diagnostics slice on this input.
func happyPathWorkflow() wf.Workflow {
	return wf.Workflow{
		Name: "ci",
		Path: ".github/workflows/ci.yml",
		Triggers: wf.Triggers{
			Push: &wf.PushTrigger{Branches: []string{"main"}},
		},
		JobsByID: map[wf.JobID]wf.Job{
			"build": {
				ID:     "build",
				RunsOn: wf.RunnerLabel{Raw: "ubuntu-latest", Labels: []string{"ubuntu-latest"}},
				Steps: []wf.Step{
					{
						Kind: wf.StepKindUses,
						Uses: wf.UsesRef{Owner: "actions", Repo: "checkout", Ref: "v4"},
					},
					{
						Kind: wf.StepKindRun,
						Run:  "echo hello",
					},
				},
			},
			"test": {
				ID:     "test",
				RunsOn: wf.RunnerLabel{Raw: "ubuntu-latest", Labels: []string{"ubuntu-latest"}},
				Needs:  []wf.JobID{"build"},
				Steps: []wf.Step{
					{Kind: wf.StepKindRun, Run: "echo testing"},
				},
			},
		},
	}
}

func TestValidate_HappyPath_NoDiagnostics(t *testing.T) {
	got := Validate(happyPathWorkflow())
	if len(got) != 0 {
		t.Fatalf("expected no diagnostics, got %d: %+v", len(got), got)
	}
}

func TestValidate_TopLevel_NoTriggers(t *testing.T) {
	w := happyPathWorkflow()
	w.Triggers = wf.Triggers{}
	got := Validate(w)
	if findCode(got, CodeNoTriggers) == nil {
		t.Fatalf("expected diagnostic with code %s, got %+v", CodeNoTriggers, got)
	}
}

func TestValidate_TopLevel_NoTriggers_OtherSliceCountsAsPresent(t *testing.T) {
	w := happyPathWorkflow()
	w.Triggers = wf.Triggers{Other: []string{"release"}}
	got := Validate(w)
	if findCode(got, CodeNoTriggers) != nil {
		t.Fatalf("Other slice should satisfy the trigger requirement, got %+v", got)
	}
}

func TestValidate_TopLevel_NoJobs(t *testing.T) {
	w := happyPathWorkflow()
	w.JobsByID = nil
	got := Validate(w)
	if findCode(got, CodeNoJobs) == nil {
		t.Fatalf("expected diagnostic with code %s, got %+v", CodeNoJobs, got)
	}
}

func TestValidate_TopLevel_EmptyJobsMap(t *testing.T) {
	w := happyPathWorkflow()
	w.JobsByID = map[wf.JobID]wf.Job{}
	got := Validate(w)
	if findCode(got, CodeNoJobs) == nil {
		t.Fatalf("expected diagnostic with code %s, got %+v", CodeNoJobs, got)
	}
}

func TestValidate_Job_EmptyRunsOn(t *testing.T) {
	w := happyPathWorkflow()
	j := w.JobsByID["build"]
	j.RunsOn = wf.RunnerLabel{}
	w.JobsByID["build"] = j
	got := Validate(w)
	if findCode(got, CodeJobRunsOnEmpty) == nil {
		t.Fatalf("expected diagnostic with code %s, got %+v", CodeJobRunsOnEmpty, got)
	}
}

func TestValidate_Job_UnknownNeeds(t *testing.T) {
	w := happyPathWorkflow()
	j := w.JobsByID["test"]
	j.Needs = []wf.JobID{"ghost"}
	w.JobsByID["test"] = j
	got := Validate(w)
	d := findCode(got, CodeJobUnknownNeeds)
	if d == nil {
		t.Fatalf("expected diagnostic with code %s, got %+v", CodeJobUnknownNeeds, got)
	}
	if !strings.Contains(d.Message, "test") || !strings.Contains(d.Message, "ghost") {
		t.Fatalf("message %q should cite both job IDs (referrer + referent)", d.Message)
	}
}

func TestValidate_Job_EmptySteps(t *testing.T) {
	w := happyPathWorkflow()
	j := w.JobsByID["build"]
	j.Steps = nil
	w.JobsByID["build"] = j
	got := Validate(w)
	if findCode(got, CodeJobNoSteps) == nil {
		t.Fatalf("expected diagnostic with code %s, got %+v", CodeJobNoSteps, got)
	}
}

func TestValidate_Job_NegativeTimeout(t *testing.T) {
	w := happyPathWorkflow()
	j := w.JobsByID["build"]
	j.TimeoutMinutes = -1
	w.JobsByID["build"] = j
	got := Validate(w)
	if findCode(got, CodeJobNegativeTimeout) == nil {
		t.Fatalf("expected diagnostic with code %s, got %+v", CodeJobNegativeTimeout, got)
	}
}

func TestValidate_Step_Empty(t *testing.T) {
	w := happyPathWorkflow()
	j := w.JobsByID["build"]
	j.Steps = []wf.Step{{Kind: wf.StepKindRun}}
	w.JobsByID["build"] = j
	got := Validate(w)
	if findCode(got, CodeStepEmpty) == nil {
		t.Fatalf("expected diagnostic with code %s, got %+v", CodeStepEmpty, got)
	}
}

func TestValidate_Step_Ambiguous(t *testing.T) {
	w := happyPathWorkflow()
	j := w.JobsByID["build"]
	j.Steps = []wf.Step{{
		Kind: wf.StepKindRun,
		Run:  "echo hi",
		Uses: wf.UsesRef{Owner: "actions", Repo: "checkout", Ref: "v4"},
	}}
	w.JobsByID["build"] = j
	got := Validate(w)
	if findCode(got, CodeStepAmbiguous) == nil {
		t.Fatalf("expected diagnostic with code %s, got %+v", CodeStepAmbiguous, got)
	}
}

func TestValidate_Step_ShellOnUses(t *testing.T) {
	w := happyPathWorkflow()
	j := w.JobsByID["build"]
	j.Steps = []wf.Step{{
		Kind:  wf.StepKindUses,
		Uses:  wf.UsesRef{Owner: "actions", Repo: "checkout", Ref: "v4"},
		Shell: "bash",
	}}
	w.JobsByID["build"] = j
	got := Validate(w)
	if findCode(got, CodeStepShellOnUses) == nil {
		t.Fatalf("expected diagnostic with code %s, got %+v", CodeStepShellOnUses, got)
	}
}

func TestValidate_Step_NegativeTimeout(t *testing.T) {
	w := happyPathWorkflow()
	j := w.JobsByID["build"]
	j.Steps = []wf.Step{{
		Kind:           wf.StepKindRun,
		Run:            "echo hi",
		TimeoutMinutes: -5,
	}}
	w.JobsByID["build"] = j
	got := Validate(w)
	if findCode(got, CodeStepNegativeTimeout) == nil {
		t.Fatalf("expected diagnostic with code %s, got %+v", CodeStepNegativeTimeout, got)
	}
}

func TestValidate_Matrix_NegativeMaxParallel(t *testing.T) {
	w := happyPathWorkflow()
	j := w.JobsByID["build"]
	j.Matrix = &wf.Matrix{
		Axes:        map[string][]any{"node": {18, 20}},
		MaxParallel: -1,
	}
	w.JobsByID["build"] = j
	got := Validate(w)
	if findCode(got, CodeMatrixNegativeMaxParallel) == nil {
		t.Fatalf("expected diagnostic with code %s, got %+v", CodeMatrixNegativeMaxParallel, got)
	}
}

func TestValidate_Matrix_IncludeTypeMismatch(t *testing.T) {
	w := happyPathWorkflow()
	j := w.JobsByID["build"]
	j.Matrix = &wf.Matrix{
		Axes: map[string][]any{"node": {18, 20}},
		Include: []map[string]any{
			{"node": "18"}, // string disagrees with int axis values.
		},
	}
	w.JobsByID["build"] = j
	got := Validate(w)
	if findCode(got, CodeMatrixIncludeTypeMismatch) == nil {
		t.Fatalf("expected diagnostic with code %s, got %+v", CodeMatrixIncludeTypeMismatch, got)
	}
}

func TestValidate_Matrix_IncludeTypeMatchOnAxis_NoMismatch(t *testing.T) {
	// Sanity check: when include values are the same kind as the axis values,
	// no diagnostic should fire. This guards against false positives in the
	// type-comparison heuristic.
	w := happyPathWorkflow()
	j := w.JobsByID["build"]
	j.Matrix = &wf.Matrix{
		Axes: map[string][]any{"node": {18, 20}},
		Include: []map[string]any{
			{"node": 22}, // same int kind as axis values.
		},
	}
	w.JobsByID["build"] = j
	got := Validate(w)
	if findCode(got, CodeMatrixIncludeTypeMismatch) != nil {
		t.Fatalf("did not expect a type-mismatch diagnostic, got %+v", got)
	}
}

func TestValidate_Matrix_IncludeNewAxis_NoMismatch(t *testing.T) {
	// Include entries may introduce wholly new keys; that is legal and must
	// not trigger a type-mismatch check (there is no declared axis to compare
	// against).
	w := happyPathWorkflow()
	j := w.JobsByID["build"]
	j.Matrix = &wf.Matrix{
		Axes: map[string][]any{"node": {18, 20}},
		Include: []map[string]any{
			{"experimental": true},
		},
	}
	w.JobsByID["build"] = j
	got := Validate(w)
	if findCode(got, CodeMatrixIncludeTypeMismatch) != nil {
		t.Fatalf("did not expect a type-mismatch diagnostic, got %+v", got)
	}
}

func TestValidate_DoesNotShortCircuit(t *testing.T) {
	// A workflow with multiple violations must surface every one of them;
	// the validator should not stop at the first error.
	w := wf.Workflow{
		Path:     "/wf/ci.yml",
		Triggers: wf.Triggers{}, // missing triggers
		JobsByID: map[wf.JobID]wf.Job{
			"build": {
				ID: "build",
				// missing runs-on
				// missing steps
				TimeoutMinutes: -2, // negative timeout
			},
		},
	}
	got := Validate(w)
	for _, code := range []string{
		CodeNoTriggers,
		CodeJobRunsOnEmpty,
		CodeJobNoSteps,
		CodeJobNegativeTimeout,
	} {
		if findCode(got, code) == nil {
			t.Errorf("expected diagnostic %s, missing in %+v", code, got)
		}
	}
}

func TestSchemaError_ErrorString_Format(t *testing.T) {
	e := SchemaError{
		Path:    "/wf/ci.yml",
		Span:    wf.SourceSpan{Path: "/wf/ci.yml", Line: 4, Column: 3},
		Message: "job 'build' has no steps",
		Code:    CodeJobNoSteps,
	}
	want := "/wf/ci.yml:4:3: job 'build' has no steps [" + CodeJobNoSteps + "]"
	if got := e.Error(); got != want {
		t.Fatalf("Error() = %q, want %q", got, want)
	}
}

func TestSchemaError_ErrorString_FallsBackToPathWhenSpanEmpty(t *testing.T) {
	// When a span has not been populated, the SchemaError should still print
	// the Path it carries so users can locate the file even without a
	// line/col anchor.
	e := SchemaError{
		Path:    "/wf/ci.yml",
		Message: "workflow has no triggers",
		Code:    CodeNoTriggers,
	}
	got := e.Error()
	if !strings.HasPrefix(got, "/wf/ci.yml:") {
		t.Fatalf("Error() = %q, want prefix %q", got, "/wf/ci.yml:")
	}
	if !strings.Contains(got, CodeNoTriggers) {
		t.Fatalf("Error() = %q, want it to contain code %q", got, CodeNoTriggers)
	}
}
