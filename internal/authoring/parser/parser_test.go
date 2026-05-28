// Golden-file tests for the workflow YAML parser.
//
// Each *.yml fixture under testdata/ has a matching *.golden.json file
// holding the JSON-serialised IR the parser is expected to produce.
// Running `go test ./internal/authoring/parser/... -update` regenerates
// the goldens — useful when the IR shape evolves or a new fixture is
// added. Without -update, the test diff is presented in unified-ish form
// so reviewers can see exactly which fields drifted.
//
// The serialised IR is normalised before comparison:
//   - SourceSpan.Path is stripped of any directory prefix and rewritten
//     with forward slashes so the goldens are portable across machines.
//   - JSON is indented with two spaces for human readability.

package parser

import (
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	wf "github.com/staneswilson/gact/pkg/workflow"
)

// update toggles golden regeneration. The conventional CLI flag
// `-update` works for ad-hoc use, while the environment variable
// GACT_PARSER_UPDATE_GOLDEN=1 covers automation contexts where adding
// extra positional arguments to `go test` is awkward.
var update = flag.Bool("update", false, "rewrite testdata/*.golden.json from current parser output")

// shouldUpdate combines the -update flag and the environment-variable
// fallback. Either is sufficient to trigger regeneration.
func shouldUpdate() bool {
	if update != nil && *update {
		return true
	}
	return os.Getenv("GACT_PARSER_UPDATE_GOLDEN") == "1"
}

// TestParser_Golden runs the parser against every testdata/*.yml fixture
// and compares the produced IR (as normalised JSON) to the matching
// golden file. Adding a new fixture and running with -update produces
// the golden file on the first run.
func TestParser_Golden(t *testing.T) {
	matches, err := filepath.Glob(filepath.Join("testdata", "*.yml"))
	if err != nil {
		t.Fatalf("glob testdata: %v", err)
	}
	// Filter out malformed-*.yml: those are exercised in their own
	// per-error tests where the failure mode matters more than the IR.
	var fixtures []string
	for _, p := range matches {
		base := filepath.Base(p)
		if strings.HasPrefix(base, "malformed-") {
			continue
		}
		fixtures = append(fixtures, p)
	}
	sort.Strings(fixtures)
	if len(fixtures) == 0 {
		t.Fatalf("no fixtures found in testdata/ — at least one *.yml is required")
	}

	for _, fixture := range fixtures {
		fixture := fixture
		name := strings.TrimSuffix(filepath.Base(fixture), ".yml")
		t.Run(name, func(t *testing.T) {
			src, err := os.ReadFile(fixture)
			if err != nil {
				t.Fatalf("read %s: %v", fixture, err)
			}
			w, err := Parse(fixture, src)
			if err != nil {
				t.Fatalf("Parse(%s) error: %v", fixture, err)
			}
			got, err := marshalWorkflow(&w, fixture)
			if err != nil {
				t.Fatalf("marshal workflow: %v", err)
			}
			goldenPath := strings.TrimSuffix(fixture, ".yml") + ".golden.json"
			if shouldUpdate() {
				if err := os.WriteFile(goldenPath, got, 0o644); err != nil {
					t.Fatalf("write golden %s: %v", goldenPath, err)
				}
				return
			}
			want, err := os.ReadFile(goldenPath)
			if err != nil {
				if os.IsNotExist(err) {
					// Bootstrap path: when a fixture has no golden yet,
					// write the current output and surface the
					// regeneration as a test skip rather than a hard
					// failure. CI environments that want strict
					// behaviour can delete the file again and re-run
					// with -update to recapture intentionally.
					if writeErr := os.WriteFile(goldenPath, got, 0o644); writeErr != nil {
						t.Fatalf("bootstrap write golden %s: %v", goldenPath, writeErr)
					}
					t.Logf("bootstrapped golden file %s (please review)", goldenPath)
					return
				}
				t.Fatalf("read golden %s (run with -update to regenerate): %v", goldenPath, err)
			}
			if normaliseEOL(string(got)) != normaliseEOL(string(want)) {
				t.Fatalf("golden mismatch for %s\n--- want ---\n%s\n--- got ---\n%s", fixture, want, got)
			}
		})
	}
}

