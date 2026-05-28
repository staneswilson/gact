// Package diag_test contains tests for the diagnostics framework.
//
// The diag package is intentionally small: a Diagnostic value object plus a
// Render function. The tests pin down the contract that downstream contexts
// (parser, schema validator, lint, LSP, CLI) will rely on:
//   - zero-value Diagnostic renders without panicking
//   - Severity stringifies stably
//   - rendering is deterministic and sorted
//   - JSON output is newline-delimited with stable key order and proper escaping.
package diag_test

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/staneswilson/gact/internal/diag"
	wf "github.com/staneswilson/gact/pkg/workflow"
)

// TestDiagnostic_ZeroValueRendersCleanly asserts that a zero-value Diagnostic
// can be rendered in both formats without panic and produces output that is
// at least non-empty and newline-terminated.
func TestDiagnostic_ZeroValueRendersCleanly(t *testing.T) {
	var d diag.Diagnostic
	diags := []diag.Diagnostic{d}

	t.Run("text", func(t *testing.T) {
		var buf bytes.Buffer
		if err := diag.Render(&buf, diags, diag.FormatText); err != nil {
			t.Fatalf("Render(text) returned error: %v", err)
		}
		out := buf.String()
		if out == "" {
			t.Fatalf("expected non-empty text output for zero-value diagnostic")
		}
		if !strings.HasSuffix(out, "\n") {
			t.Fatalf("expected trailing newline, got %q", out)
		}
	})

	t.Run("json", func(t *testing.T) {
		var buf bytes.Buffer
		if err := diag.Render(&buf, diags, diag.FormatJSON); err != nil {
			t.Fatalf("Render(json) returned error: %v", err)
		}
		out := buf.String()
		if out == "" {
			t.Fatalf("expected non-empty json output for zero-value diagnostic")
		}
		if !strings.HasSuffix(out, "\n") {
			t.Fatalf("expected trailing newline, got %q", out)
		}
		// Must be valid JSON.
		var obj map[string]any
		if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &obj); err != nil {
			t.Fatalf("json output is not valid JSON: %v\noutput=%q", err, out)
		}
	})
}

