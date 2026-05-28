// `gact list` walks .github/workflows, materialises each workflow file
// through internal/authoring.MaterialiseFull (so the listing reflects matrix
// expansion and topo verification, not the raw YAML), and renders a
// deterministic per-workflow tree:
//
//	<basename>: <workflow name>
//	  <jobID> (<runs-on>) [needs: <a>, <b>] [matrix]
//	    <jobID_n> (combo: k=v, k=v)
//	    - run: <verbatim>
//	    - uses: <owner>/<repo>@<ref>
//
// Design notes:
//
//   - Errors are non-fatal per file: a malformed workflow does NOT prevent
//     the rest from listing. We surface parse/schema diagnostics to stderr
//     via internal/diag.Render and exit non-zero only if any workflow
//     emitted a diagnostic.
//
//   - The "(no workflows found)" sentinel is printed to stderr so that
//     downstream consumers (CI grep, etc.) can pipe stdout cleanly without
//     having to filter the empty case.
//
//   - Matrix expansion turns one templated job into N children with IDs
//     "<orig>_0", "<orig>_1". After MaterialiseFull the original ID is gone
//     from JobsByID. We reconstruct the parent grouping by stripping the
//     numeric suffix (`<base>_<digits>`) — children that share a base form
//     a group. We render the synthetic parent line once, then each child
//     ordered by suffix index.

package cmd

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/staneswilson/gact/internal/authoring"
	"github.com/staneswilson/gact/internal/authoring/schema"
	"github.com/staneswilson/gact/internal/diag"
	wf "github.com/staneswilson/gact/pkg/workflow"
)

// listDir is the directory the command walks for `*.yml` / `*.yaml` files.
// It is bound to --dir and defaults to defaultWorkflowsDir (the GitHub
// convention) so the typical invocation needs no flags. Tests override this
// via --dir <tmp>/.github/workflows so they never depend on the developer's
// real checkout.
var listDir string

// defaultWorkflowsDir is the conventional GitHub Actions location. We
// expose it as a constant-style var so the RunE can fall back to it when
// the flag was not changed by the current invocation — important across
// repeated Execute calls in tests, because pflag does not reset
// variable-bound flags between invocations.
var defaultWorkflowsDir = filepath.Join(".github", "workflows")

var listCmd = &cobra.Command{
	Use:   "list",
	Short: "List discovered workflows, jobs, and matrix expansions",
	Long: "Walks the workflows directory, materialises each workflow file, and " +
		"renders a deterministic tree showing the workflow header, every job " +
		"with its runs-on and needs, and each matrix expansion. Parse and " +
		"schema diagnostics are printed to stderr; other workflows still list.",
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		// pflag does not restore variable-bound defaults between
		// successive Execute calls — once a test sets --dir, that
		// value (and the flag's Changed state) survives into the next
		// invocation. Snapshot the effective directory now, then
		// rewind both the bound variable and Changed so the NEXT
		// invocation behaves as if the flag was never touched.
		dir := listDir
		if !cmd.Flags().Changed("dir") {
			dir = defaultWorkflowsDir
		}
		if f := cmd.Flags().Lookup("dir"); f != nil {
			listDir = defaultWorkflowsDir
			f.Changed = false
		}
		return runList(cmd.OutOrStdout(), cmd.ErrOrStderr(), dir)
	},
}

func init() {
	listCmd.Flags().StringVar(
		&listDir,
		"dir",
		defaultWorkflowsDir,
		"directory to walk for workflow YAML files",
	)
	rootCmd.AddCommand(listCmd)
}

// runList is the testable workhorse: it does all the I/O against the supplied
// writers, returns an error only when at least one workflow file produced a
// diagnostic, and never panics on missing directories.
func runList(stdout, stderr io.Writer, dir string) error {
	paths, err := discoverWorkflows(dir)
	if err != nil {
		return fmt.Errorf("list: %w", err)
	}
	if len(paths) == 0 {
		// Per the design note above this is informational, not an error,
		// so it goes to stderr and the exit code stays zero.
		fmt.Fprintln(stderr, "(no workflows found)")
		return nil
	}

	var failed bool
	for _, path := range paths {
		if err := listOne(stdout, stderr, path); err != nil {
			failed = true
		}
	}
	if failed {
		// Returning a sentinel error makes ExecuteWith print "Error: ..."
		// and exit non-zero. The actual diagnostics have already been
		// rendered through diag.Render at the point of failure; this
		// message is just the summary.
		return fmt.Errorf("one or more workflows had errors")
	}
	return nil
}

