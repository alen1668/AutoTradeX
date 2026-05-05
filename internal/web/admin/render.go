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
	}
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

// ensure embed import is used even if no other file references it
var _ embed.FS
