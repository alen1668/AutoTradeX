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

// Wecom posts Message to a 企业微信 (WeCom) group bot webhook.
// Endpoint shape: https://qyapi.weixin.qq.com/cgi-bin/webhook/send?key=<KEY>
// Officially supported, free, no quota concerns for normal volume.
type Wecom struct {
	URL    string
	Client *http.Client
}

func NewWecom(url string) *Wecom {
	return &Wecom{URL: url, Client: &http.Client{Timeout: 5 * time.Second}}
}

type wecomReq struct {
	MsgType  string         `json:"msgtype"`
	Markdown map[string]any `json:"markdown,omitempty"`
	Text     map[string]any `json:"text,omitempty"`
}

type wecomResp struct {
	ErrCode int    `json:"errcode"`
	ErrMsg  string `json:"errmsg"`
}

func (f *Wecom) Send(ctx context.Context, m Message) error {
	// Markdown formatting works on WeCom group bots and renders nicely.
	var b strings.Builder
	if m.Severity != "" {
		b.WriteString("**[" + strings.ToUpper(string(m.Severity)) + "]** ")
	}
	b.WriteString(m.Title)
	b.WriteString("\n")
	if m.Body != "" {
		b.WriteString(m.Body)
		b.WriteString("\n")
	}
	if len(m.Fields) > 0 {
		for k, v := range m.Fields {
			fmt.Fprintf(&b, "> %s: %v\n", k, v)
		}
	}

	payload, _ := json.Marshal(wecomReq{
		MsgType:  "markdown",
		Markdown: map[string]any{"content": b.String()},
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, f.URL, bytes.NewReader(payload))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := f.Client.Do(req)
	if err != nil {
		return fmt.Errorf("wecom POST: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("wecom: status %d body=%q", resp.StatusCode, string(body))
	}
	var parsed wecomResp
	if err := json.Unmarshal(body, &parsed); err != nil {
		return fmt.Errorf("wecom decode: %w body=%q", err, string(body))
	}
	if parsed.ErrCode != 0 {
		return fmt.Errorf("wecom errcode=%d errmsg=%q", parsed.ErrCode, parsed.ErrMsg)
	}
	return nil
}
