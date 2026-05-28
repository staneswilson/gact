package cmd

import (
	"bytes"
	"strings"
	"testing"
)

// runWith is a thin wrapper to drive the root command from a test with
// captured streams. It returns the exit code returned by ExecuteWith and the
// contents of stdout / stderr written by cobra during the run.
func runWith(t *testing.T, args ...string) (int, string, string) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	code := ExecuteWith(args, &stdout, &stderr)
	return code, stdout.String(), stderr.String()
}

func TestRoot_HelpFlag_PrintsUsage(t *testing.T) {
	code, stdout, _ := runWith(t, "--help")
	if code != 0 {
		t.Fatalf("expected exit code 0, got %d", code)
	}
	if !strings.Contains(stdout, "runs GitHub Actions workflows locally") {
		t.Errorf("expected help output to contain workflow description, got:\n%s", stdout)
	}
	if !strings.Contains(stdout, "Usage:") {
		t.Errorf("expected help output to contain 'Usage:', got:\n%s", stdout)
	}
}

func TestRoot_NoArgs_PrintsHelp(t *testing.T) {
	code, stdout, _ := runWith(t)
	if code != 0 {
		t.Fatalf("expected exit code 0 when invoked with no args, got %d", code)
	}
	if !strings.Contains(stdout, "Usage:") {
		t.Errorf("expected default no-args output to contain 'Usage:', got:\n%s", stdout)
	}
}

func TestRoot_UnknownCommand_ReturnsNonZero(t *testing.T) {
	code, _, stderr := runWith(t, "bogus")
	if code == 0 {
		t.Fatalf("expected non-zero exit code for unknown command, got 0")
	}
	if !strings.Contains(strings.ToLower(stderr), "unknown command") {
		t.Errorf("expected stderr to mention 'unknown command', got:\n%s", stderr)
	}
}
