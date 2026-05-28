// TestPerf_List anchors plan §6.7's second row — the budget for
// enumerating workflows in a repo-sized directory. list runs
// MaterialiseFull on every workflow it discovers (parse + schema
// validate + matrix expand + DAG verify) so its time scales with both
// workflow count AND per-workflow IR complexity.
package perf

import (
	"os/exec"
	"testing"
)

// TestPerf_List asserts that `gact list --dir <staged corpus>` completes
// within BudgetList at p95. The staged corpus mirrors a 20+ workflow
// repo — see budgets.go::stageCorpus for the staging rationale.
//
// Exit code is intentionally ignored. Some staged workflows (notably
// 21_unknown_needs_dual_pass_unknown_needs.yml) intentionally surface
// diagnostics, so list exits non-zero. The plan says "list" treats
// per-file errors as non-fatal: it still emits a tree for the remaining
// workflows and the timing for the full sweep is what we care about.
//
// If this test fails, the likeliest culprit is MaterialiseFull doing
// more work than needed in the list path — list only needs the static
// IR, not the resolved-action manifests. If a future change pulls
// resolution into MaterialiseFull, list will inherit the new cost and
// this budget will break. The fix in that case is to thread a
// MaterialisePartial+lightweight projection through list instead of
// raising the budget.
func TestPerf_List(t *testing.T) {
	bin := buildGactOnce(t)
	wf := stageCorpus(t)
	durs := timeN(SampleCount, func() {
		// Ignore exit code: workflow 21 surfaces an unknown-needs
		// diagnostic and list exits non-zero. Timing is the signal,
		// not exit status.
		_ = exec.Command(bin, "list", "--dir", wf).Run()
	})
	p50 := percentile(durs, 50)
	p95 := percentile(durs, 95)
	t.Logf("gact list durations (sorted, n=%d): %v", len(durs), durs)
	t.Logf("gact list p50=%v p95=%v  budget=%v", p50, p95, BudgetList)
	if p95 > BudgetList {
		t.Fatalf("p95 list %v exceeds budget %v", p95, BudgetList)
	}
}
