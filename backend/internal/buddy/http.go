package buddy

import (
	"context"
	"encoding/json"
	"errors"
	"html/template"
	"io"
	"log/slog"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"
)

type sessionKey struct{}

type workspace struct {
	Messages       []Message                `json:"messages"`
	Reviews        []ReviewRun              `json:"reviews"`
	ReviewRequests map[string]ReviewRequest `json:"review_requests"`
}

func NewHTTPHandler(service *Service, gitHandler http.Handler, allowedOrigins []string) http.Handler {
	api := http.NewServeMux()
	api.HandleFunc("GET /api/v1/workspace", func(w http.ResponseWriter, r *http.Request) {
		owner := r.Context().Value(sessionKey{}).(session)
		state, err := service.store.snapshot()
		if err != nil {
			writeError(w, err)
			return
		}
		result := workspace{Messages: state.Messages[owner.ID], Reviews: state.Reviews[owner.ID], ReviewRequests: map[string]ReviewRequest{}}
		if result.Messages == nil {
			result.Messages = []Message{}
		}
		if result.Reviews == nil {
			result.Reviews = []ReviewRun{}
		}
		for _, op := range state.Operations {
			if op.Owner != owner.ID || op.Operation.Result == nil || op.Operation.Result.ReviewRequest == nil {
				continue
			}
			index := slices.IndexFunc(result.Reviews, func(run ReviewRun) bool { return run.ID == op.Command.ReviewRunID })
			if index >= 0 {
				run := result.Reviews[index]
				result.ReviewRequests[run.PR.Repository+"#"+strconv.Itoa(run.PR.Number)] = *op.Operation.Result.ReviewRequest
			}
		}
		writeJSON(w, http.StatusOK, result)
	})
	api.HandleFunc("POST /api/v1/messages", func(w http.ResponseWriter, r *http.Request) {
		var input struct {
			Text string `json:"text"`
		}
		if !decodeBody(w, r, &input) {
			return
		}
		input.Text = strings.TrimSpace(input.Text)
		if input.Text == "" || utf8.RuneCountInString(input.Text) > 8000 {
			writeError(w, Problem("INVALID_INPUT", "Сообщение должно содержать от 1 до 8000 символов."))
			return
		}
		submitCommand(w, r, service, Command{Kind: "message", Text: input.Text})
	})
	api.HandleFunc("POST /api/v1/self-reviews", func(w http.ResponseWriter, r *http.Request) {
		var input struct {
			PRRef string `json:"pr_ref"`
		}
		if !decodeBody(w, r, &input) {
			return
		}
		input.PRRef = strings.TrimSpace(input.PRRef)
		if input.PRRef == "" || len(input.PRRef) > 2048 {
			writeError(w, Problem("INVALID_INPUT", "Укажите номер или ссылку на PR."))
			return
		}
		submitCommand(w, r, service, Command{Kind: "review", PRRef: input.PRRef})
	})
	api.HandleFunc("POST /api/v1/review-requests", func(w http.ResponseWriter, r *http.Request) {
		var input struct {
			ReviewRunID string `json:"review_run_id"`
			MentorID    string `json:"expected_mentor_id"`
		}
		if !decodeBody(w, r, &input) {
			return
		}
		if input.ReviewRunID == "" || input.MentorID == "" || len(input.ReviewRunID) > 200 || len(input.MentorID) > 200 {
			writeError(w, Problem("INVALID_INPUT", "Нужны актуальная проверка и наставник."))
			return
		}
		submitCommand(w, r, service, Command{Kind: "request", ReviewRunID: input.ReviewRunID, ExpectedMentorID: input.MentorID})
	})
	api.HandleFunc("GET /api/v1/operations/{id}", func(w http.ResponseWriter, r *http.Request) {
		owner := r.Context().Value(sessionKey{}).(session)
		op, err := service.Operation(owner.ID, r.PathValue("id"))
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, op)
	})
	api.HandleFunc("POST /api/v1/operations/{id}/retry", func(w http.ResponseWriter, r *http.Request) {
		var input struct{}
		if !decodeBody(w, r, &input) {
			return
		}
		key, err := idempotencyKey(r)
		if err != nil {
			writeError(w, err)
			return
		}
		owner := r.Context().Value(sessionKey{}).(session)
		if err := service.Retry(owner.ID, r.PathValue("id"), key); err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusAccepted, map[string]string{"operation_id": r.PathValue("id")})
	})
	api.HandleFunc("GET /api/v1/context/documents/{id}", func(w http.ResponseWriter, r *http.Request) {
		team, err := LoadTeam(service.root)
		if err != nil {
			writeError(w, err)
			return
		}
		index := slices.IndexFunc(team.Documents, func(document Document) bool { return document.ID == r.PathValue("id") })
		if index < 0 {
			writeError(w, Problem("NOT_FOUND", "Документ недоступен."))
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		if err := documentTemplate.Execute(w, documentPage(team.Documents[index], team.Rules)); err != nil {
			slog.Error("render document", "error", err)
		}
	})

	mux := http.NewServeMux()
	mux.Handle("/internal/mcp", gitHandler)
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})
	mux.Handle("/api/", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && r.Header.Get("Origin") != "" {
			origin := r.Header.Get("Origin")
			scheme := "http"
			if r.TLS != nil {
				scheme = "https"
			}
			if origin != scheme+"://"+r.Host && !slices.Contains(allowedOrigins, origin) {
				writeError(w, Problem("FORBIDDEN", "Источник запроса недоступен."))
				return
			}
		}
		token := ""
		if cookie, err := r.Cookie("ai_buddy_session"); err == nil {
			token = cookie.Value
		}
		owner, newToken, err := service.session(token)
		if err != nil {
			writeError(w, err)
			return
		}
		if newToken != "" {
			http.SetCookie(w, &http.Cookie{Name: "ai_buddy_session", Value: newToken, Path: "/api", HttpOnly: true,
				SameSite: http.SameSiteLaxMode, Secure: r.TLS != nil, MaxAge: 60 * 60 * 24 * 30})
		}
		api.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), sessionKey{}, owner)))
	}))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Cache-Control", "no-store")
		mux.ServeHTTP(w, r)
	})
}

