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
	Title     string `json:"title"`
	URL       string `json:"url"`
	Impact    string `json:"impact"`
	Direction string `json:"direction"` // bullish | bearish | mixed | neutral
	Reason    string `json:"reason"`
}

// Classification is the structured + audit output of one news pass.
type Classification struct {
	Impact           string
	Direction        string // bullish | bearish | mixed | neutral —— 整体方向
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

const promptTemplate = `你是一名加密货币市场分析师,同时熟悉全球宏观金融市场。下面是过去 1 小时内从加密原生媒体 (CoinDesk) 和宏观财经媒体 (MarketWatch / CNBC / Yahoo Finance) 聚合的
{{len .Headlines}} 条新闻标题。请对每一条单独评级,并综合给出未来 1-4h 的整体影响判断。

注意:列表里**可能包含非加密原生的宏观新闻** (美股大幅波动、外资撤离亚洲 ETF、CPI/利率决议、美元指数异动、地缘政治等)。请评估这些宏观新闻对加密市场的**间接传导影响**:
- 全球 risk-off 信号 (重大 ETF 单周史上级别撤资、股市恐慌、地缘冲突升级) → 加密通常同步偏空
- 美元强势 / 利率上行 / 通胀超预期 → 加密偏空
- 风险偏好回升 / 美联储鸽派 / 大型科技股大涨 → 加密偏多

新闻列表:
{{range $i, $h := .Headlines}}
[{{$i}}] {{$h.Title}}
    source: {{$h.Source}}
    url:    {{$h.URL}}
{{end}}

请严格返回以下 JSON, 不要包含任何额外文字、解释或代码块标记:
{
  "impact":    "high" | "medium" | "low" | "none",
  "direction": "bullish" | "bearish" | "mixed" | "neutral",
  "summary":   "<≤200 字, 用中文概括整体短线影响>",
  "reasoning": "<≤500 字, 综合下述子评级解释为什么整体定为这个等级, 引用具体编号>",
  "per_headline": [
    {"index": <对应上面列表的编号>, "impact": "high|medium|low|none", "direction": "bullish|bearish|mixed|neutral", "reason": "<≤80 字>"},
    ...
  ]
}

要求:
- per_headline 必须覆盖列表里的每一条 (不能漏)。
- 整体 impact 的逻辑参考: 任一 high → high; 否则 ≥2 medium → medium; 否则 ≥1 medium → low;
  全 low/none → low; 列表为空 → none。可根据语境微调,并在 reasoning 中说明。
- direction 表示对加密市场的方向性影响 (不是新闻本身的好坏):
  - bullish: 显著利好加密 (机构入场、宽松货币、风险偏好上行、合规破冰等)
  - bearish: 显著利空加密 (重大监管、ETF 撤资、紧缩货币、地缘风险升级、黑客等)
  - mixed:   利好利空并存且重要性相当,方向不明
  - neutral: 主要是信息/技术性更新,无明确方向

判断标准 (单条):
- high:   重大监管/合规事件 (SEC 起诉、ETF 决议)、大型黑客盗窃、稳定币脱锚、交易所暴雷、
          关键人物消息 (鲍威尔、马斯克级别)、协议层重大事故、
          跨市场 risk-off 大事件 (主要 ETF 单周史上级别撤资、CPI 严重超预期、美联储紧急行动、主要国家股市断崖)
- medium: 主要项目大额融资/合作、宏观数据强不及预期、链上大额转账被监测、
          较大规模的 ETF 资金流向变化、美元指数大幅异动
- low:    普通项目发布、市场分析观点、价格预测、技术指标更新
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
	Direction   string `json:"direction"`
	Summary     string `json:"summary"`
	Reasoning   string `json:"reasoning"`
	PerHeadline []struct {
		Index     int    `json:"index"`
		Impact    string `json:"impact"`
		Direction string `json:"direction"`
		Reason    string `json:"reason"`
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
		Model:  c.model,
		Prompt: promptText,
		// MaxTokens 估算: 12 条 per_headline × ~120 token + reasoning 500 字 ≈ 600
		// token + summary 200 字 ≈ 250 token + 框架 ~50 = ~2350。留余量到 3000。
		MaxTokens: 3000,
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
	out.Direction = normalizeDirection(parsed.Direction)
	out.Summary = parsed.Summary
	out.Reasoning = parsed.Reasoning
	out.PerHeadline = make([]HeadlineJudgment, len(parsed.PerHeadline))
	for i, p := range parsed.PerHeadline {
		idx := p.Index
		if idx < 0 || idx >= len(headlines) {
			return out, fmt.Errorf("per_headline[%d].index out of range: %d", i, idx)
		}
		out.PerHeadline[i] = HeadlineJudgment{
			Title:     headlines[idx].Title,
			URL:       headlines[idx].URL,
			Impact:    p.Impact,
			Direction: normalizeDirection(p.Direction),
			Reason:    p.Reason,
		}
	}
	out.PerHeadlineJSON, _ = json.Marshal(out.PerHeadline)
	return out, nil
}

// normalizeDirection 规范 LLM 输出的方向字段。
// 接受同义词 (positive/negative/long/short/neutral/mixed/none),
// 缺省或不识别时返回 "neutral"。
func normalizeDirection(s string) string {
	v := strings.ToLower(strings.TrimSpace(s))
	switch v {
	case "bullish", "bull", "positive", "long", "利好", "看涨":
		return "bullish"
	case "bearish", "bear", "negative", "short", "利空", "看跌":
		return "bearish"
	case "mixed", "split", "divergent", "分歧", "混合":
		return "mixed"
	case "neutral", "none", "n/a", "中性":
		return "neutral"
	}
	return "neutral"
}

func hashPrompt(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:4])
}
