package schema_test

import (
	"testing"

	"github.com/staneswilson/gact/internal/authoring/schema"
	"github.com/staneswilson/gact/internal/diag"
	wf "github.com/staneswilson/gact/pkg/workflow"
)

func TestSchemaError_Diagnostic_MapsFieldsOneToOne(t *testing.T) {
	e := schema.SchemaError{
		Path:    "ci.yml",
		Span:    wf.SourceSpan{Path: "ci.yml", Line: 7, Column: 3},
		Message: "job 'test' has no steps",
		Code:    schema.CodeJobNoSteps,
	}

	d := e.Diagnostic()

	if d.Path != "ci.yml" {
		t.Errorf("Path = %q, want %q", d.Path, "ci.yml")
	}
	if d.Span != e.Span {
		t.Errorf("Span = %+v, want %+v", d.Span, e.Span)
	}
	if d.Message != "job 'test' has no steps" {
		t.Errorf("Message = %q, want %q", d.Message, "job 'test' has no steps")
	}
	if d.Code != schema.CodeJobNoSteps {
		t.Errorf("Code = %q, want %q", d.Code, schema.CodeJobNoSteps)
	}
	if d.Severity != diag.SeverityError {
		t.Errorf("Severity = %v, want %v", d.Severity, diag.SeverityError)
	}
}

func TestSchemaError_Diagnostic_AlwaysSeverityError(t *testing.T) {
	// Schema violations are never warnings — a workflow that fails structural
	// validation cannot be executed. Document the invariant with a test so a
	// future drive-by refactor cannot silently downgrade severity.
	cases := []schema.SchemaError{
		{Code: schema.CodeNoTriggers, Message: "no triggers"},
		{Code: schema.CodeStepAmbiguous, Message: "uses and run both set"},
		{Code: schema.CodeJobUnknownNeeds, Message: "unknown needs"},
	}
	for _, e := range cases {
		if got := e.Diagnostic().Severity; got != diag.SeverityError {
			t.Errorf("code %s: severity = %v, want SeverityError", e.Code, got)
		}
	}
}

func TestDiagnostics_ConvertsSlice(t *testing.T) {
	in := []schema.SchemaError{
		{Path: "a.yml", Message: "first", Code: schema.CodeNoTriggers},
		{Path: "b.yml", Message: "second", Code: schema.CodeNoJobs},
	}

	got := schema.Diagnostics(in)

	if len(got) != 2 {
		t.Fatalf("len = %d, want 2", len(got))
	}
	if got[0].Message != "first" || got[1].Message != "second" {
		t.Errorf("order not preserved: %+v", got)
	}
	if got[0].Severity != diag.SeverityError || got[1].Severity != diag.SeverityError {
		t.Errorf("severities = (%v, %v), want both error", got[0].Severity, got[1].Severity)
	}
}

func TestDiagnostics_NilInputReturnsNil(t *testing.T) {
	if got := schema.Diagnostics(nil); got != nil {
		t.Errorf("Diagnostics(nil) = %+v, want nil", got)
	}
}