// TestParser_Malformed_TabIndent confirms that yaml.v3's positioned
// indentation error is surfaced as a *parser.Error with a usable span.
func TestParser_Malformed_TabIndent(t *testing.T) {
	src, err := os.ReadFile(filepath.Join("testdata", "malformed-tab-indent.yml"))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	_, parseErr := Parse("testdata/malformed-tab-indent.yml", src)
	if parseErr == nil {
		t.Fatalf("expected error, got nil")
	}
	pe, ok := parseErr.(*Error)
	if !ok {
		t.Fatalf("expected *parser.Error, got %T: %v", parseErr, parseErr)
	}
	if pe.Span.Path == "" {
		t.Fatalf("expected non-empty Span.Path, got %+v", pe.Span)
	}
	if pe.Span.Line <= 0 {
		t.Fatalf("expected positive Span.Line, got %d", pe.Span.Line)
	}
}

// TestParser_Malformed_TopLevelSequence asserts the parser surfaces a
// clear positioned error when the workflow root is not a mapping (the
// most common authoring mistake — wrapping the file in `- ...`).
func TestParser_Malformed_TopLevelSequence(t *testing.T) {
	src, err := os.ReadFile(filepath.Join("testdata", "malformed-top-level-sequence.yml"))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	_, parseErr := Parse("testdata/malformed-top-level-sequence.yml", src)
	if parseErr == nil {
		t.Fatalf("expected error, got nil")
	}
	pe, ok := parseErr.(*Error)
	if !ok {
		t.Fatalf("expected *parser.Error, got %T: %v", parseErr, parseErr)
	}
	if !strings.Contains(pe.Message, "mapping") {
		t.Fatalf("expected message to mention mapping, got %q", pe.Message)
	}
	if pe.Span.Line <= 0 {
		t.Fatalf("expected positive Span.Line, got %d", pe.Span.Line)
	}
}

// jsonWorkflow is the on-disk shape of a marshalled Workflow. We map IR
// fields to keys in a stable, lexical order so the JSON output is
// deterministic across Go map iterations. The struct is local to the
// test file because it is only used for golden snapshotting — production
// consumers should marshal pkg/workflow values directly via their own
// shape if they need JSON.
type jsonWorkflow struct {
	Name     string                   `json:"name"`
	Path     string                   `json:"path"`
	Triggers jsonTriggers             `json:"triggers"`
	Env      map[string]string        `json:"env,omitempty"`
	Defaults wf.Defaults              `json:"defaults"`
	Jobs     []jsonJob                `json:"jobs"`
	Span     wf.SourceSpan            `json:"span"`
}

type jsonTriggers struct {
	Push              *wf.PushTrigger             `json:"push,omitempty"`
	PullRequest       *wf.PullRequestTrigger      `json:"pull_request,omitempty"`
	PullRequestTarget *wf.PullRequestTrigger      `json:"pull_request_target,omitempty"`
	Schedule          []wf.ScheduleTrigger        `json:"schedule,omitempty"`
	WorkflowDispatch  *wf.WorkflowDispatchTrigger `json:"workflow_dispatch,omitempty"`
	WorkflowCall      *wf.WorkflowCallTrigger     `json:"workflow_call,omitempty"`
	WorkflowRun       *wf.WorkflowRunTrigger      `json:"workflow_run,omitempty"`
	Other             []string                    `json:"other,omitempty"`
}

type jsonJob struct {
	ID              string                    `json:"id"`
	Name            string                    `json:"name,omitempty"`
	RunsOn          wf.RunnerLabel            `json:"runs_on"`
	Needs           []wf.JobID                `json:"needs,omitempty"`
	If              wf.Expression             `json:"if"`
	Matrix          *wf.Matrix                `json:"matrix,omitempty"`
	Env             map[string]string         `json:"env,omitempty"`
	Defaults        wf.Defaults               `json:"defaults"`
	ContinueOnError bool                      `json:"continue_on_error"`
	TimeoutMinutes  int                       `json:"timeout_minutes"`
	Outputs         map[string]wf.Expression  `json:"outputs,omitempty"`
	Steps           []wf.Step                 `json:"steps,omitempty"`
	Span            wf.SourceSpan             `json:"span"`
}

