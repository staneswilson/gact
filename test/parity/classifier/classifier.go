// Package classifier categorises textual diffs between a local `gact run`
// (or `gact lint`) output line and the corresponding line captured from a
// real GitHub Actions run via `gh run view --log`. It is the Spike B
// prototype called out in plan §7.10 / Task 0.20.
//
// The classifier is rule-based and stdlib-only. Each rule inspects a Diff
// and either claims it (returning a Result with a category and a reason
// token) or yields. Rules are evaluated in priority order:
//
//  1. Block rules — anything that materially changes the observable outcome
//     (exit code mismatch, missing output, missing/extra step, job verdict
//     flip). A single block-rule hit is sufficient to classify.
//  2. Noise rules — diffs that are demonstrably non-semantic: timestamps,
//     runner identifiers, randomised ports, ANSI escapes, line endings,
//     environment-dump key ordering.
//  3. Warn rules — stdout text drift when exit codes match.
//  4. Default — Warn. The conservative middle: anything we cannot explain
//     as noise and cannot prove is a block is reported to the human.
//
// Spike B's go criterion (plan §8) is ≥ 95% agreement with human
// judgement on a 5-workflow sample. This prototype provides the
// infrastructure; real workflow capture lands later when `gact run` exists
// and parity-harness Task 3.9 scales the corpus to 50.
//
// The Rules slice is exported so future work can prepend / append rules
// without changing the public Classify entry point. The variable is a
// package-level value (not a const) precisely so future tasks can
// register additional rules at init() time without touching this file.
package classifier

import (
	"regexp"
	"sort"
	"strings"
)

// Category is the classifier verdict. Ordering matches block > warn >
// noise so callers that need a "worst diff in a hunk" reduction can
// simply max() over a slice of Result.Category values.
type Category int

const (
	// CategoryNoise is for diffs that are demonstrably non-semantic.
	// They are stripped from parity reports and never escalate.
	CategoryNoise Category = iota
	// CategoryWarn is for diffs whose semantic significance is unclear.
	// They surface as PR comments but do not block a merge or open an
	// issue. This is also the default when no rule claims a diff.
	CategoryWarn
	// CategoryBlock is for diffs that materially change the observable
	// outcome of a workflow run. They open issues automatically.
	CategoryBlock
)

// String renders a Category as a lowercase token suitable for fixture
// comparisons and structured-log fields. Unknown categories render as
// "unknown" so a forgotten constant surfaces obviously rather than
// masquerading as a known value.
func (c Category) String() string {
	switch c {
	case CategoryNoise:
		return "noise"
	case CategoryWarn:
		return "warn"
	case CategoryBlock:
		return "block"
	default:
		// Defensive: a new constant added without updating String()
		// should be visible in logs, not silently rendered empty.
		return "unknown"
	}
}

// Diff is a single observation: one line (or short hunk) from the local
// run and the corresponding line from the GitHub run. The trailing
// fields are optional and let block-detecting rules see step-level
// signal that line-by-line text would otherwise miss.
//
// ExitCodeLocal and ExitCodeRemote default to 0 when omitted. We do not
// distinguish "unset" from "0" because the harness always populates
// them; a fixture that omits both fields is asserting an equal
// (success/success) exit-code pair.
//
// StepSkippedLocal and StepSkippedRemote let a fixture express "this
// step ran on one side and was skipped on the other" without needing
// to encode the skip marker into the Local/Remote text fields. A
// classifier rule promotes a skip mismatch to Block (plan §7.10).
//
// JobVerdictLocal and JobVerdictRemote may be "", "success", "failure",
// "skipped", or "cancelled". Empty strings mean "no signal" and are
// ignored by the rule that looks for verdict flips.
type Diff struct {
	Local  string
	Remote string

	ExitCodeLocal  int
	ExitCodeRemote int

	StepSkippedLocal  bool
	StepSkippedRemote bool

	JobVerdictLocal  string
	JobVerdictRemote string
}

