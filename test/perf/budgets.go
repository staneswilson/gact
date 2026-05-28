// Package perf exercises the performance budgets declared in plan §6.7.
// Each Test* in this directory measures a representative cold-start, list,
// or lint invocation against a staged 21-workflow corpus and fails the
// suite when the p95 over a small sample exceeds the corresponding budget.
//
// Design choices, in the order they tend to come up when reading the code:
//
//  1. Tests, not benchmarks. The plan classifies these as regression GATES
//     (release blockers when red), not as informational `go test -bench`
//     output. A testing.T-driven Fatalf is the only way for `go test` to
//     fail the suite on a budget breach — benchmarks are advisory by
//     default and would not gate CI.
//
//  2. Pre-built binary, not `go run`. `go run` recompiles per invocation
//     (~1–2 s of overhead), which would dominate the 50 ms cold-start
//     budget. buildGactOnce builds the binary into a per-process temp
//     directory using sync.Once, so the build cost is amortised across
//     every test in the package and the timed window contains only the
//     real cost of starting and executing gact.
//
//  3. Flattened corpus stage, not the original test/corpus layout.
//     `gact list --dir` and `gact lint --dir` treat the supplied
//     directory as a single flat workflows folder. Pointing them at the
//     corpus root would only see the README + comparison test;
//     pointing at any single NN_ subdirectory would only exercise one
//     workflow. stageCorpus copies all 21 yml files into one wf/
//     directory under t.TempDir() so a single invocation exercises the
//     full corpus shape — matching the "30-workflow repo" shape called
//     out in plan §6.7 closely enough that the budget is meaningful.
//
//  4. 11-sample p95, nearest-rank. The plan §0.19 spec says "run 11
//     times, take median + p95". For N=11 with nearest-rank, p95 lands
//     on sorted[10] — the maximum sample. That makes the assertion a
//     conservative "worst-of-eleven must fit the budget" reading, which
//     is what we want for a regression gate. The cost is that a single
//     pathological OS-scheduler hiccup can flake the test; if that
//     becomes an operational pain, the right fix is to bump SampleCount
//     (not to soften the percentile, which would weaken the gate).
package perf

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"sync"
	"testing"
	"time"
)

// Budgets locked in by plan §6.7. A regression that exceeds one of these
// fails the suite — there is no "warn" tier here on purpose; the plan
// classifies perf regressions as release blockers.
//
// BudgetColdStart is platform-stratified per docs/adr/011-cold-start-budget-windows.md.
// The plan's original 50 ms figure was Linux-anchored; Windows pays a
// structural ~30–50 ms of OS-level process-spawn cost (CreateProcess +
// PE loader + Go runtime startup) before any user init runs, which puts
// the realistic floor at ~60 ms p95. The 100 ms Windows budget keeps the
// gate meaningful (catches new init-time work) without flaking on the
// platform floor. See the ADR for the full profile and rationale.
const (
	BudgetList = 200 * time.Millisecond
	BudgetLint = 500 * time.Millisecond

	// SampleCount is the number of timed runs per test. Plan §0.19 spec:
	// "run 11 times, take median + p95". 11 is small enough to stay fast
	// and odd enough that the median is a single sample, not an average.
	SampleCount = 11
)

// BudgetColdStart is the p95 ceiling for `gact version` and is set per
// platform — see ADR-011. Read it as a value, not a constant, because
// Go does not permit runtime-conditional constants. The selection is
// done at package init so every test in this package sees a stable
// value.
var BudgetColdStart = func() time.Duration {
	if runtime.GOOS == "windows" {
		return 100 * time.Millisecond
	}
	return 50 * time.Millisecond
}()

var (
	gactBinOnce sync.Once
	gactBinDir  string
	gactBinPath string
	gactBinErr  error
)

// buildGactOnce builds cmd/gact once per test process and returns the
// path to the compiled binary. sync.Once means the second and subsequent
// callers reuse the cached path; os.MkdirTemp is used instead of
// t.TempDir because the binary must outlive any individual test (multiple
// tests in this package share it).
//
// Build errors are sticky: once gactBinErr is set, every caller observes
// the same failure. We surface them via t.Fatalf rather than t.Skip
// because a build failure here means the test environment is broken,
// not that the perf gate is irrelevant.
func buildGactOnce(t *testing.T) string {
	t.Helper()
	gactBinOnce.Do(func() {
		dir, err := os.MkdirTemp("", "gact-perf-bin-")
		if err != nil {
			gactBinErr = fmt.Errorf("mkdir temp for binary: %w", err)
			return
		}
		gactBinDir = dir
		bin := filepath.Join(dir, "gact")
		if runtime.GOOS == "windows" {
			bin += ".exe"
		}
		// We build from test/perf/ → ../../cmd/gact. -trimpath keeps the
		// binary byte-stable across rebuilds in case a future test wants
		// to hash it (e.g., to invalidate a measurement cache).
		cmd := exec.Command("go", "build", "-trimpath", "-o", bin, "../../cmd/gact")
		if out, err := cmd.CombinedOutput(); err != nil {
			gactBinErr = fmt.Errorf("go build cmd/gact: %w\n%s", err, out)
			return
		}
		gactBinPath = bin
	})
	if gactBinErr != nil {
		t.Fatalf("build gact: %v", gactBinErr)
	}
	return gactBinPath
}

