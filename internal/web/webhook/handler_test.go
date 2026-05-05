package webhook

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
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
	res *ingest.IngestResult
	err error
}

func (s *stubSvc) Ingest(_ context.Context, body []byte, ip net.IP) (*ingest.IngestResult, error) {
	s.got.body = body
	s.got.ip = ip
	return s.res, s.err
}

func TestHandler_AcceptsValidSignal(t *testing.T) {
	svc := &stubSvc{res: &ingest.IngestResult{
		SignalID: 42, Decision: "accepted", ActionTaken: "open_long",
	}}
	h := NewHandler(svc, zerolog.Nop())

	body := `{"strategy_id":"s","symbol":"ETHUSDC","signal":"Long","price":"100","timestamp":1,"secret":"x"}`
	req := httptest.NewRequest(http.MethodPost, "/webhook/tv", strings.NewReader(body))
	req.RemoteAddr = "203.0.113.5:1234"
	w := httptest.NewRecorder()
	h.Post(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	var resp response
	require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
	assert.Equal(t, "accepted", resp.Decision)
	assert.Equal(t, "open_long", resp.Action)
	assert.Equal(t, int64(42), resp.SignalID)

	assert.Equal(t, []byte(body), svc.got.body)
	assert.NotNil(t, svc.got.ip)
}

func TestHandler_InvalidReturns400(t *testing.T) {
	svc := &stubSvc{res: &ingest.IngestResult{Decision: "invalid", Reason: "bad json"}}
	h := NewHandler(svc, zerolog.Nop())
	req := httptest.NewRequest(http.MethodPost, "/webhook/tv", strings.NewReader("{}"))
	req.RemoteAddr = "127.0.0.1:1234"
	w := httptest.NewRecorder()
	h.Post(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestHandler_InternalErrorReturns500(t *testing.T) {
	svc := &stubSvc{err: assertErr("boom")}
	h := NewHandler(svc, zerolog.Nop())
	req := httptest.NewRequest(http.MethodPost, "/webhook/tv", strings.NewReader("{}"))
	req.RemoteAddr = "127.0.0.1:1234"
	w := httptest.NewRecorder()
	h.Post(w, req)
	assert.Equal(t, http.StatusInternalServerError, w.Code)
}

func TestHandler_RejectsGet(t *testing.T) {
	h := NewHandler(&stubSvc{}, zerolog.Nop())
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