// Result is the classifier's verdict for a single Diff. Reason is a
// short token (kebab-case) identifying the rule that fired, suitable
// for structured logging and for fixture assertions. Where a category
// is set by the fallback rather than a named rule, Reason is the
// special token "default-warn".
type Result struct {
	Category Category
	Reason   string
}

// Rule is the contract every classifier rule satisfies. Rules are pure
// functions of their input — no I/O, no shared state, no time-dependent
// behaviour — so the classifier itself can be exercised without
// fixtures and rules can be re-ordered or composed freely.
type Rule struct {
	// Name is a short kebab-case identifier used in logs and reasons.
	Name string
	// Category is the verdict the rule emits when it claims a Diff.
	// Storing this on the rule (rather than letting each rule pick a
	// category in its body) lets the priority ordering be enforced by
	// the Rules slice rather than by accident-of-implementation.
	Category Category
	// Match returns true iff this rule claims the diff. The reason
	// string in the eventual Result is the rule's Name; rules do not
	// fabricate their own reasons.
	Match func(d Diff) bool
}

// Rules is the ordered list of classifier rules. Block rules are first
// so a Diff with both a runner-id swap and an exit-code mismatch still
// classifies as Block. Within each category, more-specific rules come
// before more-general ones so that the emitted Reason is the most
// informative match.
//
// The slice is intentionally exported and mutable. Future work
// (Task 3.9) is expected to append workflow-specific rules; consumers
// are expected to do so at init() time before the first Classify call.
var Rules = []Rule{
	// Block rules — outcome-changing divergence.
	{Name: "exit-code-mismatch", Category: CategoryBlock, Match: ruleExitCodeMismatch},
	{Name: "job-verdict-flip", Category: CategoryBlock, Match: ruleJobVerdictFlip},
	{Name: "step-skipped-one-side", Category: CategoryBlock, Match: ruleStepSkippedOneSide},
	{Name: "output-value-mismatch", Category: CategoryBlock, Match: ruleOutputValueMismatch},

	// Noise rules — non-semantic divergence.
	{Name: "iso8601-timestamp", Category: CategoryNoise, Match: ruleISO8601Timestamp},
	{Name: "runner-name", Category: CategoryNoise, Match: ruleRunnerName},
	{Name: "runner-temp", Category: CategoryNoise, Match: ruleRunnerTemp},
	{Name: "random-port", Category: CategoryNoise, Match: ruleRandomPort},
	{Name: "ansi-escape", Category: CategoryNoise, Match: ruleANSIEscape},
	{Name: "line-ending", Category: CategoryNoise, Match: ruleLineEnding},
	{Name: "env-order", Category: CategoryNoise, Match: ruleEnvOrder},

	// Warn rules — stdout drift with matching exit codes. Must be
	// last because ruleStdoutTextDrift claims any unequal payload.
	{Name: "stdout-text-drift", Category: CategoryWarn, Match: ruleStdoutTextDrift},
}

// Classify walks the Rules slice in order and returns the first rule
// that claims the diff. If no rule fires, the diff is reported as
// Warn / "default-warn". This is the conservative middle (plan
// §7.10): we would rather surface an unfamiliar divergence as a PR
// comment than silently drop it as noise or auto-open an issue.
func Classify(d Diff) Result {
	for _, r := range Rules {
		if r.Match(d) {
			return Result{Category: r.Category, Reason: r.Name}
		}
	}
	return Result{Category: CategoryWarn, Reason: "default-warn"}
}

// ----------------------------------------------------------------------
// Block rules
// ----------------------------------------------------------------------

// ruleExitCodeMismatch fires when the local and remote runs reported
// different process-exit codes for the same step. This is the canonical
// "real divergence" — pass-on-one-side, fail-on-the-other — and is
// always a Block.
func ruleExitCodeMismatch(d Diff) bool {
	return d.ExitCodeLocal != d.ExitCodeRemote
}

