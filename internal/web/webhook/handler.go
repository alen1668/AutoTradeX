package webhook

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"

	"github.com/rs/zerolog"

	"github.com/lizhaojie/tvbot/internal/application/ingest"
	"github.com/lizhaojie/tvbot/internal/log"
)

const maxBodyBytes = 16 * 1024 // 16KB; TV payloads are tiny

// IngestService is the subset of ingest.Service the handler depends on.
// Receive is the synchronous fast path; the slow path runs on a worker
// the handler hands off to via Dispatcher.
type IngestService interface {
	Receive(ctx context.Context, body []byte, ip net.IP) (*ingest.ReceiveResult, error)
}

// Dispatcher hands the slow path off to a per-strategy worker.
type Dispatcher interface {
	Submit(strategyID string, signalID int64)
}

type Handler struct {
	svc        IngestService
	dispatcher Dispatcher
	log        zerolog.Logger
}

func NewHandler(svc IngestService, d Dispatcher, log zerolog.Logger) *Handler {
	return &Handler{svc: svc, dispatcher: d, log: log}
}

type response struct {
	Decision string `json:"decision"`
	Reason   string `json:"reason,omitempty"`
	SignalID int64  `json:"signal_id,omitempty"`
}

func (h *Handler) Post(w http.ResponseWriter, r *http.Request) {
	logger := log.FromContext(r.Context(), h.log)

	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, maxBodyBytes))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, response{Decision: "invalid", Reason: "read body: " + err.Error()})
		return
	}
	ip := ClientIP(r)

	res, err := h.svc.Receive(r.Context(), body, ip)
	if err != nil {
		logger.Error().Err(err).Msg("ingest receive internal error")
		writeJSON(w, http.StatusInternalServerError, response{Decision: "error", Reason: err.Error()})
		return
	}

	out := response{Decision: res.Decision, Reason: res.Reason, SignalID: res.SignalID}
	switch res.Decision {
	case "invalid":
		writeJSON(w, http.StatusBadRequest, out)
	case "pending":
		// Hand off the slow path. Dispatcher.Submit is non-blocking and
		// guarantees the signal is processed (synchronously as a fallback
		// if the per-strategy queue is full).
		h.dispatcher.Submit(res.StrategyID, res.SignalID)
		writeJSON(w, http.StatusOK, out)
	default: // "duplicate" or any future terminal Receive decision
		writeJSON(w, http.StatusOK, out)
	}
}

// ClientIP extracts the client IP. If behind a trusted reverse proxy that
// sets X-Forwarded-For, you'd configure that elsewhere. For now: prefer
// the rightmost X-Forwarded-For entry (the proxy's caller) only when the
// remote addr is loopback (i.e., we're behind a tunnel like cloudflared);
// otherwise trust RemoteAddr.
func ClientIP(r *http.Request) net.IP {
	host, _, _ := net.SplitHostPort(r.RemoteAddr)
	if ip := net.ParseIP(host); ip != nil && ip.IsLoopback() {
		if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
			parts := splitAndTrim(xff)
			if len(parts) > 0 {
				if ip2 := net.ParseIP(parts[len(parts)-1]); ip2 != nil {
					return ip2
				}
			}
		}
	}
	return net.ParseIP(host)
}

func splitAndTrim(s string) []string {
	out := []string{}
	cur := ""
	for _, c := range s {
		if c == ',' || c == ' ' {
			if cur != "" {
				out = append(out, cur)
				cur = ""
			}
			continue
		}
		cur += string(c)
	}
	if cur != "" {
		out = append(out, cur)
	}
	return out
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
