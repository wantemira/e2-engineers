package httpapi

import (
	"fmt"
	"html/template"
	"log/slog"
	"net/http"
	"strings"

	"github.com/wantemira/e2-engineers/backend/internal/diff"
	"github.com/wantemira/e2-engineers/backend/internal/domain"
)

type diffRow struct {
	Text    string
	Kind    string
	Number  int
	Anchors []string
}

func (h handler) reviewDiff(w http.ResponseWriter, r *http.Request) {
	run, err := h.app.ReviewDiff(requestSession(r).ID, r.PathValue("id"))
	if err != nil {
		writeError(w, err)
		return
	}
	lines, err := diff.Parse(run.Diff)
	if err != nil {
		writeError(w, err)
		return
	}
	type position struct {
		path string
		line int
	}
	anchors := make(map[position][]string)
	for i, finding := range run.Findings {
		if loc := finding.Location; loc != nil {
			key := position{loc.Path, loc.Line}
			anchors[key] = append(anchors[key], fmt.Sprintf("finding-%d", i+1))
		}
	}
	raw := strings.Split(strings.TrimSuffix(run.Diff, "\n"), "\n")
	rows := make([]diffRow, len(raw))
	for i, text := range raw {
		rows[i].Text = strings.TrimSuffix(text, "\r")
		switch {
		case strings.HasPrefix(text, "@@ "):
			rows[i].Kind = "hunk"
		case strings.HasPrefix(text, "-") && !strings.HasPrefix(text, "--- "):
			rows[i].Kind = "deleted"
		}
	}
	for _, line := range lines {
		key := position{line.Path, line.Number}
		row := &rows[line.Index]
		row.Number, row.Anchors = line.Number, anchors[key]
		delete(anchors, key)
		if line.Added {
			row.Kind = "added"
		}
	}

	page := struct {
		Review domain.ReviewRun
		Rows   []diffRow
	}{run, rows}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := diffTemplate.Execute(w, page); err != nil {
		slog.Error("render diff", "error", err)
	}
}

var diffTemplate = template.Must(template.New("diff").Parse(`<!doctype html>
<html lang="ru"><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1">
<title>Diff PR #{{.Review.PR.Number}} · AI-Buddy</title>
<style>
body{margin:24px auto;padding:0 16px;max-width:1050px;color:#141414;font:16px/1.5 system-ui,sans-serif}
pre{overflow:auto;border:1px solid #dedde3;border-radius:8px;font:14px/1.6 monospace}
.row{display:block;min-width:max-content;padding-right:16px}
.number{display:inline-block;width:5ch;margin-right:1ch;text-align:right;color:#575461;user-select:none}
.added{background:#ebf8eb}.deleted{background:#fcefee}.hunk{background:#f1edfb}
.row:has(:target){background:#ffedaa;outline:2px solid #996600;outline-offset:-2px}
.anchor{scroll-margin-top:24px}
</style>
<main><h1>Diff PR #{{.Review.PR.Number}}</h1>
<p>{{.Review.PR.Title}} · <code>{{.Review.HeadSHA}}</code></p>
<pre aria-label="Изменения PR">{{range .Rows}}<span class="row {{.Kind}}">{{range .Anchors}}<span class="anchor" id="{{.}}"></span>{{end}}<span class="number" aria-hidden="true">{{if .Number}}{{.Number}}{{end}}</span>{{.Text}}</span>{{end}}</pre></main></html>`))
