// TestPerf_Lint anchors plan §6.7's third row — the most demanding of
// the P0 budgets. lint is the heaviest of the three commands because it
// runs the full MaterialiseFull pipeline AND walks every static
// expression site (Job.If, Step.If, Step.With, Step.Env, Job.Outputs,
// WorkflowCall.Outputs) through the expression evaluator under a static
// ContextProvider, plus does the DAG cycle/unknown-needs sweep.
package perf

import (
	"os/exec"
	"testing"
)

// TestPerf_Lint asserts that `gact lint --dir <staged corpus>` completes
// within BudgetLint at p95. The 500 ms cap from plan §6.7 was set for a
// 30-workflow repo; the current corpus is 21 workflows so we expect
// some headroom.
//
// Exit code is intentionally ignored. Some staged workflows (notably
// 21_unknown_needs_dual_pass_unknown_needs.yml and
// 20_github_bogus_typo_typo.yml) intentionally emit diagnostics, so
// lint exits non-zero. Timing is the signal, not exit status.
//
// If this test fails, the likeliest culprits are:
//
//  1. A regression in the expression evaluator's per-call cost — a new
//     allocation in the lex/parse hot path, or a static ContextProvider
//     that does a filesystem lookup per Get. Profile with
//     `go test -run TestPerf_Lint -cpuprofile cpu.out -count=1` and
//     `go tool pprof cpu.out`.
//  2. A new lint walk that re-materialises the IR rather than reusing
//     the cached materialisation. Walks should consume the already-
//     built Workflow value, never re-parse.
//  3. The number of corpus workflows growing without a corresponding
//     budget revision. If the corpus crosses 30 the budget review
//     should be explicit (ADR), not implicit (silent bump).
func TestPerf_Lint(t *testing.T) {
	bin := buildGactOnce(t)
	wf := stageCorpus(t)
	durs := timeN(SampleCount, func() {
		// Ignore exit code: workflows 20 and 21 surface diagnostics and
		// lint exits non-zero. Timing is the signal.
		_ = exec.Command(bin, "lint", "--dir", wf).Run()
	})
	p50 := percentile(durs, 50)
	p95 := percentile(durs, 95)
	t.Logf("gact lint durations (sorted, n=%d): %v", len(durs), durs)
	t.Logf("gact lint p50=%v p95=%v  budget=%v", p50, p95, BudgetLint)
	if p95 > BudgetLint {
		t.Fatalf("p95 lint %v exceeds budget %v", p95, BudgetLint)
	}
}