func decodeBody(w http.ResponseWriter, r *http.Request, result any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(result); err != nil {
		writeError(w, Problem("INVALID_INPUT", "Некорректное тело запроса."))
		return false
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		writeError(w, Problem("INVALID_INPUT", "Ожидается один JSON-объект."))
		return false
	}
	return true
}

func idempotencyKey(r *http.Request) (string, error) {
	key := strings.TrimSpace(r.Header.Get("Idempotency-Key"))
	if key == "" || len(key) > 200 {
		return "", Problem("INVALID_INPUT", "Нужен ключ операции Idempotency-Key.")
	}
	return key, nil
}

func submitCommand(w http.ResponseWriter, r *http.Request, service *Service, command Command) {
	key, err := idempotencyKey(r)
	if err != nil {
		writeError(w, err)
		return
	}
	owner := r.Context().Value(sessionKey{}).(session)
	id, err := service.Submit(owner, key, command)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]string{"operation_id": id})
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(value); err != nil {
		slog.Error("write response", "error", err)
	}
}

func writeError(w http.ResponseWriter, err error) {
	var problem *AppError
	status := http.StatusInternalServerError
	if errors.As(err, &problem) {
		switch problem.Code {
		case "INVALID_INPUT":
			status = http.StatusBadRequest
		case "NOT_FOUND":
			status = http.StatusNotFound
		case "FORBIDDEN":
			status = http.StatusForbidden
		case "STALE_HEAD", "INVALID_STATE", "IDEMPOTENCY_CONFLICT":
			status = http.StatusConflict
		}
	} else {
		slog.Error("request failed", "error", err)
		problem = applicationError(err)
	}
	writeJSON(w, status, problem)
}

type documentSection struct {
	ID    string
	Title string
	Body  string
}

func documentPage(document Document, rules map[string]Rule) struct {
	Title    string
	Sections []documentSection
} {
	page := struct {
		Title    string
		Sections []documentSection
	}{Title: document.Title}
	intro, rest, _ := strings.Cut(document.Body, "\n## ")
	if strings.HasPrefix(intro, "# ") {
		_, intro, _ = strings.Cut(intro, "\n")
	}
	if body := strings.TrimSpace(intro); body != "" {
		page.Sections = append(page.Sections, documentSection{Body: body})
	}
	if rest != "" {
		for _, part := range strings.Split(rest, "\n## ") {
			title, body, _ := strings.Cut(part, "\n")
			title = strings.TrimSpace(title)
			section := documentSection{Title: title, Body: strings.TrimSpace(body)}
			if rule, exists := rules[title]; exists {
				section.ID = rule.ID
			}
			page.Sections = append(page.Sections, section)
		}
	}
	return page
}

var documentTemplate = template.Must(template.New("document").Parse(`<!doctype html>
<html lang="ru"><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1">
<title>{{.Title}} · AI-Buddy</title><style>body{max-width:800px;margin:48px auto;padding:0 24px;color:#141414;font:16px/1.6 system-ui,sans-serif}pre{font:inherit;white-space:pre-wrap;overflow-wrap:anywhere}</style>
<main><h1>{{.Title}}</h1>{{range .Sections}}{{if .Title}}<h2{{if .ID}} id="{{.ID}}"{{end}}>{{.Title}}</h2>{{end}}<pre>{{.Body}}</pre>{{end}}</main></html>`))
