package notify

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

type Feishu struct {
	URL    string
	Client *http.Client
}

func NewFeishu(url string) *Feishu {
	return &Feishu{URL: url, Client: &http.Client{Timeout: 5 * time.Second}}
}

type feishuReq struct {
	MsgType string         `json:"msg_type"`
	Content map[string]any `json:"content"`
}

type feishuResp struct {
	Code int    `json:"code"`
	Msg  string `json:"msg"`
}

func (f *Feishu) Send(ctx context.Context, m Message) error {
	var b strings.Builder
	if m.Severity != "" {
		b.WriteString("[" + strings.ToUpper(string(m.Severity)) + "] ")
	}
	b.WriteString(m.Title)
	if m.Body != "" {
		b.WriteString("\n")
		b.WriteString(m.Body)
	}
	if len(m.Fields) > 0 {
		b.WriteString("\n")
		for k, v := range m.Fields {
			fmt.Fprintf(&b, "%s: %v\n", k, v)
		}
	}

	payload, _ := json.Marshal(feishuReq{
		MsgType: "text",
		Content: map[string]any{"text": b.String()},
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, f.URL, bytes.NewReader(payload))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := f.Client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("feishu http %d: %s", resp.StatusCode, string(body))
	}
	var fr feishuResp
	if err := json.Unmarshal(body, &fr); err != nil {
		return fmt.Errorf("feishu decode: %w", err)
	}
	if fr.Code != 0 {
		return fmt.Errorf("feishu code=%d msg=%s", fr.Code, fr.Msg)
	}
	return nil
}
