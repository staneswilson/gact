// Render diagnostics in a stable, sorted, format-selectable way. Designed so
// CLI, LSP, and tests can all share one emitter.

package diag

import (
	"encoding/json"
	"fmt"
	"io"
	"sort"
)

// Format selects the output shape produced by Render. The zero value is
// FormatText, matching CLI conventions where text is the default.
type Format int

// Output formats supported by Render.
const (
	// FormatText emits one diagnostic per line in the rg-like form:
	//   <path>:<line>:<col>: <severity>: <message> [<code>]
	// The trailing "[<code>]" is omitted when Code is empty.
	FormatText Format = iota

	// FormatJSON emits newline-delimited JSON (NDJSON): one object per
	// diagnostic, in the same sorted order as text output. Keys appear in
	// a stable order: path, line, column, end_line, end_column, severity,
	// message, code.
	FormatJSON
)

// Render writes the given diagnostics to w in the requested format.
//
// Render is deterministic: diagnostics are first sorted by (effective path,
// line, column) — the same ordering for both formats — and then emitted.
// The input slice is not mutated; sorting happens on a local copy.
//
// An empty or nil diags slice writes nothing and returns nil. An unrecognised
// format returns an error and writes nothing.
//
// All write errors from w are returned to the caller. Render does not buffer
// internally beyond what encoding/json needs; partial output is possible if
// the underlying writer fails mid-stream.
func Render(w io.Writer, diags []Diagnostic, format Format) error {
	if len(diags) == 0 {
		return nil
	}

	// Copy so we don't mutate caller-owned data.
	sorted := make([]Diagnostic, len(diags))
	copy(sorted, diags)
	sort.SliceStable(sorted, func(i, j int) bool {
		pi, pj := sorted[i].effectivePath(), sorted[j].effectivePath()
		if pi != pj {
			return pi < pj
		}
		if sorted[i].Span.Line != sorted[j].Span.Line {
			return sorted[i].Span.Line < sorted[j].Span.Line
		}
		return sorted[i].Span.Column < sorted[j].Span.Column
	})

	switch format {
	case FormatText:
		return renderText(w, sorted)
	case FormatJSON:
		return renderJSON(w, sorted)
	default:
		return fmt.Errorf("diag: unknown format %d", int(format))
	}
}

// renderText writes the rg-like one-line-per-diagnostic form.
func renderText(w io.Writer, diags []Diagnostic) error {
	for _, d := range diags {
		var line string
		if d.Code != "" {
			line = fmt.Sprintf("%s: %s: %s [%s]\n",
				d.formatPosition(), d.Severity.String(), d.Message, d.Code)
		} else {
			line = fmt.Sprintf("%s: %s: %s\n",
				d.formatPosition(), d.Severity.String(), d.Message)
		}
		if _, err := io.WriteString(w, line); err != nil {
			return err
		}
	}
	return nil
}

// jsonDiagnostic is the on-the-wire shape for FormatJSON. Field order here is
// the published key order — encoding/json marshals struct fields in source
// order. Keep this in sync with the package doc on Render and with the test
// in diag_test.go.
type jsonDiagnostic struct {
	Path      string `json:"path"`
	Line      int    `json:"line"`
	Column    int    `json:"column"`
	EndLine   int    `json:"end_line"`
	EndColumn int    `json:"end_column"`
	Severity  string `json:"severity"`
	Message   string `json:"message"`
	Code      string `json:"code"`
}

// renderJSON writes newline-delimited JSON (one object per diagnostic).
func renderJSON(w io.Writer, diags []Diagnostic) error {
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false) // diagnostics aren't HTML; keep "<" / ">" readable.
	for _, d := range diags {
		obj := jsonDiagnostic{
			Path:      d.effectivePath(),
			Line:      d.Span.Line,
			Column:    d.Span.Column,
			EndLine:   d.Span.EndLine,
			EndColumn: d.Span.EndCol,
			Severity:  d.Severity.String(),
			Message:   d.Message,
			Code:      d.Code,
		}
		// json.Encoder.Encode appends a newline after each value — exactly
		// what NDJSON wants.
		if err := enc.Encode(obj); err != nil {
			return err
		}
	}
	return nil
}
