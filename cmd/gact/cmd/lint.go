// gact lint — full static analysis of workflow files.
//
// This subcommand materialises every workflow under --dir, then surfaces:
//
//   - parse errors (YAML structural failures),
//   - schema diagnostics (no triggers, no steps, ambiguous step, …),
//   - DAG failures (cycles, unknown needs targets),
//   - expression parse errors (syntactically malformed `${{ … }}`),
//   - expression lookup errors for the *knowable* scopes
//     (typos in `github.*`, `runner.*`, `vars.*`).
//
// The "opaque" scopes (`secrets.*`, `steps.*`, `needs.*`, `env.*`) are
// intentionally NOT flagged: lint cannot know the user's real secret set
// or which step outputs the runtime will populate, so any reference to
// those scopes is presumed legitimate.
//
// Output is rendered via internal/diag in deterministic order
// (path → line → column). Any diagnostic causes a non-zero exit.

package cmd

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/staneswilson/gact/internal/authoring"
	"github.com/staneswilson/gact/internal/authoring/dag"
	"github.com/staneswilson/gact/internal/authoring/expr"
	"github.com/staneswilson/gact/internal/authoring/schema"
	"github.com/staneswilson/gact/internal/diag"
	pubexpr "github.com/staneswilson/gact/pkg/expr"
	wf "github.com/staneswilson/gact/pkg/workflow"
)

// Lint diagnostic codes. The "LINT-" prefix keeps them grep-able and disjoint
// from the SCHEMA-* codes the validator owns.
const (
	codeParseError      = "LINT-PARSE"
	codeCycle           = "LINT-CYCLE"
	codeUnknownNeeds    = "LINT-UNKNOWN-NEEDS"
	codeExprParse       = "LINT-EXPR-PARSE"
	codeExprLookup      = "LINT-EXPR-LOOKUP"
	codeExprPanic       = "LINT-EXPR-PANIC"
	codeExprEvalFailure = "LINT-EXPR-EVAL"
)

// lintDir is the --dir flag value. Default mirrors the GitHub Actions
// convention so `gact lint` from the repo root just works.
var lintDir string

var lintCmd = &cobra.Command{
	Use:   "lint",
	Short: "Statically analyse every workflow under --dir",
	Long: "lint materialises every .github/workflows/*.yml file under --dir " +
		"and reports any structural, DAG, or expression-level issue it finds. " +
		"Exit status is 0 when no diagnostics surface, 1 otherwise. Output goes " +
		"to stderr; stdout is left for the user's pipeline composition.",
	Args:         cobra.NoArgs,
	SilenceUsage: true,
	RunE: func(cmd *cobra.Command, _ []string) error {
		return runLint(cmd.OutOrStdout(), cmd.ErrOrStderr(), lintDir)
	},
}

func init() {
	lintCmd.Flags().StringVar(&lintDir, "dir", ".github/workflows",
		"directory containing workflow YAML files to lint")
	rootCmd.AddCommand(lintCmd)
}

// runLint is the testable seam under the cobra RunE. It walks dir for *.yml /
// *.yaml files, lints each, and renders a deterministic diagnostic stream to
// stderr. Returns a non-nil error when any diagnostic surfaced so cobra exits
// with a non-zero status.
func runLint(_ io.Writer, stderr io.Writer, dir string) error {
	files, err := workflowFiles(dir)
	if err != nil {
		return err
	}

	var diags []diag.Diagnostic
	for _, path := range files {
		diags = append(diags, lintOne(path)...)
	}

	if len(diags) == 0 {
		return nil
	}

	// Sort and render. Render also sorts internally, but doing it here too
	// makes the API contract clear and protects against future format
	// implementations that might not sort.
	sort.SliceStable(diags, func(i, j int) bool {
		a, b := diags[i], diags[j]
		if a.Path != b.Path {
			return a.Path < b.Path
		}
		if a.Span.Line != b.Span.Line {
			return a.Span.Line < b.Span.Line
		}
		return a.Span.Column < b.Span.Column
	})
	if err := diag.Render(stderr, diags, diag.FormatText); err != nil {
		return fmt.Errorf("render diagnostics: %w", err)
	}
	// Non-nil error tells cobra to exit non-zero. The Error: prefix added
	// by ExecuteWith would double-print, so we use a sentinel message that
	// is suppressed by SilenceErrors on the root.
	return errLintFailed
}

