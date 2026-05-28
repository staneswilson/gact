// Package classifier categorises textual diffs between a local `gact run`
// (or `gact lint`) output line and the corresponding line captured from a
// real GitHub Actions run via `gh run view --log`. It is the Spike B
// prototype called out in plan §7.10 / Task 0.20.
//
// This file currently holds the public surface only; rule implementations
// land in the follow-up commit that turns the failing fixture-driven
// tests green. Keeping the API stable up-front lets the test file
// reference the same names from day one.
package classifier

// Category is the classifier verdict. Ordering matches block > warn >
// noise so callers that need a "worst diff in a hunk" reduction can
// simply max() over a slice of Result.Category values.
type Category int

const (
	// CategoryNoise is for diffs that are demonstrably non-semantic.
	// They are stripped from parity reports and never escalate.
	CategoryNoise Category = iota
	// CategoryWarn is for diffs whose semantic significance is unclear.
	// They surface as PR comments but do not block a merge or open an
	// issue. This is also the default when no rule claims a diff.
	CategoryWarn
	// CategoryBlock is for diffs that materially change the observable
	// outcome of a workflow run. They open issues automatically.
	CategoryBlock
)

// String renders a Category as a lowercase token suitable for fixture
// comparisons and structured-log fields. Unknown categories render as
// "unknown" so a forgotten constant surfaces obviously rather than
// masquerading as a known value.
func (c Category) String() string {
	switch c {
	case CategoryNoise:
		return "noise"
	case CategoryWarn:
		return "warn"
	case CategoryBlock:
		return "block"
	default:
		return "unknown"
	}
}

// Diff is a single observation: one line (or short hunk) from the local
// run and the corresponding line from the GitHub run. The trailing
// fields are optional and let block-detecting rules see step-level
// signal that line-by-line text would otherwise miss.
type Diff struct {
	Local  string
	Remote string

	ExitCodeLocal  int
	ExitCodeRemote int

	StepSkippedLocal  bool
	StepSkippedRemote bool

	JobVerdictLocal  string
	JobVerdictRemote string
}

// Result is the classifier's verdict for a single Diff. Reason is a
// short token (kebab-case) identifying the rule that fired, suitable
// for structured logging and for fixture assertions. Where a category
// is set by the fallback rather than a named rule, Reason is the
// special token "default-warn".
type Result struct {
	Category Category
	Reason   string
}

// Rule is the contract every classifier rule satisfies.
type Rule struct {
	// Name is a short kebab-case identifier used in logs and reasons.
	Name string
	// Category is the verdict the rule emits when it claims a Diff.
	Category Category
	// Match returns true iff this rule claims the diff.
	Match func(d Diff) bool
}

// Rules is the ordered list of classifier rules. Empty in this stub;
// rule definitions land alongside their matching fixtures in follow-up
// commits.
var Rules = []Rule{}

// Classify walks the Rules slice in order and returns the first rule
// that claims the diff. With an empty rule slice, every Diff falls
// through to default-warn.
func Classify(d Diff) Result {
	for _, r := range Rules {
		if r.Match(d) {
			return Result{Category: r.Category, Reason: r.Name}
		}
	}
	return Result{Category: CategoryWarn, Reason: "default-warn"}
}
