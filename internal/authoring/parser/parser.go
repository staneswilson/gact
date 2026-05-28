// Package parser translates a GitHub Actions workflow YAML document
// into the pkg/workflow IR.
//
// Design notes:
//
//   - The parser drives yaml.v3 in *node* mode rather than reflective
//     decode. Each YAML node carries Line/Column information that we lift
//     into wf.SourceSpan values; the decode functions walk the tree
//     explicitly so that every IR value can be paired with a position.
//
//   - Errors are typed (see Error) and carry a span so callers can pipe
//     them straight into the diagnostics framework.
//
//   - This package is pure domain: no os/exec, no net/http. The only
//     third-party dependency is gopkg.in/yaml.v3, which is required to do
//     anything useful with YAML and is the same library used by every
//     mainstream Go YAML parser.
//
//   - The parser is *forgiving*: unknown top-level keys (such as
//     `permissions`) are stored in Triggers.Other-style fallbacks where
//     appropriate or simply ignored where the IR does not yet model them.
//     We do not validate the workflow here — schema validation is a
//     separate concern handled by internal/authoring/schema.

package parser

import (
	"fmt"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"

	wf "github.com/staneswilson/gact/pkg/workflow"
)

// Error is the typed error returned by Parse. It pairs a human-readable
// message with a SourceSpan so diagnostics can underline the offending
// region. The zero value is intentionally invalid — callers should always
// construct via newError so the path/line/col come from a yaml.Node.
type Error struct {
	// Message is the user-facing description, e.g. "workflow root must be a
	// mapping".
	Message string
	// Span points at the offending node. For wrapped yaml.v3 errors the
	// span carries the line/column reported by the underlying parser.
	Span wf.SourceSpan
	// Cause optionally wraps a lower-level error (for instance the
	// *yaml.TypeError returned by yaml.Unmarshal).
	Cause error
}

// Error renders the Error in path:line:col: message form so that grep-able
// CLI output and IDE diagnostics agree.
func (e *Error) Error() string {
	if e == nil {
		return ""
	}
	prefix := e.Span.String()
	if prefix == ":0:0" {
		return e.Message
	}
	return prefix + ": " + e.Message
}

// Unwrap exposes the wrapped cause to errors.Is / errors.As.
func (e *Error) Unwrap() error { return e.Cause }

// newError is the single-canonical constructor for *Error. Using a
// constructor here means every parser error funnels through one place,
// which makes it easy to change formatting later without rewriting call
// sites.
func newError(path string, n *yaml.Node, msg string, cause error) *Error {
	return &Error{
		Message: msg,
		Span:    span(path, n),
		Cause:   cause,
	}
}

// Parse unmarshals src into a Workflow IR and returns it.
//
// path is the source filename (used as Span.Path on every span returned).
// It is not opened — callers are expected to read the file separately so
// that this package stays pure and can be used in-memory by editors and
// LSP servers.
//
// Errors returned are *Error so callers can extract the span via
// errors.As(err, &e). yaml.v3 errors (malformed indentation, invalid
// scalars, etc.) are wrapped in *Error and given the line/column the
// underlying parser reported.
func Parse(path string, src []byte) (wf.Workflow, error) {
	var root yaml.Node
	if err := yaml.Unmarshal(src, &root); err != nil {
		// yaml.v3 reports errors in the form "yaml: line N: message".
		// Extract the line number if present so downstream consumers can
		// jump to the right location. Column information is generally not
		// surfaced by yaml.v3 on syntax errors, so we use column 1.
		line := extractYAMLErrorLine(err)
		return wf.Workflow{}, &Error{
			Message: err.Error(),
			Span:    wf.SourceSpan{Path: path, Line: line, Column: 1, EndLine: line, EndCol: 1},
			Cause:   err,
		}
	}
	// A successful Unmarshal on empty input yields a zero Node with
	// Kind == 0. Surface that as a clean error rather than panicking on
	// nil Content access downstream.
	if root.Kind == 0 {
		return wf.Workflow{Path: path}, &Error{
			Message: "empty workflow document",
			Span:    wf.SourceSpan{Path: path, Line: 1, Column: 1, EndLine: 1, EndCol: 1},
		}
	}
	doc := unwrapDocument(&root)
	if doc == nil {
		return wf.Workflow{Path: path}, &Error{
			Message: "empty workflow document",
			Span:    wf.SourceSpan{Path: path, Line: 1, Column: 1, EndLine: 1, EndCol: 1},
		}
	}
	if doc.Kind != yaml.MappingNode {
		return wf.Workflow{Path: path}, newError(path, doc, "workflow root must be a mapping", nil)
	}
	return decodeWorkflow(path, doc)
}

// unwrapDocument returns the single content node of a DocumentNode, or n
// itself if it is already the document body. yaml.Unmarshal always
// returns a DocumentNode wrapper, but defensive code is cheap and means
// callers can hand us either form.
func unwrapDocument(n *yaml.Node) *yaml.Node {
	if n == nil {
		return nil
	}
	if n.Kind == yaml.DocumentNode {
		if len(n.Content) == 0 {
			return nil
		}
		return n.Content[0]
	}
	return n
}