// errLintFailed is the sentinel returned when diagnostics surfaced. Its text
// is never printed because rootCmd has SilenceErrors=true; cobra still
// translates it to a non-zero exit via ExecuteWith.
var errLintFailed = fmt.Errorf("lint diagnostics emitted")

// workflowFiles returns the *.yml and *.yaml files in dir, sorted by
// lexical order so lint output is deterministic across operating systems.
// A non-existent dir returns an empty list with no error — the same as the
// directory being present but empty. The CLI is read-only and we don't want
// to fail loudly when a fresh repo has no workflows yet.
func workflowFiles(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read %s: %w", dir, err)
	}
	var paths []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if !strings.HasSuffix(name, ".yml") && !strings.HasSuffix(name, ".yaml") {
			continue
		}
		paths = append(paths, filepath.Join(dir, name))
	}
	sort.Strings(paths)
	return paths, nil
}

// lintOne returns all diagnostics for a single workflow file. It reads the
// file, hands it to MaterialiseFull, and then walks the resulting IR to
// statically evaluate every reachable expression.
func lintOne(path string) []diag.Diagnostic {
	src, err := os.ReadFile(path)
	if err != nil {
		return []diag.Diagnostic{{
			Path:     path,
			Severity: diag.SeverityError,
			Message:  fmt.Sprintf("read workflow: %v", err),
			Code:     codeParseError,
		}}
	}

	staticIn := defaultStaticInputs()
	w, schemaDiags, err := authoring.MaterialiseFull(path, src, authoring.MaterialiseInputs{
		StaticInputs: staticIn,
		// Strict: false — collect every diagnostic.
	})

	var diags []diag.Diagnostic
	// Schema diagnostics come back regardless of err, so surface them first.
	diags = append(diags, schema.Diagnostics(schemaDiags)...)

	// Distinguish typed materialiser errors so we can render them as
	// structured diagnostics rather than opaque text.
	if err != nil {
		diags = append(diags, materialiseErrorToDiags(path, err)...)
		// On a hard error there is no IR to walk for expressions, so stop
		// here. The schema diagnostics we already appended will still
		// surface.
		return diags
	}

	diags = append(diags, expressionDiagnostics(path, w, staticIn)...)
	return diags
}

// defaultStaticInputs returns the StaticInputs lint uses by default. The
// fields are chosen so that every "knowable" key the static context models
// returns a non-Null value — this lets us tell apart "user wrote an unknown
// key" (Null) from "user wrote a known key" (non-Null) when scanning for
// lookup failures.
//
// The values themselves are arbitrary placeholders, not real config:
//
//   - Event: "push" — a common, always-valid event name
//   - Ref: "refs/heads/main" — the canonical default-branch ref
//   - RunnerOS / RunnerArch: covers `runner.os` and `runner.arch`
//
// Secrets is intentionally nil: any reference to `secrets.X` returns Null
// from the static context, but we filter that scope out of the lookup
// check below so secrets references do not lint as errors.
func defaultStaticInputs() expr.StaticInputs {
	return expr.StaticInputs{
		Event:      "push",
		Ref:        "refs/heads/main",
		SHA:        "0000000000000000000000000000000000000000",
		Repository: "owner/repo",
		Actor:      "lint",
		RunnerOS:   "Linux",
		RunnerArch: "X64",
	}
}

