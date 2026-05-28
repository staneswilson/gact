// Step aggregate and its value objects.
//
// A Step is a single unit of work within a Job. It is either a `run:` step
// (shell command), a `uses:` step (action invocation), or a composite step
// (only emitted by the resolver when expanding composite actions). The
// Expression value object is opaque to consumers of pkg/workflow — it carries
// only the raw text and source span; internal/authoring/expr is responsible
// for tokenising and evaluating it.

package workflow

// StepKind discriminates between the three concrete forms a Step can take.
type StepKind int

const (
	// StepKindRun is a shell `run:` step.
	StepKindRun StepKind = iota
	// StepKindUses is an action invocation via `uses:`.
	StepKindUses
	// StepKindComposite is a step produced by expanding a composite action.
	// Authoring never emits this kind directly from YAML; the resolver
	// produces composite steps when flattening an action.yml.
	StepKindComposite
)

// String returns a stable lowercase label for the step kind, suitable for
// diagnostics, JSON output, and parity reports. Unknown values stringify as
// "unknown" rather than panicking so logs remain useful if the IR is fed an
// invalid kind.
func (k StepKind) String() string {
	switch k {
	case StepKindRun:
		return "run"
	case StepKindUses:
		return "uses"
	case StepKindComposite:
		return "composite"
	default:
		return "unknown"
	}
}

// Step is the IR for a single step inside a Job.
//
// Uses is meaningful only when Kind == StepKindUses; Run is meaningful only
// when Kind == StepKindRun. With and Env carry unevaluated expressions: the
// scheduler resolves them just before execution against a live ContextProvider.
type Step struct {
	ID              string
	Name            string
	Kind            StepKind
	Uses            UsesRef
	Run             string
	Shell           string
	WorkingDirectory string
	With            map[string]Expression
	Env             map[string]Expression
	If              Expression
	ContinueOnError bool
	TimeoutMinutes  int
	Span            SourceSpan
}

// UsesRef identifies an action referenced by a `uses:` step. Local marks the
// `./.github/actions/x` form; Owner/Repo/Path/Ref describe the
// `owner/repo[/path]@ref` form. Path is empty for top-level actions and set
// to the in-repo subdirectory for monorepo actions.
type UsesRef struct {
	Owner string
	Repo  string
	Path  string
	Ref   string
	Local bool
}

// Expression is the opaque holder for a `${{ ... }}` expression as captured
// from the source. pkg/workflow consumers see only the raw text and span; the
// tokens and evaluation logic live in internal/authoring/expr and are
// intentionally not exposed here so pkg/workflow can stay semver-stable.
type Expression struct {
	Raw  string
	Span SourceSpan
}
