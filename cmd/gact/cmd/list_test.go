package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// emptyWorkflowsDir creates and returns <tmp>/.github/workflows for tests
// that need to point --dir at a directory with no files in it. The lint test
// helper `writeWorkflow` covers the populated case; this helper covers the
// empty one without duplicating its files-map plumbing.
func emptyWorkflowsDir(t *testing.T) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), ".github", "workflows")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	return dir
}

func TestList_EmptyDir_PrintsNoWorkflowsFound(t *testing.T) {
	dir := emptyWorkflowsDir(t)

	code, stdout, stderr := runWith(t, "list", "--dir", dir)
	if code != 0 {
		t.Fatalf("exit code: got %d, want 0; stderr=%q", code, stderr)
	}
	if stdout != "" {
		t.Errorf("expected empty stdout, got %q", stdout)
	}
	if !strings.Contains(stderr, "no workflows found") {
		t.Errorf("expected stderr to mention 'no workflows found', got %q", stderr)
	}
}

func TestList_SingleWorkflow_SingleJob_RendersTree(t *testing.T) {
	dir := writeWorkflow(t, t.TempDir(), map[string]string{
		"ci.yml": `name: ci
on: push
jobs:
  test:
    runs-on: ubuntu-latest
    steps:
      - name: greet
        run: echo "hello"
`,
	})

	code, stdout, stderr := runWith(t, "list", "--dir", dir)
	if code != 0 {
		t.Fatalf("exit code: got %d, want 0; stderr=%q", code, stderr)
	}

	want := "ci.yml: ci\n" +
		"  test (ubuntu-latest)\n" +
		"    - run: echo \"hello\"\n"
	if stdout != want {
		t.Errorf("stdout mismatch\nwant:\n%s\ngot:\n%s", want, stdout)
	}
}

func TestList_MultipleWorkflows_SortedByPath(t *testing.T) {
	dir := writeWorkflow(t, t.TempDir(), map[string]string{
		"zeta.yml": `name: zeta
on: push
jobs:
  z:
    runs-on: ubuntu-latest
    steps:
      - run: echo z
`,
		"alpha.yml": `name: alpha
on: push
jobs:
  a:
    runs-on: ubuntu-latest
    steps:
      - run: echo a
`,
	})

	code, stdout, stderr := runWith(t, "list", "--dir", dir)
	if code != 0 {
		t.Fatalf("exit code: got %d, want 0; stderr=%q", code, stderr)
	}
	idxAlpha := strings.Index(stdout, "alpha.yml:")
	idxZeta := strings.Index(stdout, "zeta.yml:")
	if idxAlpha == -1 || idxZeta == -1 {
		t.Fatalf("expected both workflow headers in stdout, got:\n%s", stdout)
	}
	if idxAlpha > idxZeta {
		t.Errorf("expected alpha.yml before zeta.yml, got:\n%s", stdout)
	}
}

func TestList_MatrixExpansion_GroupsChildrenUnderParent(t *testing.T) {
	dir := writeWorkflow(t, t.TempDir(), map[string]string{
		"ci.yml": `name: ci
on: push
jobs:
  build:
    runs-on: ubuntu-latest
    strategy:
      matrix:
        node: [18, 20]
    steps:
      - run: echo "build"
`,
	})

	code, stdout, stderr := runWith(t, "list", "--dir", dir)
	if code != 0 {
		t.Fatalf("exit code: got %d, want 0; stderr=%q", code, stderr)
	}
	wantSubstrings := []string{
		"ci.yml: ci",
		"build (ubuntu-latest) [matrix]",
		"build_0 (combo: node=18)",
		"build_1 (combo: node=20)",
	}
	for _, s := range wantSubstrings {
		if !strings.Contains(stdout, s) {
			t.Errorf("expected stdout to contain %q, got:\n%s", s, stdout)
		}
	}
	parentIdx := strings.Index(stdout, "build (ubuntu-latest) [matrix]")
	c0Idx := strings.Index(stdout, "build_0 (combo: node=18)")
	c1Idx := strings.Index(stdout, "build_1 (combo: node=20)")
	if !(parentIdx < c0Idx && c0Idx < c1Idx) {
		t.Errorf("expected parent then build_0 then build_1, got:\n%s", stdout)
	}
}

