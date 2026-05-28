package expr

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	pubexpr "github.com/staneswilson/gact/pkg/expr"
)

// hashFiles tests live in the implementation package so they can dial
// straight into hashFilesFn for the kind/arity error cases without going
// through the parser. The happy-path tests go through evalSrc to confirm
// the end-to-end dispatch path is wired the same way every other
// built-in is.

// fixturesA returns the absolute path to the committed three-file
// fixture set (alpha.txt, beta.txt, gamma.txt). Tests t.Chdir into it
// so hashFiles' cwd-based resolution sees the fixtures as the
// workspace root.
func fixturesA(t *testing.T) string {
	t.Helper()
	p, err := filepath.Abs(filepath.Join("testdata", "hashfiles", "a"))
	if err != nil {
		t.Fatalf("abs(fixturesA): %v", err)
	}
	return p
}

func fixturesEmpty(t *testing.T) string {
	t.Helper()
	p, err := filepath.Abs(filepath.Join("testdata", "hashfiles", "empty"))
	if err != nil {
		t.Fatalf("abs(fixturesEmpty): %v", err)
	}
	return p
}

// TestHashFiles_StableAcrossRuns hashes the same pattern over the same
// directory twice and asserts both runs produce identical digests. This
// is the bedrock guarantee: hashFiles is the cache-key input most
// workflows depend on, so any non-determinism is fatal.
func TestHashFiles_StableAcrossRuns(t *testing.T) {
	t.Chdir(fixturesA(t))
	got1 := evalSrc(t, "hashFiles('*.txt')", EmptyContext()).AsString()
	got2 := evalSrc(t, "hashFiles('*.txt')", EmptyContext()).AsString()
	if got1 == "" {
		t.Fatalf("expected non-empty hash, got empty")
	}
	if got1 != got2 {
		t.Fatalf("non-deterministic hash: %q vs %q", got1, got2)
	}
	// Sanity check: SHA-256 hex is 64 chars.
	if len(got1) != 64 {
		t.Fatalf("hash length = %d, want 64 hex chars", len(got1))
	}
}

// TestHashFiles_TreatsAbsentPathAsEmpty pins the GH-faithful semantic
// that no matches means "" with no error. A workflow that hashes a
// missing optional path must still evaluate so the surrounding
// expression (e.g. `hashFiles('go.sum') || 'fresh'`) can continue.
func TestHashFiles_TreatsAbsentPathAsEmpty(t *testing.T) {
	t.Chdir(fixturesEmpty(t))
	got := evalSrc(t, "hashFiles('*.gone')", EmptyContext())
	if got.Kind != pubexpr.KindString {
		t.Fatalf("kind = %d, want string", got.Kind)
	}
	if got.AsString() != "" {
		t.Fatalf("want empty string, got %q", got.AsString())
	}
}

// TestHashFiles_ContentChangeChangesHash builds a fresh fixture in a
// temp dir, hashes it, mutates one file, and asserts the digest
// changes. We use t.TempDir to avoid touching committed fixtures.
func TestHashFiles_ContentChangeChangesHash(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("v1\n"), 0o644); err != nil {
		t.Fatalf("write a.txt: %v", err)
	}
	t.Chdir(dir)
	before := evalSrc(t, "hashFiles('a.txt')", EmptyContext()).AsString()
	if before == "" {
		t.Fatalf("expected non-empty pre-mutation hash, got empty")
	}
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("v2\n"), 0o644); err != nil {
		t.Fatalf("rewrite a.txt: %v", err)
	}
	after := evalSrc(t, "hashFiles('a.txt')", EmptyContext()).AsString()
	if before == after {
		t.Fatalf("hash did not change on content edit (both %q)", before)
	}
}

