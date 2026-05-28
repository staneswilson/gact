// Status-function placeholders for the GH expression grammar.
//
// GitHub Actions exposes four status functions whose values depend on
// the surrounding job/step state:
//
//   - success()   true when no previous step in the job has failed and
//     no needed job's outcome is `failure` / `cancelled`.
//   - failure()   true when any previous step or needed job has failed.
//   - always()    true unconditionally — used to force a step to run
//     even on cancel/failure.
//   - cancelled() true when the workflow was cancelled.
//
// The current funcImpl signature (args []value) -> (value, error) gives us
// no access to the running schedule, so we cannot resolve these honestly
// at the expression layer alone. To keep the registry complete and parsing
// trustworthy in Phase 0 we install constant placeholders here:
//
//   - always()    -> true   (final, identical to runtime)
//   - success()   -> true   (optimistic default; the scheduler will
//     override this once it has a job/step ctx)
//   - failure()   -> false  (optimistic default; scheduler overrides)
//   - cancelled() -> false  (scheduler overrides)
//
// In P1 the scheduler will replace these via expr.register() at execution
// time using a wrapper that closes over the live RuntimeContext. That work
// is intentionally out of scope here — the placeholders exist only so that
// expression parsing and static linting do not fail for workflows that use
// these names.
package expr

import "fmt"

func init() {
	register("success", successFunc)
	register("failure", failureFunc)
	register("always", alwaysFunc)
	register("cancelled", cancelledFunc)
}

// successFunc returns true so that `if: success()` is non-skipping by
// default during static analysis. The scheduler replaces this with a
// stateful version in P1.
func successFunc(args []value) (value, error) {
	if len(args) != 0 {
		return value{}, fmt.Errorf("success: expected 0 arguments, got %d", len(args))
	}
	return boolValue(true), nil
}

// failureFunc returns false at parse / static-analysis time. A step using
// `if: failure()` should not be considered as "would run" unless we have
// runtime evidence of an earlier failure, which we do not have here.
func failureFunc(args []value) (value, error) {
	if len(args) != 0 {
		return value{}, fmt.Errorf("failure: expected 0 arguments, got %d", len(args))
	}
	return boolValue(false), nil
}

// alwaysFunc unconditionally returns true. This matches GitHub at both
// static and runtime — the scheduler does not need to override it but the
// registration stays here so the unknown-function check at parse time
// does not fire.
func alwaysFunc(args []value) (value, error) {
	if len(args) != 0 {
		return value{}, fmt.Errorf("always: expected 0 arguments, got %d", len(args))
	}
	return boolValue(true), nil
}

// cancelledFunc returns false at parse / static-analysis time. The
// scheduler replaces this with a context-aware version that consults the
// run's cancellation state during P1.
func cancelledFunc(args []value) (value, error) {
	if len(args) != 0 {
		return value{}, fmt.Errorf("cancelled: expected 0 arguments, got %d", len(args))
	}
	return boolValue(false), nil
}