// stageCorpus copies every .yml file under ../corpus/NN_*/ into a single
// flat <t.TempDir()>/wf/ directory and returns its path. The result is
// what gact list/lint --dir expects: a directory whose immediate
// children are workflow YAML files.
//
// Copies are renamed to "<parentDir>_<basename>" so the deterministic
// ordering of the corpus is preserved when discoverWorkflows lex-sorts
// the result; two corpus dirs that happen to share a yml basename would
// otherwise collide. None currently do, but the rename future-proofs
// the staging logic.
func stageCorpus(t *testing.T) string {
	t.Helper()
	wf := filepath.Join(t.TempDir(), "wf")
	if err := os.MkdirAll(wf, 0o755); err != nil {
		t.Fatalf("mkdir wf: %v", err)
	}
	matches, err := filepath.Glob("../corpus/[0-9][0-9]_*/*.yml")
	if err != nil {
		t.Fatalf("glob corpus: %v", err)
	}
	if len(matches) < 20 {
		t.Fatalf("expected at least 20 corpus workflows, got %d", len(matches))
	}
	for _, m := range matches {
		// m looks like "../corpus/03_multi_job_needs/pipeline.yml"; we
		// want "<wf>/03_multi_job_needs_pipeline.yml".
		parent := filepath.Base(filepath.Dir(m))
		name := parent + "_" + filepath.Base(m)
		if err := copyFile(m, filepath.Join(wf, name)); err != nil {
			t.Fatalf("copy %s: %v", m, err)
		}
	}
	return wf
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()
	_, err = io.Copy(out, in)
	return err
}

// timeN runs fn one untimed warmup pass to populate the OS image cache
// (and any Go runtime/cobra state shared between invocations of a child
// binary), then runs fn n more times with timing recorded, and returns
// the timed durations sorted ascending.
//
// The warmup is critical for stable measurement. Without it, the first
// invocation of a freshly-built binary pays a one-time tax to load
// .exe pages into the kernel image cache and resolve dynamic imports;
// on Windows this can be 1+ second, which would dominate any small-n
// p95 reading and turn the assertion into a noisy gate on OS state
// rather than gact's actual cost.
//
// The caller is responsible for ensuring fn does not retain references
// to per-call state that would skew subsequent runs (e.g., reusing the
// same exec.Cmd struct, which is single-use).
func timeN(n int, fn func()) []time.Duration {
	fn() // warmup — discarded
	out := make([]time.Duration, 0, n)
	for i := 0; i < n; i++ {
		start := time.Now()
		fn()
		out = append(out, time.Since(start))
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

// percentile returns the p-th percentile of sorted durations using
// nearest-rank with floor rounding. p must be in 0..100.
//
// For n=11 and p=95, rank = (95*11)/100 = 10 (zero-indexed) which is
// the largest sample — the conservative "worst of eleven" reading the
// plan intends. For n=11 and p=50, rank = 5, which is the true median
// (the 6th sample by 1-indexed count). For p outside 0..100 the
// caller is doing something wrong; we clamp rather than panic so the
// function stays usable as an inline helper.
func percentile(sorted []time.Duration, p int) time.Duration {
	if len(sorted) == 0 {
		return 0
	}
	if p < 0 {
		p = 0
	}
	if p > 100 {
		p = 100
	}
	rank := (p * len(sorted)) / 100
	if rank >= len(sorted) {
		rank = len(sorted) - 1
	}
	return sorted[rank]
}

// TestMain runs after every Test* in this package completes and removes
// the temp directory holding the cached gact binary. We don't bother
// cleaning if the build never succeeded — gactBinDir would be empty in
// that case. os.RemoveAll is best-effort; a leftover binary on tmp does
// not fail the suite.
func TestMain(m *testing.M) {
	code := m.Run()
	if gactBinDir != "" {
		_ = os.RemoveAll(gactBinDir)
	}
	os.Exit(code)
}
