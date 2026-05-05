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

type Telegram struct {
	BaseURL string // e.g. "https://api.telegram.org" (override in tests)
	Token   string
	ChatID  string
	Client  *http.Client
}

func NewTelegram(baseURL, token, chatID string) *Telegram {
	if baseURL == "" {
		baseURL = "https://api.telegram.org"
	}
	return &Telegram{
		BaseURL: baseURL, Token: token, ChatID: chatID,
		Client: &http.Client{Timeout: 5 * time.Second},
	}
}

type tgReq struct {
	ChatID    string `json:"chat_id"`
	Text      string `json:"text"`
	ParseMode string `json:"parse_mode"`
}

type tgResp struct {
	OK          bool   `json:"ok"`
	ErrorCode   int    `json:"error_code"`
	Description string `json:"description"`
}

func (tg *Telegram) Send(ctx context.Context, m Message) error {
	var b strings.Builder
	if m.Severity != "" {
		b.WriteString("[" + strings.ToUpper(string(m.Severity)) + "] ")
	}
	b.WriteString("*" + m.Title + "*")
	if m.Body != "" {
		b.WriteString("\n")
		b.WriteString(m.Body)
	}
	if len(m.Fields) > 0 {
		b.WriteString("\n")
		for k, v := range m.Fields {
			fmt.Fprintf(&b, "`%s`: %v\n", k, v)
		}
	}

	payload, _ := json.Marshal(tgReq{ChatID: tg.ChatID, Text: b.String(), ParseMode: "Markdown"})
	url := fmt.Sprintf("%s/bot%s/sendMessage", strings.TrimRight(tg.BaseURL, "/"), tg.Token)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(payload))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := tg.Client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("telegram http %d: %s", resp.StatusCode, string(body))
	}
	var tr tgResp
	if err := json.Unmarshal(body, &tr); err != nil {
		return fmt.Errorf("telegram decode: %w", err)
	}
	if !tr.OK {
		return fmt.Errorf("telegram code=%d desc=%s", tr.ErrorCode, tr.Description)
	}
	return nil
}
