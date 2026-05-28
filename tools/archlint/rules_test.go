package main

import (
	"strings"
	"testing"
)

func TestRule_PkgCannotImportInternal_FlagsViolation(t *testing.T) {
	code := `package workflow

import _ "github.com/staneswilson/gact/internal/parser"
`
	violations := lintFile("pkg/workflow/x.go", []byte(code), pkgCannotImportInternal)
	if len(violations) != 1 {
		t.Fatalf("expected 1 violation, got %d: %v", len(violations), violations)
	}
	if !strings.Contains(violations[0], "pkg may not import internal") {
		t.Fatalf("expected message to mention pkg/internal boundary, got %q", violations[0])
	}
	if !strings.Contains(violations[0], "github.com/staneswilson/gact/internal/parser") {
		t.Fatalf("expected message to name the offending import, got %q", violations[0])
	}
}

func TestRule_PkgCannotImportInternal_AllowsInternalImportingInternal(t *testing.T) {
	// A file already inside /internal/ may freely import other /internal/
	// packages; the boundary only protects /pkg/.
	code := `package parser

import _ "github.com/staneswilson/gact/internal/expr"
`
	violations := lintFile("internal/parser/x.go", []byte(code), pkgCannotImportInternal)
	if len(violations) != 0 {
		t.Fatalf("expected 0 violations, got %d: %v", len(violations), violations)
	}
}

func TestRule_PkgCannotImportInternal_AllowsPkgImportingPkg(t *testing.T) {
	// /pkg/ -> /pkg/ is always fine: that is the entire point of pkg/.
	code := `package workflow

import _ "github.com/staneswilson/gact/pkg/ir"
`
	violations := lintFile("pkg/workflow/x.go", []byte(code), pkgCannotImportInternal)
	if len(violations) != 0 {
		t.Fatalf("expected 0 violations, got %d: %v", len(violations), violations)
	}
}

func TestRule_PkgCannotImportInternal_HonoursWindowsPathSeparator(t *testing.T) {
	// Paths from filepath.Walk on Windows use backslashes; the rule must
	// still recognise /pkg/ segments.
	code := `package workflow

import _ "github.com/staneswilson/gact/internal/parser"
`
	violations := lintFile(`pkg\workflow\x.go`, []byte(code), pkgCannotImportInternal)
	if len(violations) != 1 {
		t.Fatalf("expected 1 violation on windows-style path, got %d: %v", len(violations), violations)
	}
}

func TestRule_DomainCannotImportOsExec(t *testing.T) {
	tests := []struct {
		name       string
		path       string
		code       string
		wantViols  int
		wantSubstr string
	}{
		{
			name: "authoring importing os/exec is a violation",
			path: "internal/authoring/x.go",
			code: `package authoring

import _ "os/exec"
`,
			wantViols:  1,
			wantSubstr: "os/exec",
		},
		{
			name: "scheduling importing net/http is a violation",
			path: "internal/scheduling/x.go",
			code: `package scheduling

import _ "net/http"
`,
			wantViols:  1,
			wantSubstr: "net/http",
		},
		{
			name: "selection importing os/exec is a violation",
			path: "internal/selection/x.go",
			code: `package selection

import _ "os/exec"
`,
			wantViols:  1,
			wantSubstr: "os/exec",
		},
		{
			name: "adapter under internal/adapters/proc may import os/exec",
			path: "internal/adapters/proc/x.go",
			code: `package proc

import _ "os/exec"
`,
			wantViols: 0,
		},
		{
			name: "authoring importing a benign stdlib package is fine",
			path: "internal/authoring/x.go",
			code: `package authoring

import _ "fmt"
`,
			wantViols: 0,
		},
		{
			name: "windows path separators still flag domain violations",
			path: `internal\authoring\x.go`,
			code: `package authoring

import _ "os/exec"
`,
			wantViols:  1,
			wantSubstr: "os/exec",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			violations := lintFile(tc.path, []byte(tc.code), domainCannotImportOsExec)
			if len(violations) != tc.wantViols {
				t.Fatalf("expected %d violations, got %d: %v", tc.wantViols, len(violations), violations)
			}
			if tc.wantViols > 0 && !strings.Contains(violations[0], tc.wantSubstr) {
				t.Fatalf("expected violation to mention %q, got %q", tc.wantSubstr, violations[0])
			}
		})
	}
}

func TestLintFile_MalformedSourceReportsParseError(t *testing.T) {
	// A malformed source file must not panic; it must surface a clear
	// "parse error" message via the same violations channel.
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("lintFile panicked on malformed input: %v", r)
		}
	}()
	bad := []byte("package !!!not-valid-go\n")
	violations := lintFile("pkg/workflow/broken.go", bad, pkgCannotImportInternal)
	if len(violations) == 0 {
		t.Fatalf("expected a parse-error violation, got none")
	}
	if !strings.Contains(violations[0], "parse error") {
		t.Fatalf("expected message to mention parse error, got %q", violations[0])
	}
}
