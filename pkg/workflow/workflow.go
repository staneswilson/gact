// Workflow aggregate and its trigger value objects.
//
// A Workflow is the top-level IR root for a single .github/workflows/*.yml
// file. Cross-aggregate references use JobID rather than embedding the Job
// struct itself; this keeps each aggregate small and lets matrix expansion
// produce independent Job copies without cloning a large tree.

package workflow

// JobID identifies a Job inside a Workflow. It is a typedef rather than a bare
// string so APIs that take or return job identifiers are self-documenting.
type JobID string

// String returns the underlying identifier. It exists so JobID satisfies
// fmt.Stringer and reads naturally in diagnostics.
func (j JobID) String() string { return string(j) }

// Workflow is the IR for a single GitHub Actions workflow file.
//
// Triggers, Env, and Defaults capture the top-level configuration. JobsByID
// holds the workflow's jobs keyed by their ID so callers can traverse the
// `needs:` graph without quadratic scans. Span records where the workflow
// node itself begins in the source YAML.
type Workflow struct {
	Name     string
	Path     string
	Triggers Triggers
	Env      map[string]string
	Defaults Defaults
	JobsByID map[JobID]Job
	Span     SourceSpan
}

// Defaults captures the workflow- or job-level `defaults:` block. The fields
// mirror the GitHub Actions schema; new fields can be added without breaking
// existing callers because all keys are optional.
type Defaults struct {
	Run RunDefaults
}

// RunDefaults captures `defaults.run.shell` and `defaults.run.working-directory`.
type RunDefaults struct {
	Shell            string
	WorkingDirectory string
}

// Triggers captures the workflow's `on:` block. Each event type is a separate
// pointer (or slice, for events that legitimately repeat such as `schedule`)
// so that callers can distinguish "absent" from "present but empty".
//
// For example, `on: push` and `on: { push: { branches: [main] } }` both yield
// a non-nil Push pointer, while a workflow that does not list `push:` at all
// leaves Push nil.
type Triggers struct {
	Push             *PushTrigger
	PullRequest      *PullRequestTrigger
	PullRequestTarget *PullRequestTrigger
	Schedule         []ScheduleTrigger
	WorkflowDispatch *WorkflowDispatchTrigger
	WorkflowCall     *WorkflowCallTrigger
	WorkflowRun      *WorkflowRunTrigger
	// Other       captures uncommon or future event names that have not been
	// modelled as dedicated structs yet. Each entry is the event name as it
	// appeared in YAML. Keeping a slice of names (rather than a map of opaque
	// config) lets selection logic recognise an event without forcing this
	// package to track every GitHub event payload.
	Other []string
}

// PushTrigger filters for `push:` events.
type PushTrigger struct {
	Branches       []string
	BranchesIgnore []string
	Tags           []string
	TagsIgnore     []string
	Paths          []string
	PathsIgnore    []string
}

// PullRequestTrigger filters for `pull_request:` (and `pull_request_target:`)
// events.
type PullRequestTrigger struct {
	Types          []string
	Branches       []string
	BranchesIgnore []string
	Paths          []string
	PathsIgnore    []string
}

// ScheduleTrigger captures a single cron entry from `on.schedule`. A workflow
// can have several, hence Triggers.Schedule is a slice.
type ScheduleTrigger struct {
	Cron string
}

// WorkflowDispatchTrigger captures a `workflow_dispatch:` block. Inputs are
// keyed by their declared name.
type WorkflowDispatchTrigger struct {
	Inputs map[string]DispatchInput
}

// DispatchInput describes a single declared input for `workflow_dispatch`.
type DispatchInput struct {
	Description string
	Required    bool
	Default     string
	Type        string
	Options     []string
}

// WorkflowCallTrigger captures a `workflow_call:` block — used when this
// workflow is invoked by another via `uses:`.
type WorkflowCallTrigger struct {
	Inputs  map[string]CallInput
	Secrets map[string]CallSecret
	Outputs map[string]CallOutput
}

// CallInput describes an input declared by a reusable workflow.
type CallInput struct {
	Description string
	Required    bool
	Default     string
	Type        string
}

// CallSecret describes a secret declared by a reusable workflow.
type CallSecret struct {
	Description string
	Required    bool
}

// CallOutput describes an output declared by a reusable workflow. Value holds
// the unevaluated expression — it remains opaque to pkg/workflow consumers.
type CallOutput struct {
	Description string
	Value       Expression
}

// WorkflowRunTrigger captures a `workflow_run:` block.
type WorkflowRunTrigger struct {
	Workflows []string
	Types     []string
	Branches  []string
}
