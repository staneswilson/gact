// Package schema validates the structural shape of a materialised workflow
// IR. It is intentionally separate from YAML parsing (which concerns itself
// with surface syntax) and from expression evaluation (which concerns itself
// with runtime semantics): once a Workflow has been built into pkg/workflow's
// value objects, this package answers the question "does this Workflow obey
// the structural invariants the rest of gact relies on?".
//
// Diagnostic codes are stable identifiers prefixed with "SCHEMA-" so that
// downstream tools (the lint CLI, the language server) can suppress or escape
// individual classes without depending on the human-readable Message.
//
// The Validate function never short-circuits. It walks the entire IR and
// collects every violation it finds; the caller decides whether to render all
// of them, just the first, or to filter by code.
//
// Coupling note: pkg/workflow is the only foreign import on purpose. Once the
// sibling package internal/diag stabilises, a thin adapter will convert
// SchemaError → diag.Diagnostic; the validator itself stays free of that
// dependency so it can be reused from contexts (tests, REPLs, plugins) where
// pulling the diag formatter is excessive.
package schema

import (
	"fmt"
	"sort"
	"strconv"

	wf "github.com/staneswilson/gact/pkg/workflow"
)

// Diagnostic codes. The exact string values are part of the package's public
// contract because they appear in CLI output and may be referenced by users
// in suppression directives. The "SCHEMA-" prefix matches the convention used
// by neighbouring lint packages and keeps codes greppable.
const (
	CodeNoTriggers                = "SCHEMA-NO-TRIGGERS"
	CodeNoJobs                    = "SCHEMA-NO-JOBS"
	CodeJobRunsOnEmpty            = "SCHEMA-JOB-RUNS-ON-EMPTY"
	CodeJobUnknownNeeds           = "SCHEMA-JOB-UNKNOWN-NEEDS"
	CodeJobNoSteps                = "SCHEMA-JOB-NO-STEPS"
	CodeJobNegativeTimeout        = "SCHEMA-JOB-NEGATIVE-TIMEOUT"
	CodeStepEmpty                 = "SCHEMA-STEP-EMPTY"
	CodeStepAmbiguous             = "SCHEMA-STEP-AMBIGUOUS"
	CodeStepShellOnUses           = "SCHEMA-STEP-SHELL-ON-USES"
	CodeStepNegativeTimeout       = "SCHEMA-STEP-NEGATIVE-TIMEOUT"
	CodeMatrixNegativeMaxParallel = "SCHEMA-MATRIX-NEGATIVE-MAX-PARALLEL"
	CodeMatrixIncludeTypeMismatch = "SCHEMA-MATRIX-INCLUDE-TYPE-MISMATCH"
)

// SchemaError is a single structural diagnostic. It is intentionally
// self-contained: this package must not import internal/diag (which is being
// built in parallel and is not yet on disk). Once diag lands, a small adapter
// in another package will translate SchemaError to diag.Diagnostic; nothing
// in this file needs to change.
//
// Path is the workflow file path so that errors emitted from contexts that do
// not carry a populated Span (e.g. "the whole workflow has no triggers") can
// still cite a location.
type SchemaError struct {
	Path    string
	Span    wf.SourceSpan
	Message string
	Code    string
}

// Error renders the diagnostic in the conventional
// "<path>:<line>:<col>: <message> [<code>]" form that compilers use, so the
// output threads naturally through editor problem matchers. When the span has
// not been populated (Line == 0 because the violation pertains to the file as
// a whole) the line/column degrade gracefully to zeros — the path and code
// are always present, which is what tooling actually keys on.
func (e SchemaError) Error() string {
	path := e.Span.Path
	if path == "" {
		path = e.Path
	}
	return fmt.Sprintf("%s:%d:%d: %s [%s]", path, e.Span.Line, e.Span.Column, e.Message, e.Code)
}

// Validate runs every structural check this package knows about and returns
// the full set of diagnostics. It never short-circuits; callers that want
// "first error wins" semantics can slice the result. The order is
// deterministic — top-level checks, then jobs sorted by ID, then steps in
// source order — so test fixtures and golden files remain stable.
func Validate(w wf.Workflow) []SchemaError {
	var errs []SchemaError

	errs = append(errs, validateTriggers(w)...)

	if len(w.JobsByID) == 0 {
		errs = append(errs, SchemaError{
			Path:    w.Path,
			Span:    w.Span,
			Message: "workflow has no jobs",
			Code:    CodeNoJobs,
		})
		// Even with no jobs we keep going — there is nothing more to walk,
		// but appending an empty slice is harmless and the structure makes
		// future additions (e.g. top-level env checks) obvious.
		return errs
	}

	// Walk jobs in a stable order. Map iteration is randomised, so without
	// this sort the test for "multiple violations" would be flaky.
	ids := make([]string, 0, len(w.JobsByID))
	for id := range w.JobsByID {
		ids = append(ids, string(id))
	}
	sort.Strings(ids)

	for _, id := range ids {
		job := w.JobsByID[wf.JobID(id)]
		errs = append(errs, validateJob(w, job)...)
	}

	return errs
}

