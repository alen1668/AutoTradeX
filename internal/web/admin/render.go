package admin

import (
	"bytes"
	"embed"
	"fmt"
	"html/template"
	"io/fs"
	"net/http"
	"strings"
)

//go:embed templates
var templatesFS embed.FS

// Renderer parses templates once at construction; serves them by page name.
type Renderer struct {
	templates map[string]*template.Template
}

func NewRenderer() (*Renderer, error) {
	r := &Renderer{templates: map[string]*template.Template{}}

	// Walk the pages directory recursively to find all HTML files.
	// fs.Glob with "**" does NOT work across directories in embed.FS,
	// so we use fs.WalkDir instead.
	var pages []string
	err := fs.WalkDir(templatesFS, "templates/pages", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() && strings.HasSuffix(path, ".html") {
			pages = append(pages, path)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}

	// Collect partials — optional: if the directory is empty or missing, skip.
	partials, _ := fs.Glob(templatesFS, "templates/partials/*.html")

	for _, p := range pages {
		name, err := pageName(p)
		if err != nil {
			return nil, err
		}
		layout := pickLayout(name)

		// Start with the layout file.
		patterns := []string{"templates/layouts/" + layout + ".html"}
		// Add partials only if any exist.
		if len(partials) > 0 {
			patterns = append(patterns, partials...)
		}
		// Add the page itself.
		patterns = append(patterns, p)

		t, err := template.New("base").Funcs(funcMap()).ParseFS(templatesFS, patterns...)
		if err != nil {
			return nil, fmt.Errorf("parse %s: %w", p, err)
		}
		r.templates[name] = t
	}
	return r, nil
}

func pageName(path string) (string, error) {
	clean := strings.TrimPrefix(path, "templates/pages/")
	clean = strings.TrimSuffix(clean, ".html")
	// Normalize path separators (embed uses forward slashes on all platforms).
	clean = strings.ReplaceAll(clean, "\\", "/")
	if clean == "" {
		return "", fmt.Errorf("empty page name from %s", path)
	}
	return clean, nil
}

func pickLayout(name string) string {
	if name == "login" {
		return "auth"
	}
	return "base"
}

func funcMap() template.FuncMap {
	return template.FuncMap{
		"json": func(v any) (template.JS, error) {
			// helper for embedding JSON in scripts (kept tiny for MVP)
			return template.JS(fmt.Sprintf("%v", v)), nil
		},
		"sideCN": func(side string) string {
			switch side {
			case "long":
				return "多头"
			case "short":
				return "空头"
			}
			return side
		},
		"closeReasonCN": func(reason string) string {
			switch reason {
			case "signal":
				return "信号平仓"
			case "stop_loss":
				return "止损触发"
			case "take_profit":
				return "止盈触发"
			case "recovery_offline":
				return "离线平仓(恢复)"
			case "manual":
				return "手工平仓"
			}
			return reason
		},
		"kindCN": func(kind string) string {
			switch kind {
			case "long":
				return "开多"
			case "short":
				return "开空"
			case "exit_long":
				return "平多"
			case "exit_short":
				return "平空"
			}
			return kind
		},
		"decisionCN": func(d string) string {
			switch d {
			case "accepted":
				return "接受"
			case "duplicate":
				return "重复"
			case "risk_denied":
				return "风控拒绝"
			case "disarmed":
				return "未启用"
			case "invalid":
				return "无效"
			case "pending":
				return "处理中"
			case "abandoned":
				return "已放弃"
			}
			return d
		},
		"decisionReasonCN": decisionReasonCN,
		"add":              func(a, b int) int { return a + b },
		"sub":              func(a, b int) int { return a - b },
		"agentDecisionCN": func(d string) string {
			switch d {
			case "approve":
				return "通过"
			case "abandon":
				return "拒"
			case "failed":
				return "失败"
			}
			return d
		},
		"derefInt": func(p *int) int {
			if p == nil {
				return 0
			}
			return *p
		},
		"derefStr": func(p *string) string {
			if p == nil {
				return ""
			}
			return *p
		},
		"derefBool": func(p *bool) bool {
			if p == nil {
				return false
			}
			return *p
		},
		// safeURL marks a string as a trusted URL fragment so html/template
		// stops url-encoding it. Used by /signals pagination links —
		// without this `?page=2` becomes `?page%3d2` and the page query
		// parameter never reaches the handler.
		"safeURL": func(s string) template.URL { return template.URL(s) },
	}
}

// decisionReasonCN translates the common DecisionReason values written by
// the ingest pipeline. Free-form parts (binance error messages, rule reasons
// after the prefix) are passed through. Unknown values return as-is.
func decisionReasonCN(reason string) string {
	// Exact matches for short codes.
	switch reason {
	case "":
		return ""
	case "noop":
		return "无操作"
	case "close":
		return "平仓"
	case "open_long":
		return "开多"
	case "open_short":
		return "开空"
	case "close_and_open_long":
		return "反手开多(平空+开多)"
	case "close_and_open_short":
		return "反手开空(平多+开空)"
	case "system not armed":
		return "系统未启用"
	case "strategy disabled":
		return "策略已禁用"
	case "strategy archived":
		return "策略已归档"
	case "secret mismatch":
		return "密钥不匹配"
	}
	// Prefix-translate composite messages so the operator-relevant tail
	// (binance error, rule reason) stays intact.
	for prefix, cn := range map[string]string{
		"unknown signal kind: ": "未知信号类型: ",
		"open failed: ":         "开仓失败: ",
		"close failed: ":        "平仓失败: ",
		"reverse close failed: ": "反手平仓失败: ",
		"reverse open failed: ":  "反手开仓失败: ",
		"load context: ":        "加载上下文失败: ",
		"max_position: ":        "持仓金额超限: ",
		"max_total_leverage: ":  "总杠杆超限: ",
		"max_daily_loss: ":      "日亏达限: ",
	} {
		if len(reason) > len(prefix) && reason[:len(prefix)] == prefix {
			return cn + reason[len(prefix):]
		}
	}
	return reason
}

// Render writes the named page (e.g. "login", "strategies/index") with `data` as ctx.
func (r *Renderer) Render(w http.ResponseWriter, status int, name string, data any) {
	t, ok := r.templates[name]
	if !ok {
		http.Error(w, "template not found: "+name, http.StatusInternalServerError)
		return
	}
	var buf bytes.Buffer
	if err := t.ExecuteTemplate(&buf, "base", data); err != nil {
		http.Error(w, "render: "+err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	_, _ = buf.WriteTo(w)
}

// RenderPartial renders a single named partial (without layout). Useful for HTMX swaps.
func (r *Renderer) RenderPartial(w http.ResponseWriter, name string, data any) error {
	for _, t := range r.templates {
		if t.Lookup(name) != nil {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			return t.ExecuteTemplate(w, name, data)
		}
	}
	return fmt.Errorf("partial not found: %s", name)
}

// ensure embed import is used even if no other file references it
var _ embed.FS