// TestSeverity_String asserts the stable lowercase mapping used in diagnostics.
func TestSeverity_String(t *testing.T) {
	cases := []struct {
		name string
		in   diag.Severity
		want string
	}{
		{"error", diag.SeverityError, "error"},
		{"warning", diag.SeverityWarning, "warning"},
		{"info", diag.SeverityInfo, "info"},
		{"unknown", diag.Severity(99), "unknown"},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.in.String(); got != tc.want {
				t.Fatalf("Severity.String() = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestRender_TextFormat asserts the rg-like one-line format for each severity.
//
// Format: <path>:<line>:<col>: <severity>: <message> [<code>]
// When Path is empty, Span.Path is used as the fallback.
func TestRender_TextFormat(t *testing.T) {
	cases := []struct {
		name string
		in   diag.Diagnostic
		want string
	}{
		{
			name: "error with explicit path",
			in: diag.Diagnostic{
				Path:     "ci.yml",
				Span:     wf.SourceSpan{Path: "ci.yml", Line: 12, Column: 4},
				Severity: diag.SeverityError,
				Message:  "unknown key 'jorbs'",
				Code:     "GACT001",
			},
			want: "ci.yml:12:4: error: unknown key 'jorbs' [GACT001]\n",
		},
		{
			name: "warning falls back to span path when Path is empty",
			in: diag.Diagnostic{
				Span:     wf.SourceSpan{Path: "wf.yml", Line: 3, Column: 1},
				Severity: diag.SeverityWarning,
				Message:  "deprecated key",
				Code:     "GACT020",
			},
			want: "wf.yml:3:1: warning: deprecated key [GACT020]\n",
		},
		{
			name: "info with no code omits brackets",
			in: diag.Diagnostic{
				Path:     "release.yml",
				Span:     wf.SourceSpan{Path: "release.yml", Line: 1, Column: 1},
				Severity: diag.SeverityInfo,
				Message:  "consider pinning by SHA",
			},
			want: "release.yml:1:1: info: consider pinning by SHA\n",
		},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			var buf bytes.Buffer
			if err := diag.Render(&buf, []diag.Diagnostic{tc.in}, diag.FormatText); err != nil {
				t.Fatalf("Render returned error: %v", err)
			}
			if got := buf.String(); got != tc.want {
				t.Fatalf("text render =\n%q\nwant\n%q", got, tc.want)
			}
		})
	}
}

// TestRender_SortedByPathLineColumn pins down that Render sorts diagnostics
// deterministically by (Path, Line, Column) regardless of input order. This is
// what makes diff-able diagnostic output across tools possible.
func TestRender_SortedByPathLineColumn(t *testing.T) {
	in := []diag.Diagnostic{
		{Path: "b.yml", Span: wf.SourceSpan{Path: "b.yml", Line: 5, Column: 1}, Severity: diag.SeverityError, Message: "third", Code: "C3"},
		{Path: "a.yml", Span: wf.SourceSpan{Path: "a.yml", Line: 10, Column: 1}, Severity: diag.SeverityError, Message: "second", Code: "C2"},
		{Path: "a.yml", Span: wf.SourceSpan{Path: "a.yml", Line: 2, Column: 8}, Severity: diag.SeverityError, Message: "first-col-after", Code: "C1B"},
		{Path: "a.yml", Span: wf.SourceSpan{Path: "a.yml", Line: 2, Column: 1}, Severity: diag.SeverityError, Message: "first", Code: "C1"},
	}
	var buf bytes.Buffer
	if err := diag.Render(&buf, in, diag.FormatText); err != nil {
		t.Fatalf("Render returned error: %v", err)
	}
	got := buf.String()
	want := "a.yml:2:1: error: first [C1]\n" +
		"a.yml:2:8: error: first-col-after [C1B]\n" +
		"a.yml:10:1: error: second [C2]\n" +
		"b.yml:5:1: error: third [C3]\n"
	if got != want {
		t.Fatalf("sorted text render =\n%s\nwant\n%s", got, want)
	}
}

// TestRender_SortStableForEqualKeys asserts that diagnostics with identical
// (Path, Line, Column) preserve input order — i.e. the sort is stable. This
// matters when multiple checks fire on the same token.
func TestRender_SortStableForEqualKeys(t *testing.T) {
	in := []diag.Diagnostic{
		{Path: "a.yml", Span: wf.SourceSpan{Path: "a.yml", Line: 1, Column: 1}, Severity: diag.SeverityError, Message: "alpha", Code: "A"},
		{Path: "a.yml", Span: wf.SourceSpan{Path: "a.yml", Line: 1, Column: 1}, Severity: diag.SeverityError, Message: "beta", Code: "B"},
		{Path: "a.yml", Span: wf.SourceSpan{Path: "a.yml", Line: 1, Column: 1}, Severity: diag.SeverityError, Message: "gamma", Code: "G"},
	}
	var buf bytes.Buffer
	if err := diag.Render(&buf, in, diag.FormatText); err != nil {
		t.Fatalf("Render returned error: %v", err)
	}
	got := buf.String()
	want := "a.yml:1:1: error: alpha [A]\n" +
		"a.yml:1:1: error: beta [B]\n" +
		"a.yml:1:1: error: gamma [G]\n"
	if got != want {
		t.Fatalf("stable sort render =\n%s\nwant\n%s", got, want)
	}
}

// TestRender_JSONStructure asserts the JSON output is newline-delimited with
// one object per diagnostic, stable key order, and proper escaping of
// special characters in messages (quotes, backslashes, newlines, control
// characters).
func TestRender_JSONStructure(t *testing.T) {
	in := []diag.Diagnostic{
		{
			Path:     "ci.yml",
			Span:     wf.SourceSpan{Path: "ci.yml", Line: 2, Column: 5, EndLine: 2, EndCol: 12},
			Severity: diag.SeverityError,
			Message:  "bad \"quote\" and \\backslash and \nnewline",
			Code:     "GACT001",
		},
		{
			Path:     "ci.yml",
			Span:     wf.SourceSpan{Path: "ci.yml", Line: 1, Column: 1},
			Severity: diag.SeverityWarning,
			Message:  "warn first",
			Code:     "GACT020",
		},
	}
	var buf bytes.Buffer
	if err := diag.Render(&buf, in, diag.FormatJSON); err != nil {
		t.Fatalf("Render returned error: %v", err)
	}
	out := buf.String()
	if !strings.HasSuffix(out, "\n") {
		t.Fatalf("expected trailing newline, got %q", out)
	}
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	if len(lines) != 2 {
		t.Fatalf("expected 2 JSON lines, got %d:\n%s", len(lines), out)
	}

	// Each line must be valid JSON.
	for i, line := range lines {
		var obj map[string]any
		if err := json.Unmarshal([]byte(line), &obj); err != nil {
			t.Fatalf("line %d is not valid JSON: %v\nline=%q", i, err, line)
		}
	}

	// Sorted: warning (line 1) before error (line 2).
	if !strings.Contains(lines[0], `"warn first"`) {
		t.Fatalf("expected warning to sort first, got %q", lines[0])
	}
	if !strings.Contains(lines[1], `"GACT001"`) {
		t.Fatalf("expected error to sort second, got %q", lines[1])
	}

	// Stable key order: path, line, column, end_line, end_column, severity,
	// message, code. We check by substring positions on the first emitted
	// (the warning) line, which has all keys populated.
	first := lines[1] // the error line has every field populated, including end_line.
	wantKeys := []string{`"path"`, `"line"`, `"column"`, `"end_line"`, `"end_column"`, `"severity"`, `"message"`, `"code"`}
	prev := -1
	for _, key := range wantKeys {
		idx := strings.Index(first, key)
		if idx == -1 {
			t.Fatalf("expected key %s in JSON output, got %q", key, first)
		}
		if idx <= prev {
			t.Fatalf("key %s appeared out of order in JSON output: %q", key, first)
		}
		prev = idx
	}

	// Escaping: the error message must round-trip exactly.
	var decoded struct {
		Message string `json:"message"`
	}
	if err := json.Unmarshal([]byte(lines[1]), &decoded); err != nil {
		t.Fatalf("decoding error line failed: %v", err)
	}
	wantMsg := "bad \"quote\" and \\backslash and \nnewline"
	if decoded.Message != wantMsg {
		t.Fatalf("round-tripped message = %q, want %q", decoded.Message, wantMsg)
	}
}

// TestRender_JSONUsesSpanPathFallback asserts that when Diagnostic.Path is
// empty, the JSON output's "path" field falls back to Span.Path. This mirrors
// the text renderer's behaviour and the documented invariant on Diagnostic.
func TestRender_JSONUsesSpanPathFallback(t *testing.T) {
	d := diag.Diagnostic{
		Span:     wf.SourceSpan{Path: "fallback.yml", Line: 3, Column: 1},
		Severity: diag.SeverityInfo,
		Message:  "hi",
		Code:     "GACT100",
	}
	var buf bytes.Buffer
	if err := diag.Render(&buf, []diag.Diagnostic{d}, diag.FormatJSON); err != nil {
		t.Fatalf("Render returned error: %v", err)
	}
	var decoded struct {
		Path string `json:"path"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(buf.String())), &decoded); err != nil {
		t.Fatalf("decoding failed: %v", err)
	}
	if decoded.Path != "fallback.yml" {
		t.Fatalf("path fallback = %q, want %q", decoded.Path, "fallback.yml")
	}
}

// TestRender_EmptyInput asserts that rendering an empty slice produces no
// output and no error. This makes Render safe to call unconditionally in
// callers like the CLI.
func TestRender_EmptyInput(t *testing.T) {
	for _, format := range []diag.Format{diag.FormatText, diag.FormatJSON} {
		var buf bytes.Buffer
		if err := diag.Render(&buf, nil, format); err != nil {
			t.Fatalf("Render(nil, %v) returned error: %v", format, err)
		}
		if buf.Len() != 0 {
			t.Fatalf("Render(nil, %v) wrote %q, want empty", format, buf.String())
		}
	}
}

// TestRender_UnknownFormatReturnsError asserts that an unrecognised Format
// value is a programmer error reported as an error return rather than a
// silent fallback. This prevents the LSP / CLI from emitting malformed output.
func TestRender_UnknownFormatReturnsError(t *testing.T) {
	var buf bytes.Buffer
	err := diag.Render(&buf, []diag.Diagnostic{{}}, diag.Format(99))
	if err == nil {
		t.Fatalf("expected error for unknown format, got nil")
	}
}

// TestRender_DoesNotMutateInput asserts the renderer treats its input as
// read-only. We pass a slice whose order would change under sorting and
// confirm the original slice is unchanged.
func TestRender_DoesNotMutateInput(t *testing.T) {
	in := []diag.Diagnostic{
		{Path: "b.yml", Span: wf.SourceSpan{Path: "b.yml", Line: 1, Column: 1}, Severity: diag.SeverityError, Message: "b", Code: "B"},
		{Path: "a.yml", Span: wf.SourceSpan{Path: "a.yml", Line: 1, Column: 1}, Severity: diag.SeverityError, Message: "a", Code: "A"},
	}
	var buf bytes.Buffer
	if err := diag.Render(&buf, in, diag.FormatText); err != nil {
		t.Fatalf("Render returned error: %v", err)
	}
	if in[0].Path != "b.yml" || in[1].Path != "a.yml" {
		t.Fatalf("Render mutated its input slice: %+v", in)
	}
}