func TestList_JobNeeds_RenderedInJobLine(t *testing.T) {
	dir := writeWorkflow(t, t.TempDir(), map[string]string{
		"ci.yml": `name: ci
on: push
jobs:
  lint:
    runs-on: ubuntu-latest
    steps:
      - run: echo lint
  test:
    runs-on: ubuntu-latest
    needs: lint
    steps:
      - run: echo test
`,
	})

	code, stdout, stderr := runWith(t, "list", "--dir", dir)
	if code != 0 {
		t.Fatalf("exit code: got %d, want 0; stderr=%q", code, stderr)
	}
	if !strings.Contains(stdout, "test (ubuntu-latest) [needs: lint]") {
		t.Errorf("expected test job line to include [needs: lint], got:\n%s", stdout)
	}
}

func TestList_ParseError_NonFatal_ContinuesOtherWorkflows(t *testing.T) {
	dir := writeWorkflow(t, t.TempDir(), map[string]string{
		"good.yml": `name: good
on: push
jobs:
  a:
    runs-on: ubuntu-latest
    steps:
      - run: echo a
`,
		// broken.yml has a tab in indentation, which yaml.v3 rejects with a
		// "found character that cannot start any token" parse error.
		"broken.yml": "name: broken\non: push\njobs:\n  build:\n\t\trun-on: ubuntu-latest\n",
	})

	code, stdout, stderr := runWith(t, "list", "--dir", dir)
	if code == 0 {
		t.Fatalf("expected non-zero exit on parse error, got 0")
	}
	if !strings.Contains(stdout, "good.yml: good") {
		t.Errorf("expected good.yml to still be listed in stdout, got:\n%s", stdout)
	}
	if !strings.Contains(stderr, "broken.yml") {
		t.Errorf("expected stderr to mention broken.yml, got:\n%s", stderr)
	}
	if !strings.Contains(stderr, "error:") {
		t.Errorf("expected stderr to mark severity 'error:', got:\n%s", stderr)
	}
}

func TestList_SchemaError_StillListsWorkflow_AndExitsNonZero(t *testing.T) {
	dir := writeWorkflow(t, t.TempDir(), map[string]string{
		// no-steps.yml parses cleanly but trips CodeJobNoSteps — we
		// materialised the IR successfully, so the workflow should still
		// appear in the listing while the diagnostic exits us non-zero.
		"no-steps.yml": `name: empty-steps
on: push
jobs:
  build:
    runs-on: ubuntu-latest
    steps: []
`,
	})

	code, stdout, stderr := runWith(t, "list", "--dir", dir)
	if code == 0 {
		t.Fatalf("expected non-zero exit when a schema diagnostic is present, got 0")
	}
	if !strings.Contains(stdout, "no-steps.yml: empty-steps") {
		t.Errorf("expected workflow to be listed despite schema diagnostic, got stdout:\n%s", stdout)
	}
	if !strings.Contains(stderr, "SCHEMA-JOB-NO-STEPS") {
		t.Errorf("expected stderr to render schema diagnostic code, got:\n%s", stderr)
	}
}

func TestList_DefaultDir_UsesGithubWorkflowsRelativeToCwd(t *testing.T) {
	// When --dir is omitted, list reads from .github/workflows relative to
	// the current working directory. We chdir into a tempdir so the test
	// observes that resolution without touching the developer's checkout.
	repo := t.TempDir()
	_ = writeWorkflow(t, repo, map[string]string{
		"ci.yml": `name: ci
on: push
jobs:
  a:
    runs-on: ubuntu-latest
    steps:
      - run: echo a
`,
	})

	origWD, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd: %v", err)
	}
	t.Cleanup(func() { _ = os.Chdir(origWD) })
	if err := os.Chdir(repo); err != nil {
		t.Fatalf("Chdir: %v", err)
	}

	code, stdout, stderr := runWith(t, "list")
	if code != 0 {
		t.Fatalf("exit code: got %d, want 0; stderr=%q", code, stderr)
	}
	if !strings.Contains(stdout, "ci.yml: ci") {
		t.Errorf("expected stdout to list ci.yml, got:\n%s", stdout)
	}
}