// extractYAMLErrorLine pulls the "yaml: line N:" prefix out of a yaml.v3
// error message. Returning 0 if no prefix is present is fine because the
// caller renders a zero-line span as ":0:1" — still better than no
// position at all.
func extractYAMLErrorLine(err error) int {
	if err == nil {
		return 0
	}
	msg := err.Error()
	// yaml.v3 formats: "yaml: line N: message" or "yaml: unmarshal errors: ..."
	const prefix = "yaml: line "
	i := strings.Index(msg, prefix)
	if i < 0 {
		return 0
	}
	rest := msg[i+len(prefix):]
	j := strings.IndexByte(rest, ':')
	if j < 0 {
		return 0
	}
	n, convErr := strconv.Atoi(strings.TrimSpace(rest[:j]))
	if convErr != nil {
		return 0
	}
	return n
}

// decodeWorkflow turns a YAML mapping node (the document root) into a
// fully populated Workflow. Each top-level key delegates to a focused
// helper so the dispatch table here reads as a schema overview.
func decodeWorkflow(path string, root *yaml.Node) (wf.Workflow, error) {
	w := wf.Workflow{
		Path: path,
		Span: span(path, root),
	}

	if _, v, ok := findChild(root, "name"); ok {
		if v.Kind == yaml.ScalarNode {
			w.Name = v.Value
		}
	}
	if _, v, ok := findChild(root, "on"); ok {
		t, err := decodeTriggers(path, v)
		if err != nil {
			return wf.Workflow{}, err
		}
		w.Triggers = t
	}
	if _, v, ok := findChild(root, "env"); ok {
		w.Env = decodeStringMap(v)
	}
	if _, v, ok := findChild(root, "defaults"); ok {
		w.Defaults = decodeDefaults(v)
	}
	if _, v, ok := findChild(root, "jobs"); ok {
		jobs, err := decodeJobs(path, v)
		if err != nil {
			return wf.Workflow{}, err
		}
		w.JobsByID = jobs
	}

	return w, nil
}

// decodeTriggers decodes the `on:` value, which may legitimately be a
// scalar, a sequence, or a mapping. Each form is handled with a
// dedicated branch so the contract is obvious to readers.
//
// Behaviour:
//   - Scalar form (`on: push`): the named trigger is marked present with
//     empty filters.
//   - Sequence form (`on: [push, pull_request]`): each named trigger is
//     marked present with empty filters.
//   - Mapping form (`on: { push: { branches: [main] } }`): each entry is
//     decoded by name; unknown events are recorded on Triggers.Other.
//
// YAML's bareword `on` parses to true on round-trip for some
// implementations, but yaml.v3 preserves the literal scalar "on" as the
// mapping key once we go through Node mode. We rely on that here.
func decodeTriggers(path string, n *yaml.Node) (wf.Triggers, error) {
	if n == nil {
		return wf.Triggers{}, nil
	}
	switch n.Kind {
	case yaml.ScalarNode:
		var t wf.Triggers
		applyTriggerName(&t, n.Value)
		return t, nil
	case yaml.SequenceNode:
		var t wf.Triggers
		for _, item := range n.Content {
			if item.Kind == yaml.ScalarNode {
				applyTriggerName(&t, item.Value)
			}
		}
		return t, nil
	case yaml.MappingNode:
		return decodeTriggersMapping(path, n)
	default:
		return wf.Triggers{}, newError(path, n, "`on:` must be a scalar, sequence, or mapping", nil)
	}
}

// applyTriggerName is the shared body of the scalar and sequence forms.
// It assigns an empty filter object to the named trigger so downstream
// consumers can rely on the "non-nil pointer ⇒ event is selected"
// invariant documented on pkg/workflow.Triggers.
func applyTriggerName(t *wf.Triggers, name string) {
	switch name {
	case "push":
		t.Push = &wf.PushTrigger{}
	case "pull_request":
		t.PullRequest = &wf.PullRequestTrigger{}
	case "pull_request_target":
		t.PullRequestTarget = &wf.PullRequestTrigger{}
	case "workflow_dispatch":
		t.WorkflowDispatch = &wf.WorkflowDispatchTrigger{}
	case "workflow_call":
		t.WorkflowCall = &wf.WorkflowCallTrigger{}
	case "workflow_run":
		t.WorkflowRun = &wf.WorkflowRunTrigger{}
	case "schedule":
		// schedule with no body is unusual but we tolerate it by leaving
		// the slice empty rather than introducing a zero-length sentinel.
	default:
		t.Other = append(t.Other, name)
	}
}

