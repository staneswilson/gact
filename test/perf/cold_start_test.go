// TestPerf_ColdStart anchors plan §6.7's first row — the floor every gact
// invocation pays before any useful work begins. The test exists so that
// a future change which accidentally inflates startup (e.g., adding a
// package-level init() that reads from disk, importing a heavyweight
// dependency, registering cobra flags eagerly when they should be lazy)
// shows up as a budget breach in CI rather than as a quiet user-visible
// regression.
package perf

import (
	"os/exec"
	"testing"
)

// TestPerf_ColdStart asserts that gact starts, runs a no-work leaf
// command, and exits within BudgetColdStart at p95. We invoke
// `gact version` because it exercises the full cobra initialisation
// path (root registration, version subcommand lookup, flag parsing,
// RunE call, formatted output) without touching the filesystem, the
// network, or any of the authoring/execution pipeline. That isolates
// the startup cost — what TestPerf_List and TestPerf_Lint pay on top
// of — from the cost of doing any actual work.
//
// If this test fails on a platform with high process-spawn overhead
// (Windows in particular pays for fork-equivalent + image load), the
// remedies in priority order are:
//
//  1. Profile gact's package init() chain with `GODEBUG=inittrace=1`
//     and remove any init() that does work other than registering with
//     a package-level registry.
//  2. Audit imports for transitive dependencies that pull in heavy
//     packages (the worst offenders historically have been crypto/tls,
//     net/http, and certain cobra completion paths).
//  3. As a last resort, document the platform-specific budget in ADR
//     form. Do NOT silently raise BudgetColdStart in budgets.go —
//     bumping a budget without an ADR defeats the regression gate.
func TestPerf_ColdStart(t *testing.T) {
	bin := buildGactOnce(t)
	durs := timeN(SampleCount, func() {
		// version is the cheapest leaf command and exits 0 cleanly, so
		// a non-nil err here means something is wrong with the binary
		// or the OS — never a perf signal — and bailing immediately
		// keeps the measurement clean.
		if err := exec.Command(bin, "version").Run(); err != nil {
			t.Fatalf("gact version: %v", err)
		}
	})
	p50 := percentile(durs, 50)
	p95 := percentile(durs, 95)
	t.Logf("cold-start durations (sorted, n=%d): %v", len(durs), durs)
	t.Logf("cold-start p50=%v p95=%v  budget=%v", p50, p95, BudgetColdStart)
	if p95 > BudgetColdStart {
		t.Fatalf("p95 cold start %v exceeds budget %v", p95, BudgetColdStart)
	}
}
