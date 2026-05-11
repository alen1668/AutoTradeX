package admin

import (
	"testing"
)

// TestEvalHandler_TypeCompiles is a structural smoke test: if the
// constructor signature drifts, this fails to compile.
//
// Behavior of the four routes (200 / 404 / template render / XSS escape)
// is covered by eval_handler_integration_test.go which spins up a real
// postgres via dockertest. We intentionally don't try to test the
// handlers here against a nil pool, since after Task 10+ they all call
// the pool and would panic.
func TestEvalHandler_TypeCompiles(t *testing.T) {
	var _ *EvalHandler = NewEvalHandler(nil, nil)
}