// ruleJobVerdictFlip fires when GitHub reports one job verdict
// (success / failure) and the local run reports another. Empty strings
// (the zero value) mean "no signal" and are ignored — Diffs without
// verdict information go through the line-text rules instead.
func ruleJobVerdictFlip(d Diff) bool {
	if d.JobVerdictLocal == "" || d.JobVerdictRemote == "" {
		return false
	}
	return d.JobVerdictLocal != d.JobVerdictRemote
}

// ruleStepSkippedOneSide fires when one side skipped a step (e.g. due
// to an `if:` predicate evaluating differently) and the other ran it.
// This is plan §7.10's "step skipped on one side but not the other"
// case and is always a Block.
func ruleStepSkippedOneSide(d Diff) bool {
	return d.StepSkippedLocal != d.StepSkippedRemote
}

// outputValuePattern recognises a line of the form `<key>=<value>`
// emitted by GitHub Actions to mark a step output. The capture groups
// are (key, value). We intentionally require the line to have content
// on either side of the equals sign so that a stray `=` in a log line
// is not mistaken for a step output marker.
var outputValuePattern = regexp.MustCompile(`^([A-Za-z_][A-Za-z0-9_-]*)=(.*)$`)

// noiseKeyPrefixes is the set of `<key>=<value>` keys that the noise
// rules below claim. ruleOutputValueMismatch yields on those so that
// the noise rules — which know the keys' semantics — get to classify
// them instead.
var noiseKeyPrefixes = map[string]struct{}{
	"RUNNER_NAME":       {},
	"RUNNER_TEMP":       {},
	"RUNNER_TOOL_CACHE": {},
	"GITHUB_WORKSPACE":  {},
}

// ruleOutputValueMismatch fires when both lines look like step-output
// assignments (`name=value`) with the same key but a different value.
// A mismatched output value is always functionally meaningful; even if
// the exit codes match, the downstream consumer of the output behaves
// differently, and any downstream `if:` predicate may flip.
//
// Step-output lines are syntactically indistinguishable from other
// KEY=value emissions (RUNNER_NAME=..., env-dump tokens). To avoid
// stealing those cases — which the noise rules below classify as
// noise — this rule explicitly yields on keys in noiseKeyPrefixes.
// It also yields when either side has a semicolon (the env-dump
// separator), since that's clearly an env dump rather than a step
// output.
func ruleOutputValueMismatch(d Diff) bool {
	if strings.Contains(d.Local, envOrderSeparator) ||
		strings.Contains(d.Remote, envOrderSeparator) {
		return false
	}
	lm := outputValuePattern.FindStringSubmatch(d.Local)
	rm := outputValuePattern.FindStringSubmatch(d.Remote)
	if lm == nil || rm == nil {
		return false
	}
	if lm[1] != rm[1] {
		return false
	}
	if _, isNoise := noiseKeyPrefixes[lm[1]]; isNoise {
		return false
	}
	return lm[2] != rm[2]
}

// ----------------------------------------------------------------------
// Noise rules
// ----------------------------------------------------------------------

// iso8601Pattern recognises an ISO-8601 timestamp with optional
// fractional seconds and the GitHub-emitted "Z" suffix. GitHub
// log-line prefixes look like `2026-05-28T14:32:01.4567890Z`.
var iso8601Pattern = regexp.MustCompile(`\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(?:\.\d+)?Z`)

// ruleISO8601Timestamp fires when both lines contain an ISO-8601
// timestamp at the same position and the remainder of the line is
// identical after stripping the timestamp. Wall-clock drift between
// the local and GitHub runs is non-semantic by definition.
func ruleISO8601Timestamp(d Diff) bool {
	if !iso8601Pattern.MatchString(d.Local) || !iso8601Pattern.MatchString(d.Remote) {
		return false
	}
	return iso8601Pattern.ReplaceAllString(d.Local, "") ==
		iso8601Pattern.ReplaceAllString(d.Remote, "")
}

