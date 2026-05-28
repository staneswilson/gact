// Package diag is the unified diagnostics framework for gact.
//
// Other contexts (parser, schema validator, linter, LSP, CLI) construct
// Diagnostic values and hand them to Render for emission. The package is
// intentionally a "log/slog style" pure data + render-function shape: no
// global state, no hidden buffers, no third-party dependencies.
//
// Stability: this package is internal and is not part of gact's public IR.
// pkg/workflow is the public surface; diag only consumes wf.SourceSpan.
package diag

import (
	"strconv"

	wf "github.com/staneswilson/gact/pkg/workflow"
)

// Severity classifies a diagnostic. The zero value is SeverityError so that
// callers cannot silently downgrade an unset severity to "info".
type Severity int

// Severity values. Numeric values are not part of the API; only the names
// and their String() form are stable.
const (
	// SeverityError indicates a hard failure: the workflow cannot be
	// executed, parsed, or otherwise processed as-is. CLI exits non-zero
	// when any error diagnostics are emitted.
	SeverityError Severity = iota

	// SeverityWarning indicates a likely problem that does not block
	// execution. Lints and deprecations live here.
	SeverityWarning

	// SeverityInfo is for advisory notes (e.g. "consider pinning by SHA").
	// CLI never exits non-zero on info diagnostics alone.
	SeverityInfo
)

// String returns the stable, lowercase label for a severity. Unknown values
// stringify to "unknown" — this matches the convention used elsewhere in the
// project (see pkg/workflow.StepKind.String).
func (s Severity) String() string {
	switch s {
	case SeverityError:
		return "error"
	case SeverityWarning:
		return "warning"
	case SeverityInfo:
		return "info"
	default:
		return "unknown"
	}
}

// Diagnostic is a single positioned message produced by a tool stage.
// Diagnostic is a value object — zero values are valid and copies are
// independent. Callers populate it directly; there is no constructor.
//
// Path and Span both carry path information by design. Path is the file the
// diagnostic is "about" (the file the user runs the tool against), while
// Span.Path is where the offending token physically lives, which may be a
// referenced action or reusable workflow file. When Path is empty, the
// renderer uses Span.Path as the displayed path. This lets simple callers
// omit Path entirely without losing locality.
//
// Code is a short, stable identifier (e.g. "GACT001") that users can pass to
// --ignore/--enable flags. Empty Code is permitted for ad-hoc diagnostics;
// the renderer omits the trailing "[CODE]" segment when Code is empty.
type Diagnostic struct {
	// Path is the file the diagnostic is about. May be empty, in which case
	// Span.Path is used.
	Path string

	// Span locates the offending token in source. Line and Column are
	// 1-based to match the YAML library and GitHub's own diagnostics.
	Span wf.SourceSpan

	// Severity classifies the diagnostic.
	Severity Severity

	// Message is the human-readable description. Should be a complete
	// sentence-fragment with no leading severity prefix (the renderer adds
	// it). Multi-line messages are permitted; JSON escapes them, the text
	// renderer emits them verbatim.
	Message string

	// Code is a short stable identifier like "GACT001". Optional.
	Code string
}

// effectivePath returns the path the renderer should display: Path if set,
// otherwise Span.Path. Kept private to centralise the fallback rule.
func (d Diagnostic) effectivePath() string {
	if d.Path != "" {
		return d.Path
	}
	return d.Span.Path
}

// formatPosition renders the displayed "path:line:column" prefix used by the
// text renderer. Path comes from effectivePath; line/col come from Span.
func (d Diagnostic) formatPosition() string {
	return d.effectivePath() + ":" + strconv.Itoa(d.Span.Line) + ":" + strconv.Itoa(d.Span.Column)
}