// discoverWorkflows returns the .yml + .yaml files under dir, sorted by path
// so that subsequent rendering is deterministic regardless of filesystem
// iteration order. A missing dir yields an empty slice (the "no workflows"
// case the caller handles), not an error: a freshly-cloned repo without a
// workflows directory should not crash the listing.
func discoverWorkflows(dir string) ([]string, error) {
	yml, err := filepath.Glob(filepath.Join(dir, "*.yml"))
	if err != nil {
		return nil, err
	}
	yaml, err := filepath.Glob(filepath.Join(dir, "*.yaml"))
	if err != nil {
		return nil, err
	}
	all := append(yml, yaml...)
	sort.Strings(all)
	return all, nil
}

// listOne materialises a single workflow file and renders its tree to
// stdout. Diagnostics go to stderr via diag.Render. A returned non-nil error
// signals to the caller that the file contributed a failure to the overall
// exit code; the listing for the file is still emitted whenever the
// materialiser was able to produce a Workflow.
func listOne(stdout, stderr io.Writer, path string) error {
	src, err := os.ReadFile(path)
	if err != nil {
		// I/O failures (permissions, vanished file) become a synthetic
		// diagnostic so they ride the same channel as parse errors.
		emitError(stderr, path, err.Error())
		return err
	}

	w, schemaDiags, err := authoring.MaterialiseFull(path, src, authoring.MaterialiseInputs{})
	if err != nil {
		// Parse error or topo failure: there is no workflow to render.
		// Surface the message as a single error diagnostic so the output
		// shape matches the schema-error case.
		emitError(stderr, path, err.Error())
		return err
	}

	// Successful materialise — render the tree first so stdout is in a
	// stable, parseable shape even when stderr is interleaved.
	renderWorkflow(stdout, w)

	if len(schemaDiags) == 0 {
		return nil
	}
	// Schema diagnostics carry their own severity (error) and code.
	if rerr := diag.Render(stderr, schema.Diagnostics(schemaDiags), diag.FormatText); rerr != nil {
		return rerr
	}
	return fmt.Errorf("schema diagnostics in %s", path)
}

// emitError builds an ad-hoc diag.Diagnostic for non-schema failures (parse
// errors, I/O errors) so they share the same renderer and output shape as
// schema diagnostics. Span is left zero — the underlying parser error
// already carries position information in its Message in most cases.
func emitError(stderr io.Writer, path, msg string) {
	d := diag.Diagnostic{
		Path:     path,
		Severity: diag.SeverityError,
		Message:  msg,
	}
	// Render writes to stderr; if the writer itself fails we have no
	// recourse beyond letting the surrounding error propagate.
	_ = diag.Render(stderr, []diag.Diagnostic{d}, diag.FormatText)
}

// renderWorkflow writes the per-file tree. The shape is intentionally
// human-friendly first: two-space indents, the workflow header on its own
// line, then jobs grouped by their post-expansion lineage.
func renderWorkflow(out io.Writer, w wf.Workflow) {
	base := filepath.Base(w.Path)
	name := w.Name
	if name == "" {
		name = "(unnamed)"
	}
	fmt.Fprintf(out, "%s: %s\n", base, name)

	parents, childrenByParent, soloJobs := groupJobs(w.JobsByID)

	// Stable order: parent groups and solo jobs are merged into a single
	// ordered list keyed by the rendered ID label. Without this the order
	// would jitter between expansions and non-expansions.
	type entry struct {
		key        string
		isParent   bool
		parentID   string
		soloJob    wf.Job
		parentRep  wf.Job // representative child for the parent's runs-on
		childOrder []wf.JobID
	}
	entries := make([]entry, 0, len(parents)+len(soloJobs))
	for _, p := range parents {
		entries = append(entries, entry{
			key:        p,
			isParent:   true,
			parentID:   p,
			parentRep:  childrenByParent[p][0],
			childOrder: sortedChildIDs(childrenByParent[p]),
		})
	}
	for _, j := range soloJobs {
		entries = append(entries, entry{key: string(j.ID), isParent: false, soloJob: j})
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].key < entries[j].key })

	for _, e := range entries {
		if e.isParent {
			renderParentLine(out, e.parentID, e.parentRep)
			for _, id := range e.childOrder {
				child := w.JobsByID[id]
				fmt.Fprintf(out, "    %s\n", formatChildLine(child))
				renderSteps(out, child.Steps, 6)
			}
			continue
		}
		renderJobLine(out, e.soloJob)
		renderSteps(out, e.soloJob.Steps, 4)
	}
}