// decodeTriggersMapping decodes the mapping form of `on:`. Each
// known event has its own dedicated sub-decoder; unknown events are
// recorded on Triggers.Other so downstream code can still detect them.
func decodeTriggersMapping(path string, n *yaml.Node) (wf.Triggers, error) {
	var t wf.Triggers
	for i := 0; i+1 < len(n.Content); i += 2 {
		k := n.Content[i]
		v := n.Content[i+1]
		if k.Kind != yaml.ScalarNode {
			continue
		}
		switch k.Value {
		case "push":
			t.Push = decodePushTrigger(v)
		case "pull_request":
			t.PullRequest = decodePullRequestTrigger(v)
		case "pull_request_target":
			t.PullRequestTarget = decodePullRequestTrigger(v)
		case "schedule":
			t.Schedule = decodeScheduleTriggers(v)
		case "workflow_dispatch":
			t.WorkflowDispatch = decodeWorkflowDispatch(v)
		case "workflow_call":
			t.WorkflowCall = decodeWorkflowCall(path, v)
		case "workflow_run":
			t.WorkflowRun = decodeWorkflowRun(v)
		default:
			t.Other = append(t.Other, k.Value)
		}
	}
	return t, nil
}

// decodePushTrigger reads the filter mapping under `on.push:`. A nil or
// non-mapping value still yields a present-but-empty trigger; that mirrors
// `on: push` and matches the pkg/workflow Triggers contract.
func decodePushTrigger(n *yaml.Node) *wf.PushTrigger {
	tr := &wf.PushTrigger{}
	if n == nil || n.Kind != yaml.MappingNode {
		return tr
	}
	if _, v, ok := findChild(n, "branches"); ok {
		tr.Branches = decodeStringList(v)
	}
	if _, v, ok := findChild(n, "branches-ignore"); ok {
		tr.BranchesIgnore = decodeStringList(v)
	}
	if _, v, ok := findChild(n, "tags"); ok {
		tr.Tags = decodeStringList(v)
	}
	if _, v, ok := findChild(n, "tags-ignore"); ok {
		tr.TagsIgnore = decodeStringList(v)
	}
	if _, v, ok := findChild(n, "paths"); ok {
		tr.Paths = decodeStringList(v)
	}
	if _, v, ok := findChild(n, "paths-ignore"); ok {
		tr.PathsIgnore = decodeStringList(v)
	}
	return tr
}

// decodePullRequestTrigger is shared by pull_request and
// pull_request_target — they accept the same filter shape, only the event
// semantics differ at runtime.
func decodePullRequestTrigger(n *yaml.Node) *wf.PullRequestTrigger {
	tr := &wf.PullRequestTrigger{}
	if n == nil || n.Kind != yaml.MappingNode {
		return tr
	}
	if _, v, ok := findChild(n, "types"); ok {
		tr.Types = decodeStringList(v)
	}
	if _, v, ok := findChild(n, "branches"); ok {
		tr.Branches = decodeStringList(v)
	}
	if _, v, ok := findChild(n, "branches-ignore"); ok {
		tr.BranchesIgnore = decodeStringList(v)
	}
	if _, v, ok := findChild(n, "paths"); ok {
		tr.Paths = decodeStringList(v)
	}
	if _, v, ok := findChild(n, "paths-ignore"); ok {
		tr.PathsIgnore = decodeStringList(v)
	}
	return tr
}

// decodeScheduleTriggers decodes `on.schedule:` — a sequence of mappings,
// each containing a `cron:` field. Entries without a cron value are
// dropped silently; schema validation will surface the issue if it
// matters to the caller.
func decodeScheduleTriggers(n *yaml.Node) []wf.ScheduleTrigger {
	if n == nil || n.Kind != yaml.SequenceNode {
		return nil
	}
	out := make([]wf.ScheduleTrigger, 0, len(n.Content))
	for _, item := range n.Content {
		if item.Kind != yaml.MappingNode {
			continue
		}
		if _, v, ok := findChild(item, "cron"); ok && v.Kind == yaml.ScalarNode {
			out = append(out, wf.ScheduleTrigger{Cron: v.Value})
		}
	}
	return out
}

// decodeWorkflowDispatch decodes `on.workflow_dispatch:` and its inputs.
// A nil or empty value still produces a present trigger (with no inputs)
// — that is the legitimate "I can be manually dispatched" shape.
func decodeWorkflowDispatch(n *yaml.Node) *wf.WorkflowDispatchTrigger {
	tr := &wf.WorkflowDispatchTrigger{}
	if n == nil || n.Kind != yaml.MappingNode {
		return tr
	}
	if _, v, ok := findChild(n, "inputs"); ok && v.Kind == yaml.MappingNode {
		tr.Inputs = decodeDispatchInputs(v)
	}
	return tr
}

