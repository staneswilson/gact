package expr

import pubexpr "github.com/staneswilson/gact/pkg/expr"

// EmptyContext is a no-op Context implementation: every lookup returns
// Null and no error. It is the canonical choice for evaluating literal
// expressions and as a placeholder in tests that do not exercise
// context-dependent paths.
//
// Other Context implementations live elsewhere by design:
//
//   - StaticContext (Task 0.11) is a frozen snapshot of github/env/runner
//     used by the static linter and dry-run pipelines.
//   - RuntimeContext (scheduling) tracks live job outputs and step
//     statuses as a workflow executes. Both implement the same
//     pkg/expr.Context interface so the evaluator stays oblivious.
func EmptyContext() pubexpr.Context { return emptyCtx{} }

type emptyCtx struct{}

func (emptyCtx) Get(_, _ string) (value, error) {
	return value{Kind: kindNull}, nil
}
