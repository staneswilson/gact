package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeWorkflow drops a workflow YAML file into <tmp>/.github/workflows/<name>.
// Returns the absolute path to the workflows directory so the test can pass
// it as --dir.
func writeWorkflow(t *testing.T, tmp string, files map[string]string) string {
	t.Helper()
	dir := filepath.Join(tmp, ".github", "workflows")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	for name, src := range files {
		full := filepath.Join(dir, name)
		if err := os.WriteFile(full, []byte(src), 0o644); err != nil {
			t.Fatalf("WriteFile %s: %v", name, err)
		}
	}
	return dir
}

// TestLint_CleanWorkflow_ExitsZero asserts the happy path: a syntactically and
// structurally valid workflow with reachable static expressions yields no
// diagnostics and exit 0.
func TestLint_CleanWorkflow_ExitsZero(t *testing.T) {
	tmp := t.TempDir()
	dir := writeWorkflow(t, tmp, map[string]string{
		"ci.yml": `name: ci
on: push
jobs:
  build:
    runs-on: ubuntu-latest
    steps:
      - name: greet
        run: echo "hi"
      - name: print ref
        if: github.event_name == 'push'
        run: echo "$GITHUB_REF"
`,
	})
	code, stdout, stderr := runWith(t, "lint", "--dir", dir)
	if code != 0 {
		t.Fatalf("exit code = %d, want 0\nstdout: %s\nstderr: %s", code, stdout, stderr)
	}
	if stderr != "" {
		t.Fatalf("expected empty stderr on clean workflow, got: %s", stderr)
	}
}

// TestLint_Cycle_ExitsNonZero asserts that a `needs:` cycle is surfaced as a
// diagnostic citing the offending job IDs and exits non-zero.
func TestLint_Cycle_ExitsNonZero(t *testing.T) {
	tmp := t.TempDir()
	dir := writeWorkflow(t, tmp, map[string]string{
		"ci.yml": `name: ci
on: push
jobs:
  a:
    needs: [b]
    runs-on: ubuntu-latest
    steps: [{ run: echo a }]
  b:
    needs: [a]
    runs-on: ubuntu-latest
    steps: [{ run: echo b }]
`,
	})
	code, _, stderr := runWith(t, "lint", "--dir", dir)
	if code == 0 {
		t.Fatalf("exit code = 0, want non-zero\nstderr: %s", stderr)
	}
	if !strings.Contains(stderr, "cycle in job graph") {
		t.Errorf("expected stderr to mention cycle, got:\n%s", stderr)
	}
	// The cycle path renders as "a -> b -> a" (or "b -> a -> b" depending on
	// which node TopoSort picks as the entry point). Asserting on the arrow
	// guarantees both jobs are cited in the loop rendering, not just present
	// somewhere in the noisy CLI output.
	if !strings.Contains(stderr, "a -> b") && !strings.Contains(stderr, "b -> a") {
		t.Errorf("expected stderr to render the cycle path, got:\n%s", stderr)
	}
}

// TestLint_UnknownNeeds_ExitsNonZero asserts that a `needs:` reference to a
// non-existent job surfaces as a diagnostic and exits non-zero.
func TestLint_UnknownNeeds_ExitsNonZero(t *testing.T) {
	tmp := t.TempDir()
	dir := writeWorkflow(t, tmp, map[string]string{
		"ci.yml": `name: ci
on: push
jobs:
  a:
    needs: [does-not-exist]
    runs-on: ubuntu-latest
    steps: [{ run: echo a }]
`,
	})
	code, _, stderr := runWith(t, "lint", "--dir", dir)
	if code == 0 {
		t.Fatalf("exit code = 0, want non-zero\nstderr: %s", stderr)
	}
	if !strings.Contains(stderr, "does-not-exist") {
		t.Errorf("expected stderr to mention missing needs target, got:\n%s", stderr)
	}
}

// TestLint_JobNoSteps_ExitsNonZero asserts that a job with an empty steps
// block surfaces the SCHEMA-JOB-NO-STEPS diagnostic.
func TestLint_JobNoSteps_ExitsNonZero(t *testing.T) {
	tmp := t.TempDir()
	dir := writeWorkflow(t, tmp, map[string]string{
		"ci.yml": `name: ci
on: push
jobs:
  empty:
    runs-on: ubuntu-latest
    steps: []
`,
	})
	code, _, stderr := runWith(t, "lint", "--dir", dir)
	if code == 0 {
		t.Fatalf("exit code = 0, want non-zero\nstderr: %s", stderr)
	}
	if !strings.Contains(stderr, "SCHEMA-JOB-NO-STEPS") {
		t.Errorf("expected stderr to mention SCHEMA-JOB-NO-STEPS, got:\n%s", stderr)
	}
}

// TestLint_StepEmpty_ExitsNonZero asserts that a step with neither `uses:` nor
// `run:` surfaces the SCHEMA-STEP-EMPTY diagnostic.
//
// NOTE: the related SCHEMA-STEP-AMBIGUOUS code (step with BOTH `uses:` and
// `run:`) is intentionally not exercised here because the YAML parser drops
// the secondary field instead of preserving both — see the report-back for
// details. The schema validator itself handles the ambiguous shape correctly
// when fed a hand-constructed IR (covered in schema/validate_test.go).
func TestLint_StepEmpty_ExitsNonZero(t *testing.T) {
	tmp := t.TempDir()
	dir := writeWorkflow(t, tmp, map[string]string{
		"ci.yml": `name: ci
on: push
jobs:
  build:
    runs-on: ubuntu-latest
    steps:
      - name: nothing-to-do
`,
	})
	code, _, stderr := runWith(t, "lint", "--dir", dir)
	if code == 0 {
		t.Fatalf("exit code = 0, want non-zero\nstderr: %s", stderr)
	}
	if !strings.Contains(stderr, "SCHEMA-STEP-EMPTY") {
		t.Errorf("expected stderr to mention SCHEMA-STEP-EMPTY, got:\n%s", stderr)
	}
}

