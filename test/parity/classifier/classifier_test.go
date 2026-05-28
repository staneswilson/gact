// Table-driven tests for the parity classifier (plan Task 0.20 / Spike B).
//
// Each fixture under fixtures/<category>/*.json is loaded, fed through
// Classify, and asserted to match its declared category and reason.
// Fixture-driven tests rather than literal tables keep the test file
// stable: adding a fixture file does not require touching this code.
//
// The fixture schema is documented inline on the fixture struct below.
// Hand-crafted fixtures will be re-captured against real workflows once
// `gact run` and `gh run view --log` land (plan Tasks 0.16-0.18); the
// classifier infrastructure itself is production-ready as soon as the
// rules pass.

package classifier

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fixture is the on-disk representation of a single classifier test
// case. The JSON shape is deliberately stable so that adding a new
// fixture is a pure data change. Field-level documentation:
//
//   - Name: short kebab-case label used in the test report. Distinct
//     from the filename so a fixture can be renamed without breaking
//     other fixtures that reference it.
//   - Comment: human-readable note about WHERE the diff comes from in
//     real workflows. Useful when reviewing fixtures months from now.
//   - Local / Remote: the two text payloads the classifier compares.
//   - ExitCodeLocal / ExitCodeRemote: optional. Default to 0 / 0.
//   - StepSkippedLocal / StepSkippedRemote: optional. Default to false.
//   - JobVerdictLocal / JobVerdictRemote: optional. Default to "".
//   - ExpectedCategory: one of "noise", "warn", "block".
//   - ExpectedReason: substring that must appear in Result.Reason.
//     Using a substring (not equality) lets a more-specific rule add
//     a suffix without breaking the fixture.
type fixture struct {
	Name              string `json:"name"`
	Comment           string `json:"comment,omitempty"`
	Local             string `json:"local"`
	Remote            string `json:"remote"`
	ExitCodeLocal     int    `json:"exit_code_local,omitempty"`
	ExitCodeRemote    int    `json:"exit_code_remote,omitempty"`
	StepSkippedLocal  bool   `json:"step_skipped_local,omitempty"`
	StepSkippedRemote bool   `json:"step_skipped_remote,omitempty"`
	JobVerdictLocal   string `json:"job_verdict_local,omitempty"`
	JobVerdictRemote  string `json:"job_verdict_remote,omitempty"`
	ExpectedCategory  string `json:"expected_category"`
	ExpectedReason    string `json:"expected_reason"`
}

// loadFixtures reads every *.json file under fixtures/<dir>/ and
// returns the decoded slice. A read or decode failure aborts the test
// via t.Fatalf — fixture files are project-controlled so any failure
// is a test-suite bug, not a runtime condition.
func loadFixtures(t *testing.T, dir string) []fixture {
	t.Helper()
	matches, err := filepath.Glob(filepath.Join("fixtures", dir, "*.json"))
	if err != nil {
		t.Fatalf("glob fixtures/%s: %v", dir, err)
	}
	if len(matches) == 0 {
		t.Fatalf("no fixtures under fixtures/%s", dir)
	}
	out := make([]fixture, 0, len(matches))
	for _, path := range matches {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		var f fixture
		if err := json.Unmarshal(data, &f); err != nil {
			t.Fatalf("decode %s: %v", path, err)
		}
		// Re-use the on-disk filename as a fallback label when the
		// fixture's name field is empty. This keeps the test report
		// readable even for half-written fixtures.
		if f.Name == "" {
			f.Name = strings.TrimSuffix(filepath.Base(path), ".json")
		}
		out = append(out, f)
	}
	return out
}

