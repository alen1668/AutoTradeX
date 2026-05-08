package webhook

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/lizhaojie/tvbot/internal/application/ingest"
)

type stubSvc struct {
	got struct {
		body []byte
		ip   net.IP
	}
	res *ingest.ReceiveResult
	err error
}

func (s *stubSvc) Receive(_ context.Context, body []byte, ip net.IP) (*ingest.ReceiveResult, error) {
	s.got.body = body
	s.got.ip = ip
	return s.res, s.err
}

type stubDispatcher struct {
	mu      sync.Mutex
	submits []struct {
		Strategy string
		ID       int64
	}
}

func (d *stubDispatcher) Submit(strategy string, id int64) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.submits = append(d.submits, struct {
		Strategy string
		ID       int64
	}{strategy, id})
}

func (d *stubDispatcher) Calls() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return len(d.submits)
}

func TestHandler_PendingReturns200AndDispatches(t *testing.T) {
	svc := &stubSvc{res: &ingest.ReceiveResult{
		SignalID: 42, StrategyID: "ETH", Decision: "pending",
	}}
	disp := &stubDispatcher{}
	h := NewHandler(svc, disp, zerolog.Nop())

	body := `{"strategy_id":"ETH","symbol":"ETHUSDT","signal":"Long","price":"100","timestamp":1,"secret":"x"}`
	req := httptest.NewRequest(http.MethodPost, "/webhook/tv", strings.NewReader(body))
	req.RemoteAddr = "203.0.113.5:1234"
	w := httptest.NewRecorder()
	h.Post(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	var resp response
	require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
	assert.Equal(t, "pending", resp.Decision)
	assert.Equal(t, int64(42), resp.SignalID)

	require.Equal(t, 1, disp.Calls(), "pending decision must hand off to dispatcher")
	assert.Equal(t, "ETH", disp.submits[0].Strategy)
	assert.Equal(t, int64(42), disp.submits[0].ID)
}

func TestHandler_DuplicateReturns200WithoutDispatch(t *testing.T) {
	svc := &stubSvc{res: &ingest.ReceiveResult{
		SignalID: 7, Decision: "duplicate",
	}}
	disp := &stubDispatcher{}
	h := NewHandler(svc, disp, zerolog.Nop())
	req := httptest.NewRequest(http.MethodPost, "/webhook/tv", strings.NewReader("{}"))
	req.RemoteAddr = "127.0.0.1:1234"
	w := httptest.NewRecorder()
	h.Post(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, 0, disp.Calls(), "duplicate must NOT submit again")
}

func TestHandler_InvalidReturns400(t *testing.T) {
	svc := &stubSvc{res: &ingest.ReceiveResult{Decision: "invalid", Reason: "bad json"}}
	disp := &stubDispatcher{}
	h := NewHandler(svc, disp, zerolog.Nop())
	req := httptest.NewRequest(http.MethodPost, "/webhook/tv", strings.NewReader("{}"))
	req.RemoteAddr = "127.0.0.1:1234"
	w := httptest.NewRecorder()
	h.Post(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Equal(t, 0, disp.Calls())
}

func TestHandler_InternalErrorReturns500(t *testing.T) {
	svc := &stubSvc{err: assertErr("boom")}
	disp := &stubDispatcher{}
	h := NewHandler(svc, disp, zerolog.Nop())
	req := httptest.NewRequest(http.MethodPost, "/webhook/tv", strings.NewReader("{}"))
	req.RemoteAddr = "127.0.0.1:1234"
	w := httptest.NewRecorder()
	h.Post(w, req)
	assert.Equal(t, http.StatusInternalServerError, w.Code)
	assert.Equal(t, 0, disp.Calls())
}

func TestHandler_RejectsGet(t *testing.T) {
	h := NewHandler(&stubSvc{}, &stubDispatcher{}, zerolog.Nop())
	req := httptest.NewRequest(http.MethodGet, "/webhook/tv", nil)
	w := httptest.NewRecorder()
	h.Post(w, req)
	assert.Equal(t, http.StatusMethodNotAllowed, w.Code)
}

func TestClientIP_HonorsXFFFromLoopback(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/webhook/tv", strings.NewReader(""))
	req.RemoteAddr = "127.0.0.1:80"
	req.Header.Set("X-Forwarded-For", "1.2.3.4, 10.0.0.1")
	got := ClientIP(req)
	assert.Equal(t, "10.0.0.1", got.String(),
		"rightmost XFF entry is the immediate proxy's caller")
}

func TestClientIP_IgnoresXFFFromExternal(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/webhook/tv", strings.NewReader(""))
	req.RemoteAddr = "8.8.8.8:80"
	req.Header.Set("X-Forwarded-For", "1.2.3.4")
	got := ClientIP(req)
	assert.Equal(t, "8.8.8.8", got.String(),
		"don't trust XFF from external clients")
}

type assertErr string

func (e assertErr) Error() string { return string(e) }
