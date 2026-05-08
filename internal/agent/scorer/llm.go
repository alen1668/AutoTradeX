package scorer

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// LLMClient is the minimal contract for talking to a chat-completion API.
// scorer's unit tests use a fake; the production binding is the
// AnthropicClient below.
type LLMClient interface {
	Complete(ctx context.Context, req CompleteRequest) (CompleteResponse, error)
}

type CompleteRequest struct {
	Model     string
	Prompt    string
	MaxTokens int
}

type CompleteResponse struct {
	Text     string
	TokenIn  int
	TokenOut int
}

const (
	defaultAnthropicURL = "https://api.anthropic.com"
	anthropicVersion    = "2023-06-01"
)

type anthropicClient struct {
	apiKey  string
	baseURL string
	http    *http.Client
}

// NewAnthropicClient returns an LLMClient that POSTs to /v1/messages.
// baseURL empty → use api.anthropic.com. The 30 s http.Client timeout is
// a safety net only; the upstream context deadline (5 s default from
// settings) is the actual timeout.
//
// We deliberately don't import the official SDK — one less dependency to
// vendor and the message API surface is small enough.
func NewAnthropicClient(apiKey, baseURL string) LLMClient {
	if baseURL == "" {
		baseURL = defaultAnthropicURL
	}
	return &anthropicClient{
		apiKey:  apiKey,
		baseURL: baseURL,
		http:    &http.Client{Timeout: 30 * time.Second},
	}
}

type anthropicReq struct {
	Model     string `json:"model"`
	MaxTokens int    `json:"max_tokens"`
	Messages  []msg  `json:"messages"`
}

type msg struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type anthropicResp struct {
	Content []contentBlock `json:"content"`
	Usage   usage          `json:"usage"`
}

type contentBlock struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type usage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
}

func (c *anthropicClient) Complete(ctx context.Context, req CompleteRequest) (CompleteResponse, error) {
	body, _ := json.Marshal(anthropicReq{
		Model:     req.Model,
		MaxTokens: req.MaxTokens,
		Messages:  []msg{{Role: "user", Content: req.Prompt}},
	})
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost,
		c.baseURL+"/v1/messages", bytes.NewReader(body))
	if err != nil {
		return CompleteResponse{}, fmt.Errorf("build request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("x-api-key", c.apiKey)
	httpReq.Header.Set("anthropic-version", anthropicVersion)

	resp, err := c.http.Do(httpReq)
	if err != nil {
		return CompleteResponse{}, fmt.Errorf("anthropic call: %w", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 400 {
		return CompleteResponse{}, fmt.Errorf("anthropic %d: %s", resp.StatusCode, truncate(string(raw), 200))
	}
	var ar anthropicResp
	if err := json.Unmarshal(raw, &ar); err != nil {
		return CompleteResponse{}, fmt.Errorf("anthropic parse: %w (body=%s)", err, truncate(string(raw), 200))
	}
	if len(ar.Content) == 0 {
		return CompleteResponse{}, fmt.Errorf("anthropic: empty content (body=%s)", truncate(string(raw), 200))
	}
	return CompleteResponse{
		Text:     ar.Content[0].Text,
		TokenIn:  ar.Usage.InputTokens,
		TokenOut: ar.Usage.OutputTokens,
	}, nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