// decodeDispatchInputs reads the inputs sub-mapping. Inputs are stored
// in a map (key insertion order is not preserved by Go maps but is
// preserved by yaml.v3 in the Node tree — we honour that by returning a
// fresh map every call, leaving any ordering concerns to the caller).
func decodeDispatchInputs(n *yaml.Node) map[string]wf.DispatchInput {
	if n == nil || n.Kind != yaml.MappingNode {
		return nil
	}
	out := make(map[string]wf.DispatchInput, len(n.Content)/2)
	for i := 0; i+1 < len(n.Content); i += 2 {
		k := n.Content[i]
		v := n.Content[i+1]
		if k.Kind != yaml.ScalarNode {
			continue
		}
		di := wf.DispatchInput{}
		if v.Kind == yaml.MappingNode {
			if _, vv, ok := findChild(v, "description"); ok && vv.Kind == yaml.ScalarNode {
				di.Description = vv.Value
			}
			if _, vv, ok := findChild(v, "required"); ok && vv.Kind == yaml.ScalarNode {
				di.Required = scalarBool(vv)
			}
			if _, vv, ok := findChild(v, "default"); ok && vv.Kind == yaml.ScalarNode {
				di.Default = vv.Value
			}
			if _, vv, ok := findChild(v, "type"); ok && vv.Kind == yaml.ScalarNode {
				di.Type = vv.Value
			}
			if _, vv, ok := findChild(v, "options"); ok {
				di.Options = decodeStringList(vv)
			}
		}
		out[k.Value] = di
	}
	return out
}

// decodeWorkflowCall decodes `on.workflow_call:` — the most complex of
// the trigger shapes. We only model the subset required by the IR;
// anything not represented falls to the wider Other list.
func decodeWorkflowCall(path string, n *yaml.Node) *wf.WorkflowCallTrigger {
	tr := &wf.WorkflowCallTrigger{}
	if n == nil || n.Kind != yaml.MappingNode {
		return tr
	}
	if _, v, ok := findChild(n, "inputs"); ok && v.Kind == yaml.MappingNode {
		tr.Inputs = decodeCallInputs(v)
	}
	if _, v, ok := findChild(n, "secrets"); ok && v.Kind == yaml.MappingNode {
		tr.Secrets = decodeCallSecrets(v)
	}
	if _, v, ok := findChild(n, "outputs"); ok && v.Kind == yaml.MappingNode {
		tr.Outputs = decodeCallOutputs(path, v)
	}
	return tr
}

// decodeCallInputs decodes `workflow_call.inputs`. Mirrors
// decodeDispatchInputs but uses the CallInput value object.
func decodeCallInputs(n *yaml.Node) map[string]wf.CallInput {
	if n == nil || n.Kind != yaml.MappingNode {
		return nil
	}
	out := make(map[string]wf.CallInput, len(n.Content)/2)
	for i := 0; i+1 < len(n.Content); i += 2 {
		k := n.Content[i]
		v := n.Content[i+1]
		if k.Kind != yaml.ScalarNode {
			continue
		}
		ci := wf.CallInput{}
		if v.Kind == yaml.MappingNode {
			if _, vv, ok := findChild(v, "description"); ok && vv.Kind == yaml.ScalarNode {
				ci.Description = vv.Value
			}
			if _, vv, ok := findChild(v, "required"); ok && vv.Kind == yaml.ScalarNode {
				ci.Required = scalarBool(vv)
			}
			if _, vv, ok := findChild(v, "default"); ok && vv.Kind == yaml.ScalarNode {
				ci.Default = vv.Value
			}
			if _, vv, ok := findChild(v, "type"); ok && vv.Kind == yaml.ScalarNode {
				ci.Type = vv.Value
			}
		}
		out[k.Value] = ci
	}
	return out
}

// decodeCallSecrets decodes `workflow_call.secrets`.
func decodeCallSecrets(n *yaml.Node) map[string]wf.CallSecret {
	if n == nil || n.Kind != yaml.MappingNode {
		return nil
	}
	out := make(map[string]wf.CallSecret, len(n.Content)/2)
	for i := 0; i+1 < len(n.Content); i += 2 {
		k := n.Content[i]
		v := n.Content[i+1]
		if k.Kind != yaml.ScalarNode {
			continue
		}
		cs := wf.CallSecret{}
		if v.Kind == yaml.MappingNode {
			if _, vv, ok := findChild(v, "description"); ok && vv.Kind == yaml.ScalarNode {
				cs.Description = vv.Value
			}
			if _, vv, ok := findChild(v, "required"); ok && vv.Kind == yaml.ScalarNode {
				cs.Required = scalarBool(vv)
			}
		}
		out[k.Value] = cs
	}
	return out
}

// decodeCallOutputs decodes `workflow_call.outputs`. The value of each
// output is captured as an Expression so the body can reference
// `${{ ... }}` at runtime.
func decodeCallOutputs(path string, n *yaml.Node) map[string]wf.CallOutput {
	if n == nil || n.Kind != yaml.MappingNode {
		return nil
	}
	out := make(map[string]wf.CallOutput, len(n.Content)/2)
	for i := 0; i+1 < len(n.Content); i += 2 {
		k := n.Content[i]
		v := n.Content[i+1]
		if k.Kind != yaml.ScalarNode {
			continue
		}
		co := wf.CallOutput{}
		if v.Kind == yaml.MappingNode {
			if _, vv, ok := findChild(v, "description"); ok && vv.Kind == yaml.ScalarNode {
				co.Description = vv.Value
			}
			if _, vv, ok := findChild(v, "value"); ok && vv.Kind == yaml.ScalarNode {
				co.Value = decodeExpression(path, vv)
			}
		}
		out[k.Value] = co
	}
	return out
}