// groupJobs partitions JobsByID into matrix-expanded children (grouped
// under their original template ID, recovered by stripping the "_N" suffix)
// and "solo" jobs that never expanded. The split is the central trick that
// lets the listing show the parent's template ID even though the
// materialiser has deleted it from JobsByID.
func groupJobs(jobs map[wf.JobID]wf.Job) (parents []string, byParent map[string][]wf.Job, solo []wf.Job) {
	byParent = make(map[string][]wf.Job)
	for _, j := range jobs {
		base, _, ok := splitExpandedID(string(j.ID))
		if !ok {
			solo = append(solo, j)
			continue
		}
		byParent[base] = append(byParent[base], j)
	}
	for p := range byParent {
		parents = append(parents, p)
	}
	sort.Strings(parents)
	sort.Slice(solo, func(i, j int) bool { return solo[i].ID < solo[j].ID })
	return parents, byParent, solo
}

// splitExpandedID extracts the "<base>_<index>" form the materialiser uses
// for expanded jobs. The ok return is false when the ID does not end in
// `_<digits>` — those are solo (non-expanded) jobs. We deliberately use
// strconv.Atoi on the trailing segment rather than a regex so the
// classification is unambiguous: a job literally named "foo_0" without
// expansion would still be treated as expanded, which is acceptable because
// the materialiser itself produces such IDs and authoring such a name would
// already be a maintenance hazard.
func splitExpandedID(id string) (base string, idx int, ok bool) {
	i := strings.LastIndex(id, "_")
	if i < 0 || i == len(id)-1 {
		return "", 0, false
	}
	n, err := strconv.Atoi(id[i+1:])
	if err != nil {
		return "", 0, false
	}
	return id[:i], n, true
}

// sortedChildIDs orders expansion children by their numeric suffix so
// build_2 follows build_1 follows build_0 in output regardless of map
// iteration order. We fall back to lexical order if a child has no suffix
// — which should not happen given how we got here, but is cheap insurance.
func sortedChildIDs(children []wf.Job) []wf.JobID {
	ids := make([]wf.JobID, 0, len(children))
	for _, c := range children {
		ids = append(ids, c.ID)
	}
	sort.Slice(ids, func(i, j int) bool {
		_, ia, oki := splitExpandedID(string(ids[i]))
		_, ib, okj := splitExpandedID(string(ids[j]))
		if oki && okj {
			return ia < ib
		}
		return ids[i] < ids[j]
	})
	return ids
}

// renderParentLine writes the synthesised header for a matrix-expanded job.
// The parent template is no longer in JobsByID, so we lift runs-on from any
// child (they share it by construction) and append [matrix] as the marker
// that the children listed below are expansions, not authored jobs.
func renderParentLine(out io.Writer, parentID string, rep wf.Job) {
	parts := []string{fmt.Sprintf("%s (%s)", parentID, formatRunsOn(rep.RunsOn))}
	if needs := childNeedsForParent(rep); needs != "" {
		parts = append(parts, fmt.Sprintf("[needs: %s]", needs))
	}
	parts = append(parts, "[matrix]")
	fmt.Fprintf(out, "  %s\n", strings.Join(parts, " "))
}