// TestLint_NoTriggers_ExitsNonZero asserts that a workflow with an empty `on:`
// block surfaces SCHEMA-NO-TRIGGERS.
func TestLint_NoTriggers_ExitsNonZero(t *testing.T) {
	tmp := t.TempDir()
	dir := writeWorkflow(t, tmp, map[string]string{
		"ci.yml": `name: ci
on: {}
jobs:
  build:
    runs-on: ubuntu-latest
    steps:
      - run: echo hi
`,
	})
	code, _, stderr := runWith(t, "lint", "--dir", dir)
	if code == 0 {
		t.Fatalf("exit code = 0, want non-zero\nstderr: %s", stderr)
	}
	if !strings.Contains(stderr, "SCHEMA-NO-TRIGGERS") {
		t.Errorf("expected stderr to mention SCHEMA-NO-TRIGGERS, got:\n%s", stderr)
	}
}

// TestLint_ExpressionSyntaxError_ExitsNonZero asserts that a malformed
// expression surfaces a diagnostic tagged with the expression's span.
func TestLint_ExpressionSyntaxError_ExitsNonZero(t *testing.T) {
	tmp := t.TempDir()
	dir := writeWorkflow(t, tmp, map[string]string{
		"ci.yml": `name: ci
on: push
jobs:
  build:
    runs-on: ubuntu-latest
    steps:
      - name: borked
        if: contains(
        run: echo hi
`,
	})
	code, _, stderr := runWith(t, "lint", "--dir", dir)
	if code == 0 {
		t.Fatalf("exit code = 0, want non-zero\nstderr: %s", stderr)
	}
	if !strings.Contains(stderr, "EXPR") {
		t.Errorf("expected stderr to mention an EXPR diagnostic code, got:\n%s", stderr)
	}
}

// TestLint_ExpressionUnknownGithubField_ExitsNonZero asserts that referencing
// a field of the knowable `github.*` scope that does not exist surfaces a
// diagnostic. The static context returns Null for these; lint detects them by
// AST inspection (see lint.go).
func TestLint_ExpressionUnknownGithubField_ExitsNonZero(t *testing.T) {
	tmp := t.TempDir()
	dir := writeWorkflow(t, tmp, map[string]string{
		"ci.yml": `name: ci
on: push
jobs:
  build:
    runs-on: ubuntu-latest
    steps:
      - name: typo
        if: github.bogus == 'x'
        run: echo hi
`,
	})
	code, _, stderr := runWith(t, "lint", "--dir", dir)
	if code == 0 {
		t.Fatalf("exit code = 0, want non-zero\nstderr: %s", stderr)
	}
	if !strings.Contains(stderr, "bogus") {
		t.Errorf("expected stderr to mention the bogus identifier, got:\n%s", stderr)
	}
}

// TestLint_ExpressionSecretsReference_ExitsZero asserts that referencing a
// secret (the opaque scope) is NOT an error: lint cannot know the real
// secret list, so any `secrets.X` is presumed legitimate.
func TestLint_ExpressionSecretsReference_ExitsZero(t *testing.T) {
	tmp := t.TempDir()
	dir := writeWorkflow(t, tmp, map[string]string{
		"ci.yml": `name: ci
on: push
jobs:
  build:
    runs-on: ubuntu-latest
    steps:
      - name: secret-guarded
        if: secrets.NPM_TOKEN != ''
        run: echo "publishing"
`,
	})
	code, stdout, stderr := runWith(t, "lint", "--dir", dir)
	if code != 0 {
		t.Fatalf("exit code = %d, want 0 (secrets references should not lint as errors)\nstdout: %s\nstderr: %s", code, stdout, stderr)
	}
	if stderr != "" {
		t.Fatalf("expected empty stderr, got: %s", stderr)
	}
}

// TestLint_MultipleWorkflows_OnlyBadOneFlagged asserts that with two workflow
// files in the directory, lint reports diagnostics for the bad file and stays
// silent about the clean file, while still exiting non-zero.
func TestLint_MultipleWorkflows_OnlyBadOneFlagged(t *testing.T) {
	tmp := t.TempDir()
	dir := writeWorkflow(t, tmp, map[string]string{
		"good.yml": `name: good
on: push
jobs:
  build:
    runs-on: ubuntu-latest
    steps:
      - run: echo ok
`,
		"bad.yml": `name: bad
on: push
jobs:
  empty:
    runs-on: ubuntu-latest
    steps: []
`,
	})
	code, _, stderr := runWith(t, "lint", "--dir", dir)
	if code == 0 {
		t.Fatalf("exit code = 0, want non-zero\nstderr: %s", stderr)
	}
	if !strings.Contains(stderr, "bad.yml") {
		t.Errorf("expected stderr to cite bad.yml, got:\n%s", stderr)
	}
	if strings.Contains(stderr, "good.yml") {
		t.Errorf("expected stderr to stay silent about good.yml, got:\n%s", stderr)
	}
}
