// Package main implements archlint, a custom architectural-rule linter.
//
// The linter walks Go source files and flags violations of project-specific
// boundary rules that golangci-lint cannot express. See §0.2 of the
// implementation plan for the rationale.
package main

import (
	"fmt"
	"go/parser"
	"go/token"
	"strings"
)

// Rule reports violations for a single source file given its path and the
// list of imported package paths. The token.FileSet is supplied for rules
// that need positional information; rules that do not are free to ignore it.
type Rule func(path string, fset *token.FileSet, imports []string) []string

// lintFile parses src as a Go source file (imports only) and runs r against
// it. The returned slice is empty when there are no violations. A parse
// error is itself reported as a single violation so that callers do not
// have to distinguish parse failures from rule failures; this keeps the
// CLI driver loop trivial.
func lintFile(path string, src []byte, r Rule) []string {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, path, src, parser.ImportsOnly)
	if err != nil {
		return []string{fmt.Sprintf("parse error: %s", err.Error())}
	}
	imports := make([]string, 0, len(f.Imports))
	for _, imp := range f.Imports {
		imports = append(imports, strings.Trim(imp.Path.Value, `"`))
	}
	return r(path, fset, imports)
}

// pkgCannotImportInternal enforces the pkg/ semver-stable boundary: any file
// under a pkg/ directory must not import a sibling internal/ tree, because
// internal types are not part of the public API surface and may change.
func pkgCannotImportInternal(path string, _ *token.FileSet, imports []string) []string {
	slashed := filepathToSlash(path)
	if !strings.HasPrefix(slashed, "pkg/") && !strings.Contains(slashed, "/pkg/") {
		return nil
	}
	var v []string
	for _, imp := range imports {
		if strings.Contains(imp, "/internal/") {
			v = append(v, "pkg may not import internal: "+imp)
		}
	}
	return v
}

// domainPrefixes is the set of bounded contexts whose purity is enforced by
// domainCannotImportOsExec. Domain code must be deterministic, fast, and
// side-effect free; talking to the OS or the network belongs in an adapter.
var domainPrefixes = []string{
	"internal/authoring/",
	"internal/scheduling/",
	"internal/selection/",
}

// forbiddenDomainImports is the set of stdlib packages that pure domain
// packages must never import. os/exec subprocesses out; net/http opens a
// socket. Both indicate that logic has leaked out of an adapter.
var forbiddenDomainImports = []string{
	"os/exec",
	"net/http",
}

// domainCannotImportOsExec enforces domain purity: files under any of the
// three pure-domain bounded contexts (authoring, scheduling, selection)
// must not import os/exec or net/http. Adapters under internal/adapters/
// are unaffected because that is exactly where such side-effects belong.
func domainCannotImportOsExec(path string, _ *token.FileSet, imports []string) []string {
	slashed := filepathToSlash(path)
	var matched bool
	for _, prefix := range domainPrefixes {
		if strings.Contains(slashed, prefix) {
			matched = true
			break
		}
	}
	if !matched {
		return nil
	}
	var v []string
	for _, imp := range imports {
		for _, banned := range forbiddenDomainImports {
			if imp == banned {
				v = append(v, "domain may not import "+banned)
			}
		}
	}
	return v
}

// filepathToSlash normalises path separators so that rules can match on
// "/pkg/" regardless of host OS. We avoid importing path/filepath at the
// rule layer to keep rules pure functions of their inputs.
func filepathToSlash(p string) string {
	return strings.ReplaceAll(p, `\`, "/")
}