// materialiseErrorToDiags converts a typed error from MaterialiseFull into
// structured diagnostics. The errors we recognise carry their own positional
// info (job IDs in the case of cycles, the missing target in the case of
// unknown needs); other errors are surfaced as a generic parse failure so the
// user at least gets the path and message.
func materialiseErrorToDiags(path string, err error) []diag.Diagnostic {
	var cycle *dag.CycleError
	if errors.As(err, &cycle) {
		// Render the cycle path as a single message; the job IDs in
		// CycleError.Path traverse the loop including the repeated start
		// node at the end, so they read as a closed loop already.
		parts := make([]string, 0, len(cycle.Path))
		for _, id := range cycle.Path {
			parts = append(parts, id.String())
		}
		return []diag.Diagnostic{{
			Path:     path,
			Severity: diag.SeverityError,
			Message:  "cycle in job graph: " + strings.Join(parts, " -> "),
			Code:     codeCycle,
		}}
	}
	var unknown *dag.UnknownNeedError
	if errors.As(err, &unknown) {
		return []diag.Diagnostic{{
			Path:     path,
			Severity: diag.SeverityError,
			Message: fmt.Sprintf("job %s needs unknown job %s",
				unknown.Job, unknown.Need),
			Code: codeUnknownNeeds,
		}}
	}
	return []diag.Diagnostic{{
		Path:     path,
		Severity: diag.SeverityError,
		Message:  err.Error(),
		Code:     codeParseError,
	}}
}

// expressionDiagnostics walks the materialised workflow IR and runs each
// reachable expression through the lexer, parser, and evaluator against a
// tracking static context. Three classes of issue are surfaced:
//
//  1. Expression parse errors — pubexpr.New returns an error for malformed
//     source. The diagnostic carries the expression's SourceSpan.
//
//  2. Expression evaluator failures — unknown function names, type errors,
//     etc. produce a non-nil error from Evaluate.
//
//  3. Lookups of knowable-scope identifiers that the static context does
//     not recognise. The tracking context wraps StaticContextFor and
//     records every (scope, key) pair queried; after Evaluate, any lookup
//     against github/runner/vars that returned KindNull is reported as a
//     typo or missing field.
//
// The walk covers Step.If, Step.With/.Env, Job.If, and Job.Outputs. Workflow-
// level expressions (e.g. WorkflowCallTrigger.Outputs.Value) are also
// included so reusable workflows lint cleanly.
func expressionDiagnostics(path string, w wf.Workflow, in expr.StaticInputs) []diag.Diagnostic {
	var out []diag.Diagnostic

	// Workflow-level call outputs.
	if w.Triggers.WorkflowCall != nil {
		for _, name := range sortedKeys(w.Triggers.WorkflowCall.Outputs) {
			co := w.Triggers.WorkflowCall.Outputs[name]
			out = append(out, evalSiteExpr(path, "workflow_call.outputs."+name, co.Value, in, true)...)
		}
	}

	// Walk jobs in lexical order so output is deterministic.
	ids := sortedJobIDs(w.JobsByID)
	for _, id := range ids {
		job := w.JobsByID[id]

		// Job.If — always treated as an expression.
		out = append(out, evalSiteExpr(path, "job "+string(id)+".if", job.If, in, true)...)

		// Job.Outputs — values are expressions per GH spec.
		for _, name := range sortedKeys(job.Outputs) {
			out = append(out, evalSiteExpr(path, "job "+string(id)+".outputs."+name, job.Outputs[name], in, true)...)
		}

		// Steps.
		for i, s := range job.Steps {
			label := fmt.Sprintf("job %s step #%d", id, i)
			if s.ID != "" {
				label = fmt.Sprintf("job %s step %q", id, s.ID)
			} else if s.Name != "" {
				label = fmt.Sprintf("job %s step %q", id, s.Name)
			}

			out = append(out, evalSiteExpr(path, label+".if", s.If, in, true)...)
			for _, k := range sortedKeys(s.With) {
				out = append(out, evalSiteExpr(path, label+".with."+k, s.With[k], in, false)...)
			}
			for _, k := range sortedKeys(s.Env) {
				out = append(out, evalSiteExpr(path, label+".env."+k, s.Env[k], in, false)...)
			}
		}
	}
	return out
}

