# Prompt Template 字段契约

模板渲染上下文是 `promptCtx`（见 `internal/agent/scorer/prompt.go`）。本文档列出
所有可在 `templates/*.tmpl` 模板里引用的字段及类型。

**修改 `ScoreInput` 或 `promptCtx` 时必须同步更新此文件。**

## 顶层字段

| 表达式 | 类型 | 说明 |
|---|---|---|
| `{{.StrategyID}}` | string | 策略 ID（来自 `Strategy.Config.ID`） |
| `{{.Signal.Symbol}}` | string | 交易对，如 "ETHUSDC" |
| `{{.Signal.Kind}}` | string | "long" 或 "short" |
| `{{.Signal.Price}}` | string | 信号价格（已格式化） |
| `{{.SignalTimeUTC}}` | string | 信号时间，格式 "2006-01-02 15:04:05"（UTC） |
| `{{.Input}}` | ScoreInput | 完整输入结构，子字段见下 |

## .Input 子字段

### 历史交易 (按时间倒序)

```
{{range .Input.SymbolHistory -}}
- {{.OpenedAt.UTC.Format "2006-01-02 15:04"}} {{.Direction}} 入 {{.EntryPrice}} 出 {{.ExitPrice}} ({{.ExitReason}}) PnL=${{.PnLUSD}} 持仓 {{.DurationMin}}分钟
{{end}}

{{range .Input.StrategyHistory -}}
- {{.OpenedAt.UTC.Format "2006-01-02 15:04"}} {{.Symbol}} {{.Direction}} 入 {{.EntryPrice}} 出 {{.ExitPrice}} ({{.ExitReason}}) PnL=${{.PnLUSD}}
{{end}}
```

每条 `HistoricalTrade` 字段：

| 字段 | 类型 | 说明 |
|---|---|---|
| `.OpenedAt` | time.Time | 开仓时间，可链 `.UTC.Format "..."` |
| `.Symbol` | string | 仅在 StrategyHistory 里有意义 |
| `.Direction` | string | "long" \| "short" |
| `.EntryPrice` | decimal.Decimal | |
| `.ExitPrice` | decimal.Decimal | |
| `.PnLUSD` | decimal.Decimal | 已实现盈亏（USDC） |
| `.DurationMin` | int | 持仓分钟数 |
| `.ExitReason` | string | "tp" \| "sl" \| "manual" \| "reverse" |

### 当前 portfolio (可能为 nil)

```
{{if .Input.Portfolio -}}
总名义值: ${{.Input.Portfolio.TotalNotionalUSD}}
当日已实现盈亏: ${{.Input.Portfolio.DailyPnLUSD}}
{{range .Input.Portfolio.OpenPositions -}}
- {{.StrategyID}} {{.Symbol}} {{.Direction}} 名义${{.NotionalUSD}} 浮动盈亏${{.UnrealizedPnL}}
{{end -}}
{{else}}仓位数据暂不可用
{{end}}
```

`PortfolioSnapshot` 字段：

| 字段 | 类型 |
|---|---|
| `.TotalNotionalUSD` | decimal.Decimal |
| `.DailyPnLUSD` | decimal.Decimal |
| `.OpenPositions` | []OpenPosition (含 `.StrategyID/.Symbol/.Direction/.EntryPrice/.NotionalUSD/.UnrealizedPnL`) |

### 当前市场状态 (可能为 nil)

```
{{if .Input.Market -}}
24h 区间: {{.Input.Market.Last24hLow}} ~ {{.Input.Market.Last24hHigh}}
当前价相对区间位置: {{.Input.Market.PriceVs24hRange}} (0=最低, 1=最高)
24h 涨跌: {{.Input.Market.Last24hChangePct}}%   1h 涨跌: {{.Input.Market.Last1hChangePct}}%
24h 波动率: {{.Input.Market.Volatility24h}}
最近 24 根 1h 收盘价: {{.Input.Market.KlineLookback1h}}
{{else}}市场数据暂不可用
{{end}}
```

`MarketContext` 字段：

| 字段 | 类型 |
|---|---|
| `.Symbol` | string |
| `.Last24hHigh` / `.Last24hLow` | decimal.Decimal |
| `.Last24hChangePct` / `.Last1hChangePct` | decimal.Decimal |
| `.PriceVs24hRange` | decimal.Decimal (0..1) |
| `.Volatility24h` | decimal.Decimal |
| `.KlineLookback1h` | []decimal.Decimal |

### 高波动时段

`{{.Input.HighVolWindows}}` — `[]string`，可能为空 slice。当前可能值: `us_data_release_window` / `us_market_open_window` / `weekend_gap_window`。

## 允许使用的 template action

仅 Go `text/template` 内置 action：`{{.field}}`、`{{range}}`、`{{if}}`、`{{with}}`、`{{end}}`、`{{len}}`、方法调用如 `{{.OpenedAt.UTC.Format "..."}}`。

**禁止注册自定义 `template.Funcs`** —— 模板必须能被纯模板引擎解释，不依赖任何 host 函数。所有复杂转换前移到 `RenderPrompt` / `ScoreInput` 构造阶段。

## 输出契约 (LLM 必须返回)

Prompt 末尾必须要求严格 JSON 输出（允许被 markdown fences 或 preamble 包裹，`ExtractJSON` 会容错）：

```json
{"score": <0-100整数>, "decision": "approve" 或 "abandon", "reasoning": "<≤300字理由>"}
```

`score` 不在 [0,100] / `decision` 不是 approve|abandon / 缺字段 → 视为 LLM failure。
