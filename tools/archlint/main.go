// archlint is the gact project's custom architectural-rule linter.
//
// It walks one or more Go package patterns (e.g. "./...") and runs each
// rule against every non-test Go source file it finds. Violations are
// printed to stderr in the form "path: rule: message" and a non-zero exit
// status is returned if any rule fires.
//
// Usage:
//
//	go run ./tools/archlint ./...
//
// Stdlib only.
package main

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// registeredRules is the ordered list of rules archlint runs. New rules
// should be appended here once they exist as exported functions in
// rules.go and have their own unit tests.
var registeredRules = []struct {
	name string
	fn   Rule
}{
	{name: "pkgCannotImportInternal", fn: pkgCannotImportInternal},
	{name: "domainCannotImportOsExec", fn: domainCannotImportOsExec},
}

func main() {
	patterns := os.Args[1:]
	if len(patterns) == 0 {
		patterns = []string{"./..."}
	}

	violations, err := run(patterns)
	if err != nil {
		fmt.Fprintln(os.Stderr, "archlint:", err)
		os.Exit(2)
	}
	for _, v := range violations {
		fmt.Fprintln(os.Stderr, v)
	}
	if len(violations) > 0 {
		os.Exit(1)
	}
}

// run resolves each pattern to a set of Go files, lints each file with
// every registered rule, and returns the formatted violations.
func run(patterns []string) ([]string, error) {
	files, err := resolveFiles(patterns)
	if err != nil {
		return nil, err
	}

	var out []string
	for _, path := range files {
		src, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", path, err)
		}
		for _, r := range registeredRules {
			for _, msg := range lintFile(path, src, r.fn) {
				out = append(out, fmt.Sprintf("%s: %s: %s", path, r.name, msg))
			}
		}
	}
	return out, nil
}

// resolveFiles expands the given patterns into a sorted, deduplicated list
// of .go file paths. The only pattern shape supported today is the Go
// convention "<dir>/..." (recursive) plus plain directory paths; that is
// enough for CI usage. Test files (_test.go) and vendored / hidden
// directories are skipped.
func resolveFiles(patterns []string) ([]string, error) {
	seen := make(map[string]struct{})
	var files []string

	for _, p := range patterns {
		recursive := strings.HasSuffix(p, "/...")
		root := strings.TrimSuffix(p, "/...")
		if root == "" || root == "." {
			root = "."
		}
		root = filepath.Clean(root)

		info, err := os.Stat(root)
		if err != nil {
			return nil, fmt.Errorf("stat %s: %w", root, err)
		}
		if !info.IsDir() {
			return nil, fmt.Errorf("%s is not a directory", root)
		}

		walkErr := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				if shouldSkipDir(root, path, recursive) {
					return filepath.SkipDir
				}
				return nil
			}
			if !strings.HasSuffix(path, ".go") {
				return nil
			}
			if strings.HasSuffix(path, "_test.go") {
				return nil
			}
			if _, dup := seen[path]; dup {
				return nil
			}
			seen[path] = struct{}{}
			files = append(files, path)
			return nil
		})
		if walkErr != nil {
			return nil, walkErr
		}
	}
	return files, nil
}

// shouldSkipDir filters out directories we never want to descend into:
// hidden dirs (.git, .github), vendor/, testdata/, and — when the pattern
// is not recursive — any subdirectory below the root.
func shouldSkipDir(root, path string, recursive bool) bool {
	if path == root {
		return false
	}
	base := filepath.Base(path)
	if base == "vendor" || base == "testdata" || base == "node_modules" {
		return true
	}
	if strings.HasPrefix(base, ".") {
		return true
	}
	if !recursive {
		return true
	}
	return false
}