// runnerNamePattern recognises a `RUNNER_NAME=<anything>` assignment.
// The hosted-agent pool name has spaces and digits ("GitHub Actions 17")
// so we accept anything but a newline after the equals sign.
var runnerNamePattern = regexp.MustCompile(`^RUNNER_NAME=.*$`)

// ruleRunnerName fires when both lines are RUNNER_NAME assignments.
// The right-hand side is GitHub-pool-assigned remotely and host-name
// locally; the value is never functionally meaningful.
func ruleRunnerName(d Diff) bool {
	return runnerNamePattern.MatchString(d.Local) &&
		runnerNamePattern.MatchString(d.Remote)
}

// runnerTempPattern recognises a RUNNER_TEMP, RUNNER_TOOL_CACHE or
// GITHUB_WORKSPACE assignment. The right-hand side is a path that
// contains a randomly-generated component on at least one of the two
// runners and is therefore guaranteed to differ.
var runnerTempPattern = regexp.MustCompile(`^(?:RUNNER_TEMP|RUNNER_TOOL_CACHE|GITHUB_WORKSPACE)=.*$`)

// ruleRunnerTemp fires when both lines are assignments to one of the
// scratch-path env vars. The exact path value is never functionally
// significant — only its existence and its readability/writability.
func ruleRunnerTemp(d Diff) bool {
	return runnerTempPattern.MatchString(d.Local) &&
		runnerTempPattern.MatchString(d.Remote)
}

// randomPortPattern recognises a TCP/IP socket address with a high-
// numbered port (>= 1024). Service containers publish to a randomly-
// allocated port number, so any high-port-different/rest-equal line is
// almost certainly a Docker port-forward diff.
var randomPortPattern = regexp.MustCompile(`(\d{1,3}\.\d{1,3}\.\d{1,3}\.\d{1,3}):(\d{4,5})`)

// ruleRandomPort fires when both lines contain an IPv4:port pair, the
// IPs match, the ports differ, and the surrounding text is otherwise
// identical. This is the random-host-port case from plan §7.10.
//
// We deliberately require the IP to match: if a service migrates from
// 127.0.0.1 to a routable interface that IS semantic and should
// surface as a Warn.
func ruleRandomPort(d Diff) bool {
	lm := randomPortPattern.FindStringSubmatchIndex(d.Local)
	rm := randomPortPattern.FindStringSubmatchIndex(d.Remote)
	if lm == nil || rm == nil {
		return false
	}
	// Capture group 1 is the IP, group 2 is the port. The full match
	// is index pair (0,1); each capture is the next pair.
	localIP := d.Local[lm[2]:lm[3]]
	remoteIP := d.Remote[rm[2]:rm[3]]
	if localIP != remoteIP {
		return false
	}
	// Compare the lines with the full IP:port substring removed.
	localStripped := d.Local[:lm[0]] + d.Local[lm[1]:]
	remoteStripped := d.Remote[:rm[0]] + d.Remote[rm[1]:]
	return localStripped == remoteStripped
}

// ansiEscapePattern recognises a CSI escape sequence: ESC [ <params> <letter>.
// We accept the literal ESC byte (\x1b) and also the bracket-only form
// some shells emit when escapes are stripped of the leading ESC.
// The bracket-form is conservative: a colour code is the most common
// non-semantic source of "this line has weird characters" diffs.
var ansiEscapePattern = regexp.MustCompile(`\x1b\[[0-9;]*[A-Za-z]|\[\d{1,3}(?:;\d{1,3})*m`)

// ruleANSIEscape fires when one or both lines contain ANSI escapes
// and the lines are equal after stripping them. GitHub Actions emits
// colour codes by default; local non-TTY runs do not.
func ruleANSIEscape(d Diff) bool {
	if !ansiEscapePattern.MatchString(d.Local) && !ansiEscapePattern.MatchString(d.Remote) {
		return false
	}
	return ansiEscapePattern.ReplaceAllString(d.Local, "") ==
		ansiEscapePattern.ReplaceAllString(d.Remote, "")
}