// decodeWorkflowRun decodes `on.workflow_run:` filters.
func decodeWorkflowRun(n *yaml.Node) *wf.WorkflowRunTrigger {
	tr := &wf.WorkflowRunTrigger{}
	if n == nil || n.Kind != yaml.MappingNode {
		return tr
	}
	if _, v, ok := findChild(n, "workflows"); ok {
		tr.Workflows = decodeStringList(v)
	}
	if _, v, ok := findChild(n, "types"); ok {
		tr.Types = decodeStringList(v)
	}
	if _, v, ok := findChild(n, "branches"); ok {
		tr.Branches = decodeStringList(v)
	}
	return tr
}

// decodeStringList handles the two GitHub Actions list shapes: a scalar
// (treated as a single-element list) or a sequence. Non-scalar elements
// are skipped to keep the decoder tolerant of malformed input.
func decodeStringList(n *yaml.Node) []string {
	if n == nil {
		return nil
	}
	switch n.Kind {
	case yaml.ScalarNode:
		if n.Value == "" {
			return nil
		}
		return []string{n.Value}
	case yaml.SequenceNode:
		out := make([]string, 0, len(n.Content))
		for _, item := range n.Content {
			if item.Kind == yaml.ScalarNode {
				out = append(out, item.Value)
			}
		}
		return out
	default:
		return nil
	}
}

// decodeStringMap reads a YAML mapping into a Go map[string]string. Non-
// scalar values are coerced with yaml.Node.Decode so simple types
// (numbers, bools) survive as their canonical string forms.
func decodeStringMap(n *yaml.Node) map[string]string {
	if n == nil || n.Kind != yaml.MappingNode {
		return nil
	}
	out := make(map[string]string, len(n.Content)/2)
	for i := 0; i+1 < len(n.Content); i += 2 {
		k := n.Content[i]
		v := n.Content[i+1]
		if k.Kind != yaml.ScalarNode {
			continue
		}
		if v.Kind == yaml.ScalarNode {
			out[k.Value] = v.Value
		}
	}
	return out
}

// decodeDefaults handles both workflow-level and job-level `defaults:`
// blocks. They share a schema so a single decoder is enough.
func decodeDefaults(n *yaml.Node) wf.Defaults {
	d := wf.Defaults{}
	if n == nil || n.Kind != yaml.MappingNode {
		return d
	}
	if _, v, ok := findChild(n, "run"); ok && v.Kind == yaml.MappingNode {
		if _, sv, ok := findChild(v, "shell"); ok && sv.Kind == yaml.ScalarNode {
			d.Run.Shell = sv.Value
		}
		if _, sv, ok := findChild(v, "working-directory"); ok && sv.Kind == yaml.ScalarNode {
			d.Run.WorkingDirectory = sv.Value
		}
	}
	return d
}

// decodeJobs walks the `jobs:` mapping and produces a JobsByID map.
// Each job is decoded independently; the first error short-circuits the
// whole workflow so callers see a stable failure point.
func decodeJobs(path string, n *yaml.Node) (map[wf.JobID]wf.Job, error) {
	if n == nil || n.Kind != yaml.MappingNode {
		return nil, newError(path, n, "`jobs:` must be a mapping", nil)
	}
	out := make(map[wf.JobID]wf.Job, len(n.Content)/2)
	for i := 0; i+1 < len(n.Content); i += 2 {
		k := n.Content[i]
		v := n.Content[i+1]
		if k.Kind != yaml.ScalarNode {
			continue
		}
		id := wf.JobID(k.Value)
		job, err := decodeJob(path, id, k, v)
		if err != nil {
			return nil, err
		}
		out[id] = job
	}
	return out, nil
}

// decodeJob decodes a single `<job-id>:` entry. The keyNode is the key
// scalar so the job span can be anchored at the *key* rather than the
// value — this matches user intuition when reporting "job X has a cycle"
// and is the convention used throughout the parser.
func decodeJob(path string, id wf.JobID, keyNode, n *yaml.Node) (wf.Job, error) {
	job := wf.Job{
		ID:   id,
		Span: span(path, keyNode),
	}
	if n == nil || n.Kind != yaml.MappingNode {
		return job, newError(path, n, fmt.Sprintf("job %q must be a mapping", id), nil)
	}
	if _, v, ok := findChild(n, "name"); ok && v.Kind == yaml.ScalarNode {
		job.Name = v.Value
	}
	if _, v, ok := findChild(n, "runs-on"); ok {
		job.RunsOn = decodeRunsOn(v)
	}
	if _, v, ok := findChild(n, "needs"); ok {
		job.Needs = decodeNeeds(v)
	}
	if _, v, ok := findChild(n, "if"); ok && v.Kind == yaml.ScalarNode {
		job.If = decodeExpression(path, v)
	}
	if _, v, ok := findChild(n, "strategy"); ok && v.Kind == yaml.MappingNode {
		if m, err := decodeStrategy(path, v); err != nil {
			return wf.Job{}, err
		} else if m != nil {
			job.Matrix = m
		}
	}
	if _, v, ok := findChild(n, "env"); ok {
		job.Env = decodeStringMap(v)
	}
	if _, v, ok := findChild(n, "defaults"); ok {
		job.Defaults = decodeDefaults(v)
	}
	if _, v, ok := findChild(n, "continue-on-error"); ok && v.Kind == yaml.ScalarNode {
		job.ContinueOnError = scalarBool(v)
	}
	if _, v, ok := findChild(n, "timeout-minutes"); ok && v.Kind == yaml.ScalarNode {
		job.TimeoutMinutes = scalarInt(v)
	}
	if _, v, ok := findChild(n, "outputs"); ok && v.Kind == yaml.MappingNode {
		job.Outputs = decodeOutputExpressions(path, v)
	}
	if _, v, ok := findChild(n, "steps"); ok && v.Kind == yaml.SequenceNode {
		steps, err := decodeSteps(path, v)
		if err != nil {
			return wf.Job{}, err
		}
		job.Steps = steps
	}
	return job, nil
}