// validateTriggers checks the top-level `on:` block. The IR represents an
// absent trigger as a nil pointer (or empty slice), so the test is purely
// structural; we deliberately do not try to recognise individual event names.
//
// The Other slice exists for forward compatibility: a workflow that only
// listens to a custom or future event is still considered to have triggers.
func validateTriggers(w wf.Workflow) []SchemaError {
	t := w.Triggers
	hasAny := t.Push != nil ||
		t.PullRequest != nil ||
		t.PullRequestTarget != nil ||
		len(t.Schedule) > 0 ||
		t.WorkflowDispatch != nil ||
		t.WorkflowCall != nil ||
		t.WorkflowRun != nil ||
		len(t.Other) > 0
	if hasAny {
		return nil
	}
	return []SchemaError{{
		Path:    w.Path,
		Span:    w.Span,
		Message: "workflow has no triggers (on: block is empty)",
		Code:    CodeNoTriggers,
	}}
}

// validateJob inspects a single Job. It checks invariants that hold
// regardless of the rest of the workflow, and one cross-aggregate invariant
// (Needs referring to existing jobs) using the JobsByID map on the workflow.
func validateJob(w wf.Workflow, j wf.Job) []SchemaError {
	var errs []SchemaError
	jobID := string(j.ID)

	// runs-on must be populated. Either Labels carries at least one entry
	// (the normalised representation) or Raw carries the original textual
	// form (e.g. an expression we have not parsed). Both being empty means
	// the job has nothing to schedule on.
	if len(j.RunsOn.Labels) == 0 && j.RunsOn.Raw == "" {
		errs = append(errs, SchemaError{
			Path:    w.Path,
			Span:    j.Span,
			Message: "job " + quote(jobID) + " has empty runs-on",
			Code:    CodeJobRunsOnEmpty,
		})
	}

	// needs must reference jobs that actually exist in the workflow.
	for _, need := range j.Needs {
		if _, ok := w.JobsByID[need]; !ok {
			errs = append(errs, SchemaError{
				Path:    w.Path,
				Span:    j.Span,
				Message: "job " + quote(jobID) + " needs unknown job " + quote(string(need)),
				Code:    CodeJobUnknownNeeds,
			})
		}
	}

	if len(j.Steps) == 0 {
		errs = append(errs, SchemaError{
			Path:    w.Path,
			Span:    j.Span,
			Message: "job " + quote(jobID) + " has no steps",
			Code:    CodeJobNoSteps,
		})
	}

	if j.TimeoutMinutes < 0 {
		errs = append(errs, SchemaError{
			Path:    w.Path,
			Span:    j.Span,
			Message: "job " + quote(jobID) + " has negative timeout-minutes (" + strconv.Itoa(j.TimeoutMinutes) + ")",
			Code:    CodeJobNegativeTimeout,
		})
	}

	if j.Matrix != nil {
		errs = append(errs, validateMatrix(w, j, *j.Matrix)...)
	}

	for i, s := range j.Steps {
		errs = append(errs, validateStep(w, j, i, s)...)
	}

	return errs
}

// validateStep applies the per-step structural rules. A step is well-formed
// when it is either run-shaped (Run text supplied) or uses-shaped (Local
// flag set or Owner populated) but never both and never neither. Shell on a
// uses-step is reported as a separate diagnostic because the step still has
// something to do — it just makes the authoring intent ambiguous.
//
// Judgement call: the task description labels several "warnings" but the
// public surface only carries a Code, not a Severity. We surface every
// violation as a SchemaError and trust the caller to map codes to severity
// where it matters (e.g. CodeStepShellOnUses and CodeMatrixIncludeTypeMismatch
// are warning-grade; the rest are error-grade). Tying severity to code
// without owning a diag.Severity type would be premature — Task 0.14's diag
// package will own that vocabulary.
func validateStep(w wf.Workflow, j wf.Job, idx int, s wf.Step) []SchemaError {
	var errs []SchemaError
	loc := stepLabel(j, idx, s)

	usesShaped := s.Uses.Local || s.Uses.Owner != ""
	runShaped := s.Run != ""

	switch {
	case !usesShaped && !runShaped:
		errs = append(errs, SchemaError{
			Path:    w.Path,
			Span:    s.Span,
			Message: loc + " has neither 'uses' nor 'run'",
			Code:    CodeStepEmpty,
		})
	case usesShaped && runShaped:
		errs = append(errs, SchemaError{
			Path:    w.Path,
			Span:    s.Span,
			Message: loc + " has both 'uses' and 'run' (only one is permitted)",
			Code:    CodeStepAmbiguous,
		})
	}

	// Shell only applies to run-style steps. When the step is a uses-step
	// the shell setting is silently dropped at run time; surface that as a
	// dedicated diagnostic so the author can see what they wrote.
	if s.Kind == wf.StepKindUses && s.Shell != "" {
		errs = append(errs, SchemaError{
			Path:    w.Path,
			Span:    s.Span,
			Message: loc + " sets 'shell' on a uses-step (shell is ignored for uses)",
			Code:    CodeStepShellOnUses,
		})
	}

	if s.TimeoutMinutes < 0 {
		errs = append(errs, SchemaError{
			Path:    w.Path,
			Span:    s.Span,
			Message: loc + " has negative timeout-minutes (" + strconv.Itoa(s.TimeoutMinutes) + ")",
			Code:    CodeStepNegativeTimeout,
		})
	}

	return errs
}