// runCategoryFixtures executes the Classify call for every fixture in
// fixtures/<category>/ and asserts both the category and the reason
// substring match. Errors are reported per-fixture via t.Errorf so
// that one bad fixture does not mask the status of the others.
func runCategoryFixtures(t *testing.T, category string) {
	t.Helper()
	fixtures := loadFixtures(t, category)
	for _, f := range fixtures {
		f := f // pin loop var for t.Run closure
		t.Run(f.Name, func(t *testing.T) {
			d := Diff{
				Local:             f.Local,
				Remote:            f.Remote,
				ExitCodeLocal:     f.ExitCodeLocal,
				ExitCodeRemote:    f.ExitCodeRemote,
				StepSkippedLocal:  f.StepSkippedLocal,
				StepSkippedRemote: f.StepSkippedRemote,
				JobVerdictLocal:   f.JobVerdictLocal,
				JobVerdictRemote:  f.JobVerdictRemote,
			}
			got := Classify(d)
			if got.Category.String() != f.ExpectedCategory {
				t.Errorf("category mismatch: want %q, got %q (reason=%q)",
					f.ExpectedCategory, got.Category.String(), got.Reason)
			}
			if !strings.Contains(got.Reason, f.ExpectedReason) {
				t.Errorf("reason mismatch: want substring %q, got %q",
					f.ExpectedReason, got.Reason)
			}
		})
	}
}

// TestClassifier_Noise exercises every fixture under fixtures/noise/.
// Each fixture is expected to classify as CategoryNoise.
func TestClassifier_Noise(t *testing.T) {
	runCategoryFixtures(t, "noise")
}

// TestClassifier_Warn exercises every fixture under fixtures/warn/.
// Each fixture is expected to classify as CategoryWarn. The
// default-warn fallback is also exercised here.
func TestClassifier_Warn(t *testing.T) {
	runCategoryFixtures(t, "warn")
}

// TestClassifier_Block exercises every fixture under fixtures/block/.
// Each fixture is expected to classify as CategoryBlock and should
// take priority over any incidental noise/warn signal present in the
// same Diff (block rules run first per plan §7.10).
func TestClassifier_Block(t *testing.T) {
	runCategoryFixtures(t, "block")
}

// TestClassifier_DefaultIsWarn pins the explicit default: an arbitrary
// non-empty diff that no rule claims falls through to default-warn.
// This is the conservative-middle behaviour from plan §7.10; if it
// ever changes (e.g. to default-noise or default-block) the change is
// load-bearing and we want it to break a known test, not surprise a
// user months later.
func TestClassifier_DefaultIsWarn(t *testing.T) {
	d := Diff{
		Local:  "some-unfamiliar-output: 42",
		Remote: "some-unfamiliar-output: 43",
	}
	got := Classify(d)
	if got.Category != CategoryWarn {
		t.Errorf("default category: want %v, got %v", CategoryWarn, got.Category)
	}
	// The default reason MAY be "default-warn" OR a rule reason if a
	// new rule lands that legitimately claims this diff. Either is
	// acceptable; what we are pinning here is the category, not the
	// reason. A separate test below pins default-warn for the case
	// where no rule fires at all.
}

// TestClassifier_DefaultWarnReason pins the explicit default reason
// for the case where no rule fires. We construct a Diff that no
// current rule can claim by giving it two empty-after-trim payloads
// and equal exit codes — equal lines, equal exit codes, no verdict
// signal. The line-ending and stdout-drift rules both refuse to claim
// "Local == Remote" inputs, so this lands on the fallback.
func TestClassifier_DefaultWarnReason(t *testing.T) {
	d := Diff{Local: "identical", Remote: "identical"}
	got := Classify(d)
	if got.Reason != "default-warn" {
		t.Errorf("default reason: want %q, got %q", "default-warn", got.Reason)
	}
	if got.Category != CategoryWarn {
		t.Errorf("default category: want %v, got %v", CategoryWarn, got.Category)
	}
}

// TestCategory_String exercises the Category.String mapping. The set
// is small enough that a literal table is the clearest expression.
func TestCategory_String(t *testing.T) {
	cases := map[Category]string{
		CategoryNoise: "noise",
		CategoryWarn:  "warn",
		CategoryBlock: "block",
		Category(99):  "unknown",
	}
	for c, want := range cases {
		if got := c.String(); got != want {
			t.Errorf("Category(%d).String() = %q, want %q", int(c), got, want)
		}
	}
}
