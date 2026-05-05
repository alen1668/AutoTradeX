package admin

import "net/http"

// WithStatus injects current StatusData into a data map so every admin page
// can render the status bar without duplicating the query logic.
// If the status query fails the key is simply omitted (status bar degrades gracefully).
func (h *StatusHandler) WithStatus(r *http.Request, data map[string]any) map[string]any {
	if data == nil {
		data = map[string]any{}
	}
	st, err := h.Build(r)
	if err == nil {
		data["Status"] = st
	}
	return data
}
