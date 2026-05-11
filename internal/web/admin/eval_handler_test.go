package admin

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestEvalHandler_TypeCompiles is a structural smoke test: if the
// constructor signature drifts, this fails to compile.
func TestEvalHandler_TypeCompiles(t *testing.T) {
	var _ *EvalHandler = NewEvalHandler(nil, nil)
}

// TestEvalHandler_StubsReturn501 verifies the four routes are wired and
// reachable via direct method calls. Each returns 501 until its real
// implementation lands (Tasks 10-13).
func TestEvalHandler_StubsReturn501(t *testing.T) {
	h := NewEvalHandler(nil, nil)
	for _, route := range []struct {
		name string
		fn   func(http.ResponseWriter, *http.Request)
	}{
		{"Index", h.Index},
		{"ReplayList", h.ReplayList},
		{"ReplayDetail", h.ReplayDetail},
		{"ReplayRowsPartial", h.ReplayRowsPartial},
	} {
		req := httptest.NewRequest("GET", "/eval", nil)
		w := httptest.NewRecorder()
		route.fn(w, req)
		require.Equal(t, http.StatusNotImplemented, w.Code, route.name)
	}
}
