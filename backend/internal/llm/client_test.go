package llm

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/wantemira/e2-engineers/backend/internal/domain"
)

func writeCall(w http.ResponseWriter, name, arguments string) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{
		"finish_reason": "tool_calls", "message": map[string]any{"tool_calls": []any{map[string]any{
			"id": "call-test", "type": "function", "function": map[string]any{"name": name, "arguments": arguments},
		}}},
	}}})
}

func testClient(t *testing.T, handler http.HandlerFunc) *Client {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	client, err := New(Config{BaseURL: server.URL + "/v1", Model: "test-model", APIKey: "test-secret"})
	if err != nil {
		t.Fatal(err)
	}
	return client
}

func TestExplicitModeAndConfiguration(t *testing.T) {
	for _, tc := range []struct {
		name          string
		env           map[string]string
		live, invalid bool
	}{
		{"default", nil, false, false},
		{"key alone stays demo", map[string]string{"LLM_API_KEY": "secret"}, false, false},
		{"demo ignores model config", map[string]string{"BUDDY_MODE": "demo", "LLM_BASE_URL": "wrong"}, false, false},
		{"unknown mode", map[string]string{"BUDDY_MODE": "auto"}, false, true},
		{"missing config", map[string]string{"BUDDY_MODE": "live"}, false, true},
		{"valid", map[string]string{"BUDDY_MODE": "live", "LLM_BASE_URL": "https://example.test/v1", "LLM_MODEL": "model", "LLM_API_KEY": "secret"}, true, false},
		{"bad timeout", map[string]string{"BUDDY_MODE": "live", "LLM_TIMEOUT": "forever"}, false, true},
		{"unsupported provider", map[string]string{"BUDDY_MODE": "live", "LLM_PROVIDER": "other"}, false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client, err := FromEnvironment(func(key string) string { return tc.env[key] })
			if (err != nil) != tc.invalid || (client != nil) != tc.live {
				t.Fatalf("client=%v error=%v", client != nil, err)
			}
		})
	}
	for _, config := range []Config{
		{BaseURL: "file:///tmp/model", Model: "model", APIKey: "secret"},
		{BaseURL: "https://user:secret@example.test/v1", Model: "model", APIKey: "secret"},
		{BaseURL: "https://example.test/v1?key=secret", Model: "model", APIKey: "secret"},
		{BaseURL: "https://example.test/v1", Model: "model", APIKey: "bad\nkey"},
		{BaseURL: "https://example.test/v1", Model: "model", APIKey: "secret", Timeout: time.Minute},
	} {
		if _, err := New(config); err == nil {
			t.Fatal("accepted invalid configuration")
		}
	}
}

func TestModelReceivesContextAndOnlyReadTools(t *testing.T) {
	client := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" || r.Header.Get("Authorization") != "Bearer test-secret" {
			t.Error("wrong endpoint or authentication")
		}
		var body struct {
			Model    string `json:"model"`
			Messages []struct {
				Content string `json:"content"`
			} `json:"messages"`
			Tools  []Tool `json:"tools"`
			Choice string `json:"tool_choice"`
		}
		if json.NewDecoder(r.Body).Decode(&body) != nil {
			t.Fatal("bad request JSON")
		}
		if body.Model != "test-model" || body.Choice != "required" || !strings.Contains(body.Messages[1].Content, "actual skill instructions") || !strings.Contains(body.Messages[1].Content, "actual team document") {
			t.Error("context or instructions missing")
		}
		for _, tool := range body.Tools {
			if tool.Function.Name == "request_pull_request_review" {
				t.Error("write tool exposed")
			}
		}
		writeCall(w, "answer_question", `{"text":"По правилам команды используйте gofmt.","document_ids":["review-rules"],"mentor_draft":""}`)
	})
	input := domain.ChatInput{Text: "Как оформить код?", Team: domain.Team{
		Documents: []domain.Document{{ID: "review-rules", Title: "Правила", Body: "actual team document"}},
		Skills:    []domain.SkillDefinition{{SkillRef: domain.SkillRef{ID: "pr-self-review"}, Instructions: "actual skill instructions"}},
	}}
	message, err := client.Answer(context.Background(), input)
	if err != nil || len(message.Links) != 1 || message.Links[0].URL != "/api/v1/context/documents/review-rules" {
		t.Fatalf("answer=%+v err=%v", message, err)
	}
	if client.Info().Mode != "live" || client.Info().Model != "test-model" {
		t.Fatal("wrong public info")
	}
}

func TestProviderFailuresAndUntrustedCalls(t *testing.T) {
	for _, tc := range []struct {
		name                     string
		status                   int
		nameOut, args, raw, code string
		retry                    bool
	}{
		{"rate limit", 429, "", "", "secret provider error", "LLM_UNAVAILABLE", true},
		{"server", 503, "", "", "secret provider error", "LLM_UNAVAILABLE", true},
		{"credentials", 401, "", "", "test-secret", "LLM_CONFIGURATION", false},
		{"bad json", 200, "", "", "{", "LLM_INVALID_RESULT", true},
		{"write attempt", 200, "request_pull_request_review", `{}`, "", "LLM_INVALID_RESULT", true},
		{"extra identity", 200, "run_pr_self_review", `{"pr_ref":"17","actor":"admin"}`, "", "LLM_INVALID_RESULT", true},
		{"trailing json", 200, "run_pr_self_review", `{"pr_ref":"17"} {}`, "", "LLM_INVALID_RESULT", true},
		{"empty PR", 200, "run_pr_self_review", `{"pr_ref":""}`, "", "LLM_INVALID_RESULT", true},
		{"invented source", 200, "answer_question", `{"text":"Answer","document_ids":["secret-document"],"mentor_draft":""}`, "", "LLM_INVALID_RESULT", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client := testClient(t, func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tc.status)
				if tc.raw != "" {
					w.Write([]byte(tc.raw))
				} else {
					writeCall(w, tc.nameOut, tc.args)
				}
			})
			_, err := client.Plan(context.Background(), domain.ChatInput{})
			problem := domain.PublicError(err)
			if err == nil || problem.Code != tc.code || problem.Retryable != tc.retry || strings.Contains(problem.Message, "test-secret") {
				t.Fatalf("unexpected public error: %v", err)
			}
		})
	}
}

func TestTimeoutCancellationAndRedirect(t *testing.T) {
	client := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
		case <-time.After(100 * time.Millisecond):
		}
	})
	client.http.Timeout = 15 * time.Millisecond
	_, err := client.Plan(context.Background(), domain.ChatInput{})
	if domain.PublicError(err).Code != "LLM_TIMEOUT" {
		t.Fatalf("timeout=%v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := client.Plan(ctx, domain.ChatInput{}); err != context.Canceled {
		t.Fatalf("cancellation=%v", err)
	}
	redirect := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "https://example.test/stolen", http.StatusTemporaryRedirect)
	})
	if _, err := redirect.Plan(context.Background(), domain.ChatInput{}); domain.PublicError(err).Code != "LLM_CONFIGURATION" {
		t.Fatalf("redirect=%v", err)
	}
}
