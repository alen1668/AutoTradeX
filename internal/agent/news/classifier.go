package news

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"text/template"
	"time"

	"github.com/rs/zerolog"

	"github.com/lizhaojie/tvbot/internal/agent/scorer"
)

// HeadlineJudgment is one entry in PerHeadline (mirrors macrocontext.HeadlineJudgment).
type HeadlineJudgment struct {
	Title  string `json:"title"`
	URL    string `json:"url"`
	Impact string `json:"impact"`
	Reason string `json:"reason"`
}

// Classification is the structured + audit output of one news pass.
type Classification struct {
	Impact           string
	Summary          string
	Reasoning        string
	PerHeadline      []HeadlineJudgment
	PerHeadlineJSON  []byte
	RawHeadlinesJSON []byte
	PromptHash       string
	PromptText       string
	ResponseRaw      string
	LLMModel         string
	LLMTokensIn      int
	LLMTokensOut     int
	LLMLatencyMs     int
	MeasuredAt       time.Time
}

const promptTemplate = `你是一名加密货币市场分析师。下面是过去 1 小时内 cryptopanic 上排名最高的
{{len .Headlines}} 条新闻标题。请对每一条单独评级,并综合给出未来 1-4h 的整体影响判断。

新闻列表:
{{range $i, $h := .Headlines}}
[{{$i}}] {{$h.Title}}
    source: {{$h.Source}}
    url:    {{$h.URL}}
{{end}}

请严格返回以下 JSON, 不要包含任何额外文字、解释或代码块标记:
{
  "impact":    "high" | "medium" | "low" | "none",
  "summary":   "<≤200 字, 用中文概括整体短线影响>",
  "reasoning": "<≤500 字, 综合下述子评级解释为什么整体定为这个等级, 引用具体编号>",
  "per_headline": [
    {"index": <对应上面列表的编号>, "impact": "high|medium|low|none", "reason": "<≤80 字>"},
    ...
  ]
}

要求:
- per_headline 必须覆盖列表里的每一条 (不能漏)。
- 整体 impact 的逻辑参考: 任一 high → high; 否则 ≥2 medium → medium; 否则 ≥1 medium → low;
  全 low/none → low; 列表为空 → none。可根据语境微调,并在 reasoning 中说明。

判断标准 (单条):
- high:   重大监管/合规事件 (SEC 起诉、ETF 决议)、大型黑客盗窃、稳定币脱锚、交易所暴雷、
          关键人物消息 (鲍威尔、马斯克级别)、协议层重大事故
- medium: 主要项目大额融资/合作、宏观数据强不及预期、链上大额转账被监测
- low:    普通项目发布、市场分析观点、价格预测
- none:   无意义内容、纯娱乐、纯营销
`

type Classifier struct {
	llm   scorer.LLMClient
	model string
	log   zerolog.Logger
	tmpl  *template.Template
}

func NewClassifier(llm scorer.LLMClient, model string, log zerolog.Logger) *Classifier {
	return &Classifier{
		llm:   llm,
		model: model,
		log:   log,
		tmpl:  template.Must(template.New("news").Parse(promptTemplate)),
	}
}

type llmResponse struct {
	Impact      string `json:"impact"`
	Summary     string `json:"summary"`
	Reasoning   string `json:"reasoning"`
	PerHeadline []struct {
		Index  int    `json:"index"`
		Impact string `json:"impact"`
		Reason string `json:"reason"`
	} `json:"per_headline"`
}

// Classify returns a structured Classification. On any error (LLM, JSON,
// per_headline coverage), an error is returned. The caller (worker) builds
// a "failure" record from the partial Classification and persists it.
func (c *Classifier) Classify(ctx context.Context, headlines []Headline) (Classification, error) {
	now := time.Now().UTC()
	rawHeadlines := make([]map[string]any, len(headlines))
	for i, h := range headlines {
		rawHeadlines[i] = h.Raw
	}
	rawHeadlinesJSON, _ := json.Marshal(rawHeadlines)
	if rawHeadlinesJSON == nil {
		rawHeadlinesJSON = []byte("[]")
	}

	if len(headlines) == 0 {
		emptyPH, _ := json.Marshal([]HeadlineJudgment{})
		return Classification{
			Impact:           "none",
			Summary:          "",
			Reasoning:        "headlines empty",
			PerHeadline:      []HeadlineJudgment{},
			PerHeadlineJSON:  emptyPH,
			RawHeadlinesJSON: rawHeadlinesJSON,
			PromptHash:       hashPrompt(""),
			PromptText:       "",
			LLMModel:         c.model,
			MeasuredAt:       now,
		}, nil
	}

	var buf bytes.Buffer
	if err := c.tmpl.Execute(&buf, map[string]any{"Headlines": headlines}); err != nil {
		return Classification{}, fmt.Errorf("render prompt: %w", err)
	}
	promptText := buf.String()
	promptHash := hashPrompt(promptText)

	start := time.Now()
	resp, err := c.llm.Complete(ctx, scorer.CompleteRequest{
		Model:     c.model,
		Prompt:    promptText,
		MaxTokens: 1024,
	})
	latency := int(time.Since(start).Milliseconds())
	out := Classification{
		PromptHash:       promptHash,
		PromptText:       promptText,
		ResponseRaw:      resp.Text,
		LLMModel:         c.model,
		LLMTokensIn:      resp.TokenIn,
		LLMTokensOut:     resp.TokenOut,
		LLMLatencyMs:     latency,
		MeasuredAt:       now,
		RawHeadlinesJSON: rawHeadlinesJSON,
	}
	if err != nil {
		return out, fmt.Errorf("llm: %w", err)
	}

	cleaned := strings.TrimSpace(resp.Text)
	cleaned = strings.TrimPrefix(cleaned, "```json")
	cleaned = strings.TrimPrefix(cleaned, "```")
	cleaned = strings.TrimSuffix(cleaned, "```")
	cleaned = strings.TrimSpace(cleaned)

	var parsed llmResponse
	if err := json.Unmarshal([]byte(cleaned), &parsed); err != nil {
		return out, fmt.Errorf("parse response: %w; raw=%q", err, resp.Text)
	}
	if len(parsed.PerHeadline) != len(headlines) {
		return out, fmt.Errorf("per_headline coverage mismatch: got %d, want %d", len(parsed.PerHeadline), len(headlines))
	}
	out.Impact = parsed.Impact
	out.Summary = parsed.Summary
	out.Reasoning = parsed.Reasoning
	out.PerHeadline = make([]HeadlineJudgment, len(parsed.PerHeadline))
	for i, p := range parsed.PerHeadline {
		idx := p.Index
		if idx < 0 || idx >= len(headlines) {
			return out, fmt.Errorf("per_headline[%d].index out of range: %d", i, idx)
		}
		out.PerHeadline[i] = HeadlineJudgment{
			Title:  headlines[idx].Title,
			URL:    headlines[idx].URL,
			Impact: p.Impact,
			Reason: p.Reason,
		}
	}
	out.PerHeadlineJSON, _ = json.Marshal(out.PerHeadline)
	return out, nil
}

func hashPrompt(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:4])
}