// decodeRunsOn returns a RunnerLabel that captures the original `runs-on`
// authoring shape.
//
// Documentation of Raw:
//   - For the scalar form `runs-on: ubuntu-latest`, Raw is the literal
//     scalar value ("ubuntu-latest") and Labels is a single-element slice.
//   - For the sequence form `runs-on: [self-hosted, gpu]`, Raw is the
//     bracketed Go-style join ("[self-hosted, gpu]") and Labels lists each
//     label in source order. We deliberately do not round-trip yaml.v3
//     style; the Raw form is a debugging aid, not a serialisation target.
func decodeRunsOn(n *yaml.Node) wf.RunnerLabel {
	if n == nil {
		return wf.RunnerLabel{}
	}
	switch n.Kind {
	case yaml.ScalarNode:
		return wf.RunnerLabel{Raw: n.Value, Labels: []string{n.Value}}
	case yaml.SequenceNode:
		labels := decodeStringList(n)
		return wf.RunnerLabel{Raw: "[" + strings.Join(labels, ", ") + "]", Labels: labels}
	default:
		return wf.RunnerLabel{}
	}
}

// decodeNeeds normalises the `needs:` field — which may be a scalar
// (single JobID) or a sequence — into []JobID.
func decodeNeeds(n *yaml.Node) []wf.JobID {
	if n == nil {
		return nil
	}
	switch n.Kind {
	case yaml.ScalarNode:
		if n.Value == "" {
			return nil
		}
		return []wf.JobID{wf.JobID(n.Value)}
	case yaml.SequenceNode:
		out := make([]wf.JobID, 0, len(n.Content))
		for _, item := range n.Content {
			if item.Kind == yaml.ScalarNode {
				out = append(out, wf.JobID(item.Value))
			}
		}
		return out
	default:
		return nil
	}
}

// decodeStrategy reads `strategy:` and returns the Matrix portion. It is
// the entry point invoked by decodeJob.
func decodeStrategy(path string, n *yaml.Node) (*wf.Matrix, error) {
	if n == nil || n.Kind != yaml.MappingNode {
		return nil, nil
	}
	var (
		mtxNode    *yaml.Node
		maxPar     int
		failFast   = true // GitHub default
		failFastOK bool
	)
	for i := 0; i+1 < len(n.Content); i += 2 {
		k := n.Content[i]
		v := n.Content[i+1]
		if k.Kind != yaml.ScalarNode {
			continue
		}
		switch k.Value {
		case "matrix":
			mtxNode = v
		case "max-parallel":
			if v.Kind == yaml.ScalarNode {
				maxPar = scalarInt(v)
			}
		case "fail-fast":
			if v.Kind == yaml.ScalarNode {
				failFast = scalarBool(v)
				failFastOK = true
			}
		}
	}
	if mtxNode == nil {
		return nil, nil
	}
	mtx, err := decodeMatrix(path, mtxNode)
	if err != nil {
		return nil, err
	}
	if mtx == nil {
		return nil, nil
	}
	mtx.MaxParallel = maxPar
	if failFastOK {
		mtx.FailFast = failFast
	} else {
		// Apply GitHub's documented default explicitly so consumers do not
		// have to know it. This also matches the IR contract:
		// `FailFast defaults to true on GitHub; here it is stored as
		// authored, with the parser supplying the default if absent`.
		mtx.FailFast = true
	}
	return mtx, nil
}

// decodeMatrix decodes the `strategy.matrix:` mapping. Top-level keys
// other than `include`/`exclude` become axes; the values of include and
// exclude are sequences of mapping nodes that we materialise into
// []map[string]any for downstream expansion.
func decodeMatrix(path string, n *yaml.Node) (*wf.Matrix, error) {
	if n == nil || n.Kind != yaml.MappingNode {
		return nil, nil
	}
	m := &wf.Matrix{Axes: map[string][]any{}}
	for i := 0; i+1 < len(n.Content); i += 2 {
		k := n.Content[i]
		v := n.Content[i+1]
		if k.Kind != yaml.ScalarNode {
			continue
		}
		switch k.Value {
		case "include":
			rows, err := decodeAnyMapList(path, v)
			if err != nil {
				return nil, err
			}
			m.Include = rows
		case "exclude":
			rows, err := decodeAnyMapList(path, v)
			if err != nil {
				return nil, err
			}
			m.Exclude = rows
		default:
			if v == nil {
				continue
			}
			values, err := decodeAnyList(path, v)
			if err != nil {
				return nil, err
			}
			m.Axes[k.Value] = values
		}
	}
	if len(m.Axes) == 0 && len(m.Include) == 0 && len(m.Exclude) == 0 {
		return nil, nil
	}
	return m, nil
}