// ruleLineEnding fires when the only difference between the lines is
// trailing whitespace (typically a stray \r from a Windows host). We
// strip ALL trailing whitespace, not just \r, because the same
// principle applies to a stray space at the end of a line.
func ruleLineEnding(d Diff) bool {
	l := strings.TrimRight(d.Local, "\r\n\t ")
	r := strings.TrimRight(d.Remote, "\r\n\t ")
	if l != r {
		return false
	}
	// Lines that are already exactly equal are not noise — they are
	// "no diff at all" and the harness should not have produced a
	// Diff record for them. Refusing to claim equal lines lets the
	// default-warn fallback flag the harness bug.
	return d.Local != d.Remote
}

// envOrderSeparator splits an environment-dump line into individual
// key=value assignments. The harness joins the dump with semicolons so
// the order is encoded as a list rather than a multi-line block.
const envOrderSeparator = ";"

// envAssignPattern is the "looks like an env assignment" predicate.
// Keys are ASCII letters / digits / underscore starting with a non-
// digit, as POSIX environment variables.
var envAssignPattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*=.*$`)

// ruleEnvOrder fires when both lines are semicolon-joined env dumps
// and the multiset of key=value pairs is equal. This is the
// `process.env` ordering case from plan §7.10 — Node iterates the
// environment in insertion order, which differs between the local
// shell and the GitHub runner image.
//
// We require ≥ 2 tokens on each side so that "FOO=1" vs "FOO=2" is
// not silently classified as an order swap of a one-element set.
func ruleEnvOrder(d Diff) bool {
	localTokens := splitEnvTokens(d.Local)
	remoteTokens := splitEnvTokens(d.Remote)
	if len(localTokens) < 2 || len(remoteTokens) < 2 {
		return false
	}
	if len(localTokens) != len(remoteTokens) {
		return false
	}
	// Every token must look like a valid KEY=VALUE assignment;
	// otherwise we are not looking at an env dump.
	for _, t := range localTokens {
		if !envAssignPattern.MatchString(t) {
			return false
		}
	}
	for _, t := range remoteTokens {
		if !envAssignPattern.MatchString(t) {
			return false
		}
	}
	// Sort and compare. If sorting and comparing equal, the
	// multisets match — i.e. the only difference is order.
	sort.Strings(localTokens)
	sort.Strings(remoteTokens)
	for i := range localTokens {
		if localTokens[i] != remoteTokens[i] {
			return false
		}
	}
	// Order must actually differ; identical lines are not noise.
	return d.Local != d.Remote
}

// splitEnvTokens splits an env-dump line on the documented separator
// and trims surrounding whitespace from each token. Empty tokens
// (caused by trailing separators) are dropped.
func splitEnvTokens(s string) []string {
	raw := strings.Split(s, envOrderSeparator)
	out := make([]string, 0, len(raw))
	for _, t := range raw {
		t = strings.TrimSpace(t)
		if t != "" {
			out = append(out, t)
		}
	}
	return out
}

// ----------------------------------------------------------------------
// Warn rules
// ----------------------------------------------------------------------

// ruleStdoutTextDrift is the catch-all warn rule. By the time we reach
// it, no block rule has fired (so exit codes match, no verdict flip,
// no skip flip, no step-output value mismatch on a non-noise key) and
// no noise rule has fired (so the divergence is not a known non-
// semantic source). The lines are different, which means stdout text
// drifted. Emit a Warn so a human can decide.
//
// Note this matches any two distinct strings where at least one side
// is non-empty, so it must be the LAST rule. The Rules slice ordering
// enforces that. If a future rule needs to claim a specific drift
// pattern (e.g. a known cosmetic banner change) it should be inserted
// before this rule in the Rules slice.
//
// Equal payloads return false so the harness's default-warn fallback
// can flag "this diff record should never have been emitted" as a
// harness bug instead of silently claiming it.
func ruleStdoutTextDrift(d Diff) bool {
	if d.Local == d.Remote {
		return false
	}
	return d.Local != "" || d.Remote != ""
}