// TestHashFiles_MultiplePatterns_DedupesAndSorts confirms that passing
// the same file twice (once via a literal pattern, once via a glob that
// also matches it) yields the same digest as a single pattern. If the
// implementation forgot to dedupe, the file would hash twice and the
// digests would diverge.
func TestHashFiles_MultiplePatterns_DedupesAndSorts(t *testing.T) {
	t.Chdir(fixturesA(t))
	single := evalSrc(t, "hashFiles('*.txt')", EmptyContext()).AsString()
	dup := evalSrc(t, "hashFiles('alpha.txt', '*.txt')", EmptyContext()).AsString()
	if single == "" {
		t.Fatalf("single-pattern hash empty")
	}
	if single != dup {
		t.Fatalf("dedupe failure: single=%q dup=%q", single, dup)
	}
}

// TestHashFiles_DoublestarMatchesNested verifies the `**` recursive
// glob includes files at any depth and that path-sensitivity in the
// digest layout reflects nested layout. We build a two-level fixture
// in TempDir so the test is independent of committed data.
func TestHashFiles_DoublestarMatchesNested(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "a"), 0o755); err != nil {
		t.Fatalf("mkdir a: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "b", "c"), 0o755); err != nil {
		t.Fatalf("mkdir b/c: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "a", "x.txt"), []byte("x\n"), 0o644); err != nil {
		t.Fatalf("write a/x.txt: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "b", "c", "y.txt"), []byte("y\n"), 0o644); err != nil {
		t.Fatalf("write b/c/y.txt: %v", err)
	}
	t.Chdir(dir)

	all := evalSrc(t, "hashFiles('**/*.txt')", EmptyContext()).AsString()
	if all == "" {
		t.Fatalf("expected non-empty hash for nested glob, got empty")
	}

	// Sanity: hashing only the shallow file should differ from hashing
	// the whole tree. If the digests were equal, **/*.txt would have
	// silently dropped the nested match.
	shallow := evalSrc(t, "hashFiles('a/x.txt')", EmptyContext()).AsString()
	if shallow == "" {
		t.Fatalf("expected non-empty hash for shallow file, got empty")
	}
	if all == shallow {
		t.Fatalf("** glob did not include nested file (both %q)", all)
	}
}

// TestHashFiles_NonexistentDirectory asserts that a pattern rooted in a
// directory that does not exist returns "" — not an error. Workflows
// using `hashFiles('vendor/**/*.go')` on a repo without vendored code
// must still evaluate.
func TestHashFiles_NonexistentDirectory(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	got := evalSrc(t, "hashFiles('does-not-exist/**/*.txt')", EmptyContext())
	if got.Kind != pubexpr.KindString {
		t.Fatalf("kind = %d, want string", got.Kind)
	}
	if got.AsString() != "" {
		t.Fatalf("want empty string for missing dir, got %q", got.AsString())
	}
}

// TestHashFiles_NoArgs_ReturnsError pins the arity guard. GH does the
// same — calling hashFiles() with no patterns is a workflow-author bug
// and we surface it loudly rather than returning "".
func TestHashFiles_NoArgs_ReturnsError(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	e, err := compile("hashFiles()")
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	_, err = e.evaluate(EmptyContext())
	if err == nil {
		t.Fatal("expected error for hashFiles() with no args, got nil")
	}
	if !strings.Contains(strings.ToLower(err.Error()), "at least one pattern") {
		t.Fatalf("error %q does not mention pattern requirement", err.Error())
	}
}

// TestHashFiles_NonStringArg_ReturnsError covers the type guard. The
// parser accepts `hashFiles(42)` because the grammar does not type-check
// arguments, so the function itself must reject non-string kinds.
func TestHashFiles_NonStringArg_ReturnsError(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	e, err := compile("hashFiles(42)")
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	_, err = e.evaluate(EmptyContext())
	if err == nil {
		t.Fatal("expected error for non-string arg, got nil")
	}
	if !strings.Contains(strings.ToLower(err.Error()), "string") {
		t.Fatalf("error %q does not mention string requirement", err.Error())
	}
}