// decodeAnyList decodes either a single scalar (one-element list) or a
// sequence into []any. yaml.v3 Decode is used to honour YAML's native
// scalar typing — ints stay ints, bools stay bools, strings stay strings.
func decodeAnyList(path string, n *yaml.Node) ([]any, error) {
	if n == nil {
		return nil, nil
	}
	switch n.Kind {
	case yaml.ScalarNode:
		var v any
		if err := n.Decode(&v); err != nil {
			return nil, newError(path, n, "matrix axis value: "+err.Error(), err)
		}
		return []any{v}, nil
	case yaml.SequenceNode:
		out := make([]any, 0, len(n.Content))
		for _, item := range n.Content {
			var v any
			if err := item.Decode(&v); err != nil {
				return nil, newError(path, item, "matrix axis element: "+err.Error(), err)
			}
			out = append(out, v)
		}
		return out, nil
	default:
		return nil, newError(path, n, "matrix axis must be a scalar or sequence", nil)
	}
}

// decodeAnyMapList decodes a YAML sequence of mappings into
// []map[string]any. Used for matrix include/exclude.
func decodeAnyMapList(path string, n *yaml.Node) ([]map[string]any, error) {
	if n == nil || n.Kind != yaml.SequenceNode {
		return nil, nil
	}
	out := make([]map[string]any, 0, len(n.Content))
	for _, item := range n.Content {
		if item.Kind != yaml.MappingNode {
			continue
		}
		row := map[string]any{}
		for i := 0; i+1 < len(item.Content); i += 2 {
			k := item.Content[i]
			v := item.Content[i+1]
			if k.Kind != yaml.ScalarNode {
				continue
			}
			var decoded any
			if err := v.Decode(&decoded); err != nil {
				return nil, newError(path, v, "matrix include/exclude value: "+err.Error(), err)
			}
			row[k.Value] = decoded
		}
		out = append(out, row)
	}
	return out, nil
}

// decodeOutputExpressions reads a `job.outputs:` mapping into Expression
// values so callers can later resolve the embedded `${{ ... }}` against a
// context.
func decodeOutputExpressions(path string, n *yaml.Node) map[string]wf.Expression {
	if n == nil || n.Kind != yaml.MappingNode {
		return nil
	}
	out := make(map[string]wf.Expression, len(n.Content)/2)
	for i := 0; i+1 < len(n.Content); i += 2 {
		k := n.Content[i]
		v := n.Content[i+1]
		if k.Kind != yaml.ScalarNode || v.Kind != yaml.ScalarNode {
			continue
		}
		out[k.Value] = decodeExpression(path, v)
	}
	return out
}