// sortedKeys is a helper that returns the keys of an arbitrary string-keyed
// map sorted lexically. Used to ensure expression-site iteration is
// deterministic — Go's map iteration order is randomised, and the rendered
// diagnostics are sorted by source position, so we additionally pre-sort
// here to keep the *call* order stable in case two values share a position.
func sortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// sortedJobIDs returns the keys of m lexically sorted. Mirrors the helper in
// internal/authoring/materialise.go; duplicated here so the lint command does
// not have to reach into a sibling package's internals.
func sortedJobIDs(m map[wf.JobID]wf.Job) []wf.JobID {
	out := make([]wf.JobID, 0, len(m))
	for id := range m {
		out = append(out, id)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

// evalSiteExpr lints a single expression value at one site. The boolean
// "implicit" reports whether the site (e.g. `if:`) treats its scalar as an
// expression unconditionally. When implicit is false (e.g. `with:` map
// values), only Raw strings that contain the `${{` template delimiter are
// considered expressions — plain literals like `node-version: "18"` are
// skipped to avoid false positives.
//
// The function does NOT panic on malformed input: the underlying evaluator
// is wrapped in a deferred recover so unexpected expression-walker bugs
// degrade to a structured diagnostic rather than crashing the linter.
func evalSiteExpr(path, label string, e wf.Expression, in expr.StaticInputs, implicit bool) (diags []diag.Diagnostic) {
	raw := strings.TrimSpace(e.Raw)
	if raw == "" {
		return nil
	}
	if !implicit {
		// Non-implicit sites (e.g. `with:`/`env:` values) only carry an
		// expression when the scalar is the strict envelope `${{ ... }}`.
		// Concatenated forms like "v${{ tag }}" are template literals at
		// runtime, not expressions — we deliberately skip them rather than
		// flagging the surrounding text as an expression parse error.
		if !(strings.HasPrefix(raw, "${{") && strings.HasSuffix(raw, "}}")) {
			return nil
		}
	}

	body := stripTemplateEnvelope(raw)
	if body == "" {
		return nil
	}

	defer func() {
		if r := recover(); r != nil {
			diags = append(diags, diag.Diagnostic{
				Path:     path,
				Span:     e.Span,
				Severity: diag.SeverityError,
				Message:  fmt.Sprintf("%s: expression evaluator panicked: %v", label, r),
				Code:     codeExprPanic,
			})
		}
	}()

	ev, err := pubexpr.New(body)
	if err != nil {
		diags = append(diags, diag.Diagnostic{
			Path:     path,
			Span:     e.Span,
			Severity: diag.SeverityError,
			Message:  fmt.Sprintf("%s: expression parse error: %v", label, err),
			Code:     codeExprParse,
		})
		return diags
	}

	tracker := &lookupTracker{inner: expr.StaticContextFor(in)}
	if _, evalErr := ev.Evaluate(tracker); evalErr != nil {
		diags = append(diags, diag.Diagnostic{
			Path:     path,
			Span:     e.Span,
			Severity: diag.SeverityError,
			Message:  fmt.Sprintf("%s: expression evaluation failed: %v", label, evalErr),
			Code:     codeExprEvalFailure,
		})
		// Continue to surface lookup misses too, in case the failure is
		// transient (e.g. a non-fatal func arity issue) and we can still
		// be helpful.
	}

	for _, miss := range tracker.knowableMisses() {
		diags = append(diags, diag.Diagnostic{
			Path:     path,
			Span:     e.Span,
			Severity: diag.SeverityError,
			Message: fmt.Sprintf("%s: reference to unknown %s.%s (typo or unmodelled field)",
				label, miss.scope, miss.key),
			Code: codeExprLookup,
		})
	}
	return diags
}

// stripTemplateEnvelope removes a leading `${{` and trailing `}}` from a
// scalar that was authored in template form (the form `${{ … }}` used in
// `with:`/`env:` values). For `if:` scalars, which are implicitly expression
// scope, the function is a no-op because no envelope is present. Whitespace
// inside the envelope is trimmed for the evaluator's benefit.
//
// Multi-template strings such as `prefix-${{ a }}-${{ b }}-suffix` are
// intentionally NOT split here: the evaluator would reject such input as a
// parse error, which is the right outcome — concatenated template scalars
// are a runtime concern and the linter does not pretend to render them.
func stripTemplateEnvelope(raw string) string {
	s := strings.TrimSpace(raw)
	if strings.HasPrefix(s, "${{") && strings.HasSuffix(s, "}}") {
		return strings.TrimSpace(s[3 : len(s)-2])
	}
	return s
}

// lookupRecord is one (scope, key) tuple observed by the tracker. Lookups
// against the same pair multiple times collapse into a single record so the
// caller does not see a flood of duplicate diagnostics on common idioms.
type lookupRecord struct {
	scope string
	key   string
}

// lookupTracker wraps a pubexpr.Context and records every (scope, key)
// queried. After evaluation, knowableMisses returns the subset of records
// whose lookups returned KindNull *and* whose scope is one we can authoritatively
// validate (github, runner, vars).
//
// Tracking lets lint surface "github.bogus" as an unknown field while leaving
// `secrets.X`, `steps.Y.outputs.Z`, and `needs.A.result` alone because lint
// cannot know the user's real secret set or which runtime state will exist.
type lookupTracker struct {
	inner pubexpr.Context
	// hits indexes (scope, key) tuples to their last-known result Kind.
	// Using a map prevents duplicate records when the same lookup happens
	// inside a loop or function call.
	hits map[lookupRecord]pubexpr.Kind
}

// Get forwards to the inner context and records the result. Errors from the
// inner context bubble up unchanged — the lint command's error path will
// convert them into a structured diagnostic, but we don't want to swallow
// them here.
func (t *lookupTracker) Get(scope, key string) (pubexpr.Value, error) {
	v, err := t.inner.Get(scope, key)
	if t.hits == nil {
		t.hits = make(map[lookupRecord]pubexpr.Kind)
	}
	t.hits[lookupRecord{scope: scope, key: key}] = v.Kind
	return v, err
}

// knowableMisses returns the set of (scope, key) tuples that:
//
//   - were queried against a scope whose key set we know authoritatively
//     (currently github, runner, vars), AND
//   - resolved to KindNull.
//
// Results are sorted by (scope, key) so diagnostics emit deterministically.
//
// The scope filter is deliberately conservative: opaque scopes (secrets,
// steps, needs, env) are excluded because lint cannot tell a typo from a
// legitimate reference to runtime state. If a future task wires lint to a
// secrets allowlist (e.g. via .gact.yml), the secrets scope can be promoted
// to "knowable" by adding it to the switch in isKnowableScope below.
func (t *lookupTracker) knowableMisses() []lookupRecord {
	var out []lookupRecord
	for rec, kind := range t.hits {
		if !isKnowableScope(rec.scope) {
			continue
		}
		if kind != pubexpr.KindNull {
			continue
		}
		out = append(out, rec)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].scope != out[j].scope {
			return out[i].scope < out[j].scope
		}
		return out[i].key < out[j].key
	})
	return out
}

// isKnowableScope reports whether the static context can authoritatively
// determine the legitimacy of a key under this scope. Scopes excluded here
// are opaque to lint by design (see lookupTracker doc-comment).
//
// NOTE: vars is treated as opaque-by-default because lint does not currently
// have access to the user's .gact.yml vars map. Once that wiring exists
// (planned for the selection task), vars.X can be promoted by switching the
// case below to return true.
func isKnowableScope(scope string) bool {
	switch scope {
	case "github", "runner":
		return true
	}
	return false
}