// validateMatrix inspects the matrix block for a job. We only check
// invariants that can be evaluated without expanding the matrix (which is
// the scheduler's job): negative MaxParallel and obvious type disagreement
// between Include entries and declared axes.
func validateMatrix(w wf.Workflow, j wf.Job, m wf.Matrix) []SchemaError {
	var errs []SchemaError
	jobID := string(j.ID)

	if m.MaxParallel < 0 {
		errs = append(errs, SchemaError{
			Path:    w.Path,
			Span:    j.Span,
			Message: "job " + quote(jobID) + " matrix has negative max-parallel (" + strconv.Itoa(m.MaxParallel) + ")",
			Code:    CodeMatrixNegativeMaxParallel,
		})
	}

	// Include entries may reference declared axes or introduce wholly new
	// keys. When they reference a declared axis, the value's Go kind must
	// agree with the kinds already used on that axis; otherwise the
	// resulting expanded matrix will mix types in a way that almost always
	// indicates a typo (e.g. node: "18" vs node: 18).
	for incIdx, inc := range m.Include {
		// Walk the include keys in a stable order so the diagnostic
		// sequence is deterministic for table tests.
		keys := make([]string, 0, len(inc))
		for k := range inc {
			keys = append(keys, k)
		}
		sort.Strings(keys)

		for _, key := range keys {
			axisValues, ok := m.Axes[key]
			if !ok || len(axisValues) == 0 {
				// New axis introduced by include — not a mismatch.
				continue
			}
			incValue := inc[key]
			axisKind := kindOf(axisValues[0])
			if axisKind == "" {
				continue
			}
			incKind := kindOf(incValue)
			if incKind == "" {
				continue
			}
			if axisKind != incKind {
				errs = append(errs, SchemaError{
					Path: w.Path,
					Span: j.Span,
					Message: fmt.Sprintf(
						"job %s matrix include[%d].%s is %s but axis %q is %s",
						quote(jobID), incIdx, key, incKind, key, axisKind,
					),
					Code: CodeMatrixIncludeTypeMismatch,
				})
			}
		}
	}

	return errs
}

// kindOf collapses a Go value into one of a handful of named buckets that
// matter for matrix type comparison. We intentionally treat all numeric Go
// types as "number" because YAML decoders can choose int, int64, or float64
// for the same source literal depending on shape.
func kindOf(v any) string {
	switch v.(type) {
	case nil:
		return "" // skip — cannot classify
	case bool:
		return "bool"
	case string:
		return "string"
	case int, int8, int16, int32, int64,
		uint, uint8, uint16, uint32, uint64,
		float32, float64:
		return "number"
	case []any:
		return "array"
	case map[string]any:
		return "object"
	default:
		return "" // unknown — skip rather than emit a spurious mismatch
	}
}

// stepLabel produces a short human-readable description of a step suitable
// for the start of a diagnostic message. It prefers the explicit step ID,
// falls back to the step name, and last to the positional index.
func stepLabel(j wf.Job, idx int, s wf.Step) string {
	switch {
	case s.ID != "":
		return "step " + quote(s.ID) + " (job " + quote(string(j.ID)) + ")"
	case s.Name != "":
		return "step " + quote(s.Name) + " (job " + quote(string(j.ID)) + ")"
	default:
		return "step #" + strconv.Itoa(idx) + " (job " + quote(string(j.ID)) + ")"
	}
}

// quote returns the string wrapped in single quotes for readability inside
// diagnostic messages. Single quotes (rather than %q) keep the output free of
// Go's escape sequences when the identifier is already safe.
func quote(s string) string { return "'" + s + "'" }
