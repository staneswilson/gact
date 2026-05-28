package cmd

import (
	"regexp"
	"testing"
)

func TestVersion_PrintsVersionString(t *testing.T) {
	code, stdout, _ := runWith(t, "version")
	if code != 0 {
		t.Fatalf("expected exit code 0, got %d", code)
	}
	re := regexp.MustCompile(`^gact version \S+ \(commit \S+, built \S+\)\s*$`)
	if !re.MatchString(stdout) {
		t.Errorf("version output did not match expected format, got:\n%q", stdout)
	}
}

func TestVersion_HonoursPackageVariables(t *testing.T) {
	origVersion, origCommit, origBuildDate := Version, Commit, BuildDate
	t.Cleanup(func() {
		Version = origVersion
		Commit = origCommit
		BuildDate = origBuildDate
	})

	Version = "1.2.3"
	Commit = "abcdef0"
	BuildDate = "2026-05-28"

	code, stdout, _ := runWith(t, "version")
	if code != 0 {
		t.Fatalf("expected exit code 0, got %d", code)
	}
	want := "gact version 1.2.3 (commit abcdef0, built 2026-05-28)\n"
	if stdout != want {
		t.Errorf("expected stdout %q, got %q", want, stdout)
	}
}
