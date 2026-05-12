package notify

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestWecom_SendSuccess(t *testing.T) {
	var gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		_, _ = w.Write([]byte(`{"errcode":0,"errmsg":"ok"}`))
	}))
	defer srv.Close()

	wc := NewWecom(srv.URL)
	wc.Client = srv.Client()
	err := wc.Send(context.Background(), Message{
		Title:    "News HIGH",
		Body:     "Impact高: SEC sues XYZ",
		Severity: SeverityWarn,
		Fields:   map[string]any{"impact": "high", "headlines": 5},
	})
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	var parsed map[string]any
	if err := json.Unmarshal([]byte(gotBody), &parsed); err != nil {
		t.Fatalf("server got malformed JSON: %v", err)
	}
	if parsed["msgtype"] != "markdown" {
		t.Errorf("msgtype: %v", parsed["msgtype"])
	}
	if md, ok := parsed["markdown"].(map[string]any); !ok || !strings.Contains(md["content"].(string), "News HIGH") {
		t.Errorf("markdown content: %+v", parsed["markdown"])
	}
}

func TestWecom_SendErrcodeNonZero(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"errcode":93000,"errmsg":"invalid webhook"}`))
	}))
	defer srv.Close()
	wc := NewWecom(srv.URL)
	wc.Client = srv.Client()
	err := wc.Send(context.Background(), Message{Title: "x"})
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "93000") {
		t.Errorf("err: %v", err)
	}
}

func TestWecom_SendHTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "down", http.StatusInternalServerError)
	}))
	defer srv.Close()
	wc := NewWecom(srv.URL)
	wc.Client = srv.Client()
	err := wc.Send(context.Background(), Message{Title: "x"})
	if err == nil {
		t.Fatal("expected error")
	}
}
