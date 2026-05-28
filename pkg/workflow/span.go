// Package workflow defines the public, semver-stable IR (intermediate
// representation) for GitHub Actions workflows.
//
// Everything in this package is a value object: zero values are valid, no
// pointers are kept between aggregates, and cross-aggregate references use
// IDs (see JobID). The three aggregates are Workflow, Job, and Step.
//
// pkg/workflow may not import any internal/ package. Consumers may build,
// inspect, and serialise these types without taking on a dependency on the
// parser, evaluator, or executor.
package workflow

import "strconv"

// SourceSpan locates a node in its source file. It is a value object: copies
// are independent and the zero value (empty path, all zeros) is well-defined.
//
// Line and Column are 1-based to match the underlying YAML library and
// GitHub's own diagnostics. EndLine and EndCol describe the inclusive end of
// the span; when a span covers a single token they may equal Line/Column.
type SourceSpan struct {
	Path    string
	Line    int
	Column  int
	EndLine int
	EndCol  int
}

// String renders the span in the conventional "path:line:col" form used by
// compilers and linters. The end of the span is intentionally omitted to keep
// diagnostic output compact; callers that need the full range can read the
// fields directly.
func (s SourceSpan) String() string {
	return s.Path + ":" + strconv.Itoa(s.Line) + ":" + strconv.Itoa(s.Column)
}