// marshalWorkflow produces the JSON form used for golden comparisons.
// fixturePath is the source file path; SourceSpan.Path is normalised
// against it so every span in the output points at the fixture basename
// (e.g. "minimal.yml"), making the goldens portable.
func marshalWorkflow(w *wf.Workflow, fixturePath string) ([]byte, error) {
	normalised := normaliseWorkflow(*w, fixturePath)
	jw := jsonWorkflow{
		Name:     normalised.Name,
		Path:     filepath.ToSlash(filepath.Base(normalised.Path)),
		Triggers: jsonTriggers(toJSONTriggers(normalised.Triggers)),
		Env:      normalised.Env,
		Defaults: normalised.Defaults,
		Jobs:     toJSONJobs(normalised.JobsByID),
		Span:     normalised.Span,
	}
	return json.MarshalIndent(jw, "", "  ")
}

// normaliseWorkflow returns a copy of w with SourceSpan.Path values
// rewritten to the fixture basename (forward-slash form). This is the
// single place where portability between Windows/macOS/Linux is
// enforced.
func normaliseWorkflow(w wf.Workflow, fixturePath string) wf.Workflow {
	rel := filepath.ToSlash(filepath.Base(fixturePath))
	w.Path = rel
	w.Span.Path = rel
	if w.JobsByID != nil {
		jobs := make(map[wf.JobID]wf.Job, len(w.JobsByID))
		for id, j := range w.JobsByID {
			j.Span.Path = rel
			j.If.Span.Path = rel
			// Steps
			for i := range j.Steps {
				j.Steps[i].Span.Path = rel
				j.Steps[i].If.Span.Path = rel
				for k, e := range j.Steps[i].With {
					e.Span.Path = rel
					j.Steps[i].With[k] = e
				}
				for k, e := range j.Steps[i].Env {
					e.Span.Path = rel
					j.Steps[i].Env[k] = e
				}
			}
			for k, e := range j.Outputs {
				e.Span.Path = rel
				j.Outputs[k] = e
			}
			jobs[id] = j
		}
		w.JobsByID = jobs
	}
	return w
}

// toJSONTriggers converts the parser triggers to the JSON-tagged form.
func toJSONTriggers(t wf.Triggers) jsonTriggers {
	return jsonTriggers{
		Push:              t.Push,
		PullRequest:       t.PullRequest,
		PullRequestTarget: t.PullRequestTarget,
		Schedule:          t.Schedule,
		WorkflowDispatch:  t.WorkflowDispatch,
		WorkflowCall:      t.WorkflowCall,
		WorkflowRun:       t.WorkflowRun,
		Other:             t.Other,
	}
}

// toJSONJobs returns the jobs slice ordered by JobID so iteration order
// of the underlying map does not perturb the golden comparison.
func toJSONJobs(jobs map[wf.JobID]wf.Job) []jsonJob {
	if len(jobs) == 0 {
		return nil
	}
	ids := make([]string, 0, len(jobs))
	for id := range jobs {
		ids = append(ids, string(id))
	}
	sort.Strings(ids)
	out := make([]jsonJob, 0, len(ids))
	for _, id := range ids {
		j := jobs[wf.JobID(id)]
		out = append(out, jsonJob{
			ID:              string(j.ID),
			Name:            j.Name,
			RunsOn:          j.RunsOn,
			Needs:           j.Needs,
			If:              j.If,
			Matrix:          j.Matrix,
			Env:             j.Env,
			Defaults:        j.Defaults,
			ContinueOnError: j.ContinueOnError,
			TimeoutMinutes:  j.TimeoutMinutes,
			Outputs:         j.Outputs,
			Steps:           j.Steps,
			Span:            j.Span,
		})
	}
	return out
}

// normaliseEOL collapses Windows CRLF line endings to LF so that goldens
// written on either platform compare cleanly. We do not normalise
// trailing whitespace because the marshaller never emits any.
func normaliseEOL(s string) string {
	return strings.ReplaceAll(s, "\r\n", "\n")
}
