// Job aggregate and its value objects.
//
// A Job belongs to a Workflow but is stored independently in
// Workflow.JobsByID; references to other jobs flow through Needs as JobIDs.
// This avoids pointer aliasing across aggregates and keeps matrix expansion
// (which fans out a single template into N concrete jobs) cheap.

package workflow

// Job is the IR for a single job within a workflow.
//
// If is the unresolved job-level conditional; Matrix, when non-nil, describes
// the matrix template that will fan out into multiple concrete jobs at
// scheduling time. The Span records the position of the `<jobid>:` key in the
// source YAML.
type Job struct {
	ID              JobID
	Name            string
	RunsOn          RunnerLabel
	Needs           []JobID
	If              Expression
	Matrix          *Matrix
	Steps           []Step
	Env             map[string]string
	Defaults        Defaults
	ContinueOnError bool
	TimeoutMinutes  int
	Outputs         map[string]Expression
	Span            SourceSpan
}

// RunnerLabel captures the `runs-on:` value, which may be a single label
// (`ubuntu-latest`), a YAML sequence (`[self-hosted, gpu]`), or a future
// expression. Raw preserves the original textual form for diagnostics and
// round-tripping; Labels is the normalised list of required labels.
//
// For a scalar string, Labels contains a single element. For a sequence,
// Labels contains each element in source order.
type RunnerLabel struct {
	Raw    string
	Labels []string
}

// Matrix captures a job's `strategy.matrix:` configuration as authored.
//
// Axes preserves declared axes in insertion order via map iteration semantics
// at the parser layer (callers that need ordering should record it elsewhere
// — pkg/workflow stays a plain map for value-object simplicity). Include and
// Exclude are slices of raw value bags exactly as authored. MaxParallel of 0
// means "unbounded" (matching GitHub's default). FailFast defaults to true on
// GitHub; here it is stored as authored, with the parser supplying the
// default if absent.
type Matrix struct {
	Include     []map[string]any
	Exclude     []map[string]any
	Axes        map[string][]any
	MaxParallel int
	FailFast    bool
}
