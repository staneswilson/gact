package schema

import "github.com/staneswilson/gact/internal/diag"

// Diagnostic converts a SchemaError into the unified diag.Diagnostic value
// the rest of gact (the lint CLI, the LSP, future formatters) consumes.
//
// Severity is always diag.SeverityError because schema violations are
// structural — a workflow that does not satisfy the structural invariants
// cannot be executed, listed accurately, or analysed further. Anything that
// is "merely advisory" lives in a separate lint pass, not in this validator.
func (e SchemaError) Diagnostic() diag.Diagnostic {
	return diag.Diagnostic{
		Path:     e.Path,
		Span:     e.Span,
		Severity: diag.SeverityError,
		Message:  e.Message,
		Code:     e.Code,
	}
}

// Diagnostics converts a slice of SchemaError to a slice of diag.Diagnostic,
// preserving order. Returns nil for a nil input so the renderer can treat the
// "no errors" and "no validator was run" cases identically.
func Diagnostics(errs []SchemaError) []diag.Diagnostic {
	if errs == nil {
		return nil
	}
	out := make([]diag.Diagnostic, len(errs))
	for i, e := range errs {
		out[i] = e.Diagnostic()
	}
	return out
}
