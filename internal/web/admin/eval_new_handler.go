package admin

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"strings"
	"text/template"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/lizhaojie/tvbot/internal/agent/scorer"
	"github.com/lizhaojie/tvbot/internal/eval"
)

// EvalNewHandler owns the three routes that create a new replay run:
//   - GET  /eval/replays/new         → blank form
//   - POST /eval/replays/preview     → validates + cost estimate + confirm page
//   - POST /eval/replays             → INSERTs status='pending'; 302 to detail page
//
// All form values flow through the hidden-field-preserves-input pattern;
// there's no server-side draft store.
type EvalNewHandler struct {
	render  *Renderer
	pool    *pgxpool.Pool
	store   *eval.Store
	statusH *StatusHandler
}

func NewEvalNewHandler(r *Renderer, pool *pgxpool.Pool) *EvalNewHandler {
	var st *eval.Store
	if pool != nil {
		st = eval.NewStore(pool)
	}
	return &EvalNewHandler{render: r, pool: pool, store: st}
}

func (h *EvalNewHandler) WithStatus(s *StatusHandler) *EvalNewHandler {
	h.statusH = s
	return h
}

// GetNew renders the blank new-replay form.
func (h *EvalNewHandler) GetNew(w http.ResponseWriter, r *http.Request) {
	data := map[string]any{
		"SinceOpts":    eval.AllowedSinces,
		"Models":       scorer.SupportedModels,
		"DefaultModel": scorer.DefaultModel,
	}
	if h.statusH != nil {
		data = h.statusH.WithStatus(r, data)
	}
	h.render.Render(w, http.StatusOK, "eval/replay_new", data)
}

// previewInput is the validated form state passed to the preview page (and
// re-posted from there as hidden fields when the user confirms).
type previewInput struct {
	Since        string
	MaxN         int
	Concurrency  int
	Model        string
	PromptText   string
	PromptName   string
	PromptSHA256 string
}

// parseForm validates the multipart form and returns either a previewInput
// or a user-facing error string suitable for the form's red banner.
func (h *EvalNewHandler) parseForm(r *http.Request) (previewInput, string) {
	// 10 MB cap on the prompt upload — far larger than any reasonable .tmpl.
	if err := r.ParseMultipartForm(10 << 20); err != nil {
		return previewInput{}, "解析表单失败: " + err.Error()
	}
	in := previewInput{
		Since:      strings.TrimSpace(r.FormValue("since")),
		Model:      strings.TrimSpace(r.FormValue("model")),
		PromptText: r.FormValue("prompt_text"),
		PromptName: strings.TrimSpace(r.FormValue("prompt_name")),
	}
	fmt.Sscanf(r.FormValue("max_n"), "%d", &in.MaxN)
	fmt.Sscanf(r.FormValue("concurrency"), "%d", &in.Concurrency)

	if _, ok := eval.ParseSince(in.Since); !ok {
		return previewInput{}, "时间窗 " + in.Since + " 不在允许列表 " + strings.Join(eval.AllowedSinces, ",")
	}
	if in.MaxN < 0 {
		return previewInput{}, "最多 N 条不可为负"
	}
	if in.Concurrency < 1 {
		in.Concurrency = 1
	}
	if in.Concurrency > 10 {
		in.Concurrency = 10
	}
	if !scorer.IsSupportedModel(in.Model) {
		return previewInput{}, "模型 " + in.Model + " 不在支持列表"
	}

	// File takes precedence over textarea.
	file, header, err := r.FormFile("prompt_file")
	if err == nil {
		defer file.Close()
		body, rerr := io.ReadAll(file)
		if rerr != nil {
			return previewInput{}, "读取上传文件失败: " + rerr.Error()
		}
		in.PromptText = string(body)
		if in.PromptName == "" && header != nil {
			in.PromptName = header.Filename
		}
	}
	if strings.TrimSpace(in.PromptText) == "" {
		return previewInput{}, "Prompt 文件和粘贴内容不能都为空"
	}

	// Syntax check on the template.
	if _, terr := template.New("p").Parse(in.PromptText); terr != nil {
		return previewInput{}, "Prompt 模板语法错误: " + terr.Error()
	}

	if in.PromptName == "" {
		in.PromptName = "手输-" + time.Now().Format("20060102-1504")
	}
	if len(in.PromptName) > 64 {
		in.PromptName = in.PromptName[:64]
	}

	sha := sha256.Sum256([]byte(in.PromptText))
	in.PromptSHA256 = hex.EncodeToString(sha[:])
	return in, ""
}

// PostPreview validates the submission and renders the confirm page with
// cost estimate. On validation error renders the form again with .Error.
func (h *EvalNewHandler) PostPreview(w http.ResponseWriter, r *http.Request) {
	in, formErr := h.parseForm(r)
	if formErr != "" {
		data := map[string]any{
			"SinceOpts":    eval.AllowedSinces,
			"Models":       scorer.SupportedModels,
			"DefaultModel": scorer.DefaultModel,
			"Error":        formErr,
		}
		if h.statusH != nil {
			data = h.statusH.WithStatus(r, data)
		}
		h.render.Render(w, http.StatusUnprocessableEntity, "eval/replay_new", data)
		return
	}

	ctx, cancel := withTimeout(r)
	defer cancel()
	est, costErr := eval.EstimateCost(ctx, h.pool, in.Since, in.Model)

	data := map[string]any{
		"In":          in,
		"Cost":        est,
		"CostErr":     costErrToString(costErr),
		"PromptPrev":  promptPreview(in.PromptText),
		"PromptLines": strings.Count(in.PromptText, "\n") + 1,
	}
	if h.statusH != nil {
		data = h.statusH.WithStatus(r, data)
	}
	h.render.Render(w, http.StatusOK, "eval/replay_preview", data)
}

// PostCreate is the final submit: re-validates (don't trust hidden fields
// blindly), INSERTs status='pending', 302 to /eval/replays/:id.
func (h *EvalNewHandler) PostCreate(w http.ResponseWriter, r *http.Request) {
	in, formErr := h.parseForm(r)
	if formErr != "" {
		data := map[string]any{
			"SinceOpts":    eval.AllowedSinces,
			"Models":       scorer.SupportedModels,
			"DefaultModel": scorer.DefaultModel,
			"Error":        formErr,
		}
		if h.statusH != nil {
			data = h.statusH.WithStatus(r, data)
		}
		h.render.Render(w, http.StatusUnprocessableEntity, "eval/replay_new", data)
		return
	}

	ctx, cancel := withTimeout(r)
	defer cancel()
	cutoff, _ := eval.ParseSince(in.Since)

	id, err := h.store.CreateRun(ctx, eval.ReplayRun{
		SinceWindow:  in.Since,
		SinceCutoff:  cutoff.Unix(),
		MaxN:         in.MaxN,
		Concurrency:  in.Concurrency,
		Model:        in.Model,
		PromptText:   in.PromptText,
		PromptName:   &in.PromptName,
		PromptSHA256: in.PromptSHA256,
		Status:       "pending",
	})
	if err != nil {
		http.Error(w, "create run: "+err.Error(), http.StatusInternalServerError)
		return
	}

	http.Redirect(w, r, fmt.Sprintf("/eval/replays/%d", id), http.StatusSeeOther)
}

// promptPreview returns the first ~500 chars of the prompt for the confirm
// page's "you submitted this" sanity-check display.
func promptPreview(p string) string {
	const lim = 500
	if len(p) <= lim {
		return p
	}
	return p[:lim] + "…"
}

func costErrToString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}
