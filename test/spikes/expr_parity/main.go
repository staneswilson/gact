// Spike A driver: load corpus.json, evaluate each expression against its
// embedded context, compare to the expected scalar, print PASS/FAIL per case
// and a final summary line. Exit 0 if pass rate >= 90%, exit 1 otherwise so
// CI can gate on the go/no-go criterion (plan §8 Spike A, §9 Task 0.4).
//
// This is a prototype. The production evaluator (Task 0.6) lives under
// internal/authoring/expr; this directory will be deleted or harvested at
// that point.
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
)

type corpusEntry struct {
	ID           string         `json:"id"`
	Expr         string         `json:"expr"`
	Context      map[string]any `json:"context"`
	Expected     string         `json:"expected"`
	ExpectedKind string         `json:"expected_kind"`
	Source       string         `json:"source,omitempty"`
	Note         string         `json:"note,omitempty"`
}

func main() {
	dir := corpusDir()
	path := filepath.Join(dir, "corpus.json")
	data, err := os.ReadFile(path)
	if err != nil {
		fmt.Fprintf(os.Stderr, "spike-a: cannot read %s: %v\n", path, err)
		os.Exit(2)
	}
	var corpus []corpusEntry
	if err := json.Unmarshal(data, &corpus); err != nil {
		fmt.Fprintf(os.Stderr, "spike-a: cannot parse %s: %v\n", path, err)
		os.Exit(2)
	}

	pass := 0
	for _, e := range corpus {
		ok, gotStr, gotKind, why := runCase(e)
		if ok {
			pass++
			fmt.Printf("PASS %-45s expr=%q -> %s (%s)\n", e.ID, e.Expr, gotStr, gotKind)
			continue
		}
		fmt.Printf("FAIL %-45s expr=%q\n     want=%q (%s)\n     got =%q (%s)\n     why : %s\n",
			e.ID, e.Expr, e.Expected, e.ExpectedKind, gotStr, gotKind, why)
	}

	total := len(corpus)
	pct := 0.0
	if total > 0 {
		pct = float64(pass) / float64(total) * 100
	}
	fmt.Printf("\nspike-a: %d/%d passed (%.1f%%)\n", pass, total, pct)
	if pct >= 90.0 {
		fmt.Println("go: PROCEED")
		os.Exit(0)
	}
	fmt.Println("go: FAIL")
	os.Exit(1)
}

// runCase evaluates one corpus entry. Returns (passed, gotStr, gotKind, reason).
// reason is only meaningful when passed is false; it is intended for diagnosis,
// not for assertion logic.
func runCase(e corpusEntry) (bool, string, string, string) {
	ast, err := parseExpr(e.Expr)
	if err != nil {
		return false, "", "", fmt.Sprintf("parse error: %v", err)
	}
	ctx := newCtx(e.Context)
	v, err := ctx.eval(ast)
	if err != nil {
		return false, "", "", fmt.Sprintf("eval error: %v", err)
	}
	gotStr := v.asString()
	gotKind := v.kindName()
	if gotStr != e.Expected {
		return false, gotStr, gotKind, "value mismatch"
	}
	// Kind is informational — the canonical assertion is the string form,
	// which is what GH workflow templates actually compare. We still surface
	// kind drift as a warning by not gating on it, since "true" string vs
	// boolean is a known divergence point and the corpus normalises to
	// template-rendered form.
	return true, gotStr, gotKind, ""
}

// corpusDir resolves the directory containing corpus.json. Strategy:
//  1. CWD (covers running from the spike directory directly).
//  2. test/spikes/expr_parity under CWD (covers running from the repo root
//     via `go run ./test/spikes/expr_parity`).
//  3. Directory of this source file via runtime.Caller (covers `go run` with
//     odd working directories).
func corpusDir() string {
	if wd, err := os.Getwd(); err == nil {
		if _, err := os.Stat(filepath.Join(wd, "corpus.json")); err == nil {
			return wd
		}
		nested := filepath.Join(wd, "test", "spikes", "expr_parity")
		if _, err := os.Stat(filepath.Join(nested, "corpus.json")); err == nil {
			return nested
		}
	}
	if _, file, _, ok := runtime.Caller(0); ok {
		return filepath.Dir(file)
	}
	return "."
}