// childNeedsForParent renders the needs list that ought to be attributed to
// the parent template. After remapNeeds every child depends on every
// producer expansion, so we collapse that fan-out back to the producer
// templates so the parent line stays compact. Anything that does not look
// like an expansion suffix is kept verbatim.
func childNeedsForParent(child wf.Job) string {
	if len(child.Needs) == 0 {
		return ""
	}
	seen := make(map[string]struct{})
	keys := make([]string, 0, len(child.Needs))
	for _, n := range child.Needs {
		base, _, ok := splitExpandedID(string(n))
		key := string(n)
		if ok {
			key = base
		}
		if _, dup := seen[key]; dup {
			continue
		}
		seen[key] = struct{}{}
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return strings.Join(keys, ", ")
}

// renderJobLine writes the one-line summary for a job that was not
// matrix-expanded. The fields appear in a fixed order: ID, runs-on, then
// optional needs.
func renderJobLine(out io.Writer, j wf.Job) {
	parts := []string{fmt.Sprintf("%s (%s)", j.ID, formatRunsOn(j.RunsOn))}
	if len(j.Needs) > 0 {
		needs := make([]string, len(j.Needs))
		for i, n := range j.Needs {
			needs[i] = string(n)
		}
		sort.Strings(needs)
		parts = append(parts, fmt.Sprintf("[needs: %s]", strings.Join(needs, ", ")))
	}
	fmt.Fprintf(out, "  %s\n", strings.Join(parts, " "))
}

// formatChildLine writes the per-expansion summary used under a parent's
// [matrix] line. The expanded job's ID encodes the combo index
// ("build_0", "build_1"), while its Name carries the human-readable combo
// descriptor ("build (combo: node=18)"). We render "<id> (combo: ...)" so
// that the listing remains greppable by ID while still showing the combo.
// When Name carries no combo (an empty matrix or a missing axis), we fall
// back to the bare ID.
func formatChildLine(j wf.Job) string {
	if idx := strings.Index(j.Name, "(combo:"); idx >= 0 {
		return fmt.Sprintf("%s %s", j.ID, strings.TrimSpace(j.Name[idx:]))
	}
	return string(j.ID)
}

// renderSteps prints each step in source order with `indent` leading spaces.
// We intentionally render only a one-line summary: full step details belong
// in `gact run --dry-run` (future). The point of `list` is structural,
// not literal.
func renderSteps(out io.Writer, steps []wf.Step, indent int) {
	pad := strings.Repeat(" ", indent)
	for _, s := range steps {
		switch s.Kind {
		case wf.StepKindUses:
			fmt.Fprintf(out, "%s- uses: %s\n", pad, formatUses(s.Uses))
		case wf.StepKindRun, wf.StepKindComposite:
			fallthrough
		default:
			fmt.Fprintf(out, "%s- run: %s\n", pad, oneLine(s.Run))
		}
	}
}

// formatUses renders a UsesRef in canonical "owner/repo[/path]@ref" form, or
// "./path" for local actions. Empty refs degrade to "<unknown>" so the line
// remains parseable even when the parser produced a partial value.
func formatUses(u wf.UsesRef) string {
	if u.Local {
		return "./" + u.Path
	}
	out := u.Owner + "/" + u.Repo
	if u.Path != "" {
		out += "/" + u.Path
	}
	if u.Ref != "" {
		out += "@" + u.Ref
	}
	return out
}

// formatRunsOn collapses a RunnerLabel to its display form. We prefer the
// joined Labels list because that survives normalisation; Raw is the
// fallback for cases where the parser kept the literal authored text (e.g.
// an expression).
func formatRunsOn(r wf.RunnerLabel) string {
	if len(r.Labels) > 0 {
		return strings.Join(r.Labels, ",")
	}
	return r.Raw
}

// oneLine strips embedded newlines from a run script so the listing stays
// one line per step. Multi-line scripts are rare in real workflows and
// inspecting them fully belongs in a separate "show" command rather than
// in the high-level tree.
func oneLine(s string) string {
	if !strings.ContainsAny(s, "\r\n") {
		return s
	}
	cleaned := strings.ReplaceAll(strings.ReplaceAll(s, "\r\n", " "), "\n", " ")
	return strings.Join(strings.Fields(cleaned), " ")
}