// decodeSteps walks a `steps:` sequence and returns the decoded list.
func decodeSteps(path string, n *yaml.Node) ([]wf.Step, error) {
	if n == nil || n.Kind != yaml.SequenceNode {
		return nil, nil
	}
	out := make([]wf.Step, 0, len(n.Content))
	for _, item := range n.Content {
		if item.Kind != yaml.MappingNode {
			return nil, newError(path, item, "step must be a mapping", nil)
		}
		s, err := decodeStep(path, item)
		if err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, nil
}

// decodeStep decodes a single step mapping. Kind is determined by which
// of `uses:` or `run:` is present, with `uses:` taking priority since a
// well-formed step never declares both.
func decodeStep(path string, n *yaml.Node) (wf.Step, error) {
	s := wf.Step{
		Span: span(path, n),
	}
	if _, v, ok := findChild(n, "id"); ok && v.Kind == yaml.ScalarNode {
		s.ID = v.Value
	}
	if _, v, ok := findChild(n, "name"); ok && v.Kind == yaml.ScalarNode {
		s.Name = v.Value
	}
	if _, v, ok := findChild(n, "if"); ok && v.Kind == yaml.ScalarNode {
		s.If = decodeExpression(path, v)
	}
	if _, v, ok := findChild(n, "shell"); ok && v.Kind == yaml.ScalarNode {
		s.Shell = v.Value
	}
	if _, v, ok := findChild(n, "working-directory"); ok && v.Kind == yaml.ScalarNode {
		s.WorkingDirectory = v.Value
	}
	if _, v, ok := findChild(n, "continue-on-error"); ok && v.Kind == yaml.ScalarNode {
		s.ContinueOnError = scalarBool(v)
	}
	if _, v, ok := findChild(n, "timeout-minutes"); ok && v.Kind == yaml.ScalarNode {
		s.TimeoutMinutes = scalarInt(v)
	}
	if _, v, ok := findChild(n, "with"); ok && v.Kind == yaml.MappingNode {
		s.With = decodeExpressionMap(path, v)
	}
	if _, v, ok := findChild(n, "env"); ok && v.Kind == yaml.MappingNode {
		s.Env = decodeExpressionMap(path, v)
	}
	if _, v, ok := findChild(n, "uses"); ok && v.Kind == yaml.ScalarNode {
		s.Kind = wf.StepKindUses
		ref, err := decodeUses(path, v)
		if err != nil {
			return wf.Step{}, err
		}
		s.Uses = ref
	} else if _, v, ok := findChild(n, "run"); ok && v.Kind == yaml.ScalarNode {
		s.Kind = wf.StepKindRun
		s.Run = v.Value
	}
	return s, nil
}

// decodeExpressionMap reads a mapping of scalar→scalar into a map of
// Expression values. Used by both `with:` and `env:` on steps so that
// every value carries the right span.
func decodeExpressionMap(path string, n *yaml.Node) map[string]wf.Expression {
	if n == nil || n.Kind != yaml.MappingNode {
		return nil
	}
	out := make(map[string]wf.Expression, len(n.Content)/2)
	for i := 0; i+1 < len(n.Content); i += 2 {
		k := n.Content[i]
		v := n.Content[i+1]
		if k.Kind != yaml.ScalarNode || v.Kind != yaml.ScalarNode {
			continue
		}
		out[k.Value] = decodeExpression(path, v)
	}
	return out
}

// decodeUses parses a `uses:` scalar into a UsesRef. Supported forms:
//
//   - `actions/checkout@v4`                        → remote, no path
//   - `actions/aws/ec2-action@v3`                  → remote, with path
//   - `./.github/actions/foo`                      → local
//
// We return a typed Error rather than panicking on malformed input so
// schema validation can surface the issue with a precise span.
func decodeUses(path string, n *yaml.Node) (wf.UsesRef, error) {
	raw := strings.TrimSpace(n.Value)
	if raw == "" {
		return wf.UsesRef{}, newError(path, n, "empty `uses:` value", nil)
	}
	if strings.HasPrefix(raw, "./") || strings.HasPrefix(raw, "../") {
		return wf.UsesRef{Local: true, Path: raw}, nil
	}
	// owner/repo[/path]@ref
	atIdx := strings.LastIndex(raw, "@")
	if atIdx <= 0 {
		return wf.UsesRef{}, newError(path, n, "`uses:` must be of the form owner/repo[/path]@ref or ./local-action", nil)
	}
	ref := raw[atIdx+1:]
	left := raw[:atIdx]
	parts := strings.SplitN(left, "/", 3)
	if len(parts) < 2 {
		return wf.UsesRef{}, newError(path, n, "`uses:` must include an owner and repo (owner/repo[/path]@ref)", nil)
	}
	uref := wf.UsesRef{
		Owner: parts[0],
		Repo:  parts[1],
		Ref:   ref,
	}
	if len(parts) == 3 {
		uref.Path = parts[2]
	}
	return uref, nil
}

// decodeExpression captures an Expression value from a scalar node.
//
// Decision on Raw format: when the scalar is authored as a literal
// `${{ ... }}` template, we preserve the whole thing (including the
// `${{` and `}}` delimiters) but trim outer whitespace. When the scalar
// is a bare conditional (e.g. `if: success() && github.event_name == 'push'`)
// we store the trimmed text exactly as authored. This matches what
// GitHub Actions documents — an `if:` value is implicitly expression
// scope; a `with:` or `env:` scalar must be quoted with the `${{ ... }}`
// shell.
//
// The trade-off is intentional: callers that want the *body* alone can
// strip the delimiters themselves; callers that want to reconstruct the
// authored form already have it. This avoids ambiguity around values
// that legitimately contain a literal `${{` (rare but possible inside
// shell heredocs).
func decodeExpression(path string, n *yaml.Node) wf.Expression {
	if n == nil {
		return wf.Expression{}
	}
	return wf.Expression{
		Raw:  strings.TrimSpace(n.Value),
		Span: span(path, n),
	}
}

// scalarBool parses a yaml scalar's value as a boolean using yaml.v3's
// own decoder, so the same set of truthy/falsy keywords accepted by the
// upstream library is accepted here.
func scalarBool(n *yaml.Node) bool {
	if n == nil {
		return false
	}
	var b bool
	if err := n.Decode(&b); err == nil {
		return b
	}
	switch strings.ToLower(strings.TrimSpace(n.Value)) {
	case "true", "yes", "on", "y", "1":
		return true
	default:
		return false
	}
}

// scalarInt parses a yaml scalar as an int via yaml.v3's decoder, falling
// back to strconv when Decode rejects an unquoted numeric.
func scalarInt(n *yaml.Node) int {
	if n == nil {
		return 0
	}
	var i int
	if err := n.Decode(&i); err == nil {
		return i
	}
	if v, err := strconv.Atoi(strings.TrimSpace(n.Value)); err == nil {
		return v
	}
	return 0
}

