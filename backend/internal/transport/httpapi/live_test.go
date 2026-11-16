package httpapi_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/wantemira/e2-engineers/backend/internal/domain"
	"github.com/wantemira/e2-engineers/backend/internal/integration/mcpgit"
	"github.com/wantemira/e2-engineers/backend/internal/integration/mcpknowledge"
	"github.com/wantemira/e2-engineers/backend/internal/llm"
	"github.com/wantemira/e2-engineers/backend/internal/repository"
	"github.com/wantemira/e2-engineers/backend/internal/service"
	"github.com/wantemira/e2-engineers/backend/internal/teamconfig"
	"github.com/wantemira/e2-engineers/backend/internal/transport/httpapi"
)

func modelCall(w http.ResponseWriter, name string, arguments any) {
	data, _ := json.Marshal(arguments)
	json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{
		"finish_reason": "tool_calls", "message": map[string]any{"tool_calls": []any{map[string]any{
			"id": "test-call", "type": "function", "function": map[string]any{"name": name, "arguments": string(data)},
		}}},
	}}})
}

func liveAPI(t *testing.T, provider http.HandlerFunc, customKnowledge ...service.KnowledgeTools) (string, *httptest.Server) {
	t.Helper()
	root := t.TempDir()
	for _, dir := range []string{"teams", "skills", "mocks"} {
		if err := os.CopyFS(filepath.Join(root, dir), os.DirFS(filepath.Join("../../../..", dir))); err != nil {
			t.Fatal(err)
		}
	}
	modelAPI := httptest.NewServer(provider)
	t.Cleanup(modelAPI.Close)
	model, err := llm.New(llm.Config{BaseURL: modelAPI.URL + "/v1", Model: "contract-model", APIKey: "synthetic-key"})
	if err != nil {
		t.Fatal(err)
	}
	gitHandler, err := mcpgit.NewGitHandler(filepath.Join(root, "mocks", "pr-17.json"), filepath.Join(root, "data", "git.json"), "test-secret")
	if err != nil {
		t.Fatal(err)
	}
	gitAPI := httptest.NewServer(gitHandler)
	t.Cleanup(gitAPI.Close)
	git := mcpgit.NewMCPGit(gitAPI.URL, "test-secret")
	teams := teamconfig.New(root)
	store, err := repository.NewStore(filepath.Join(root, "data", "application.json"))
	if err != nil {
		t.Fatal(err)
	}
	knowledgeAPI := httptest.NewServer(mcpknowledge.NewHandler("test-secret", teams))
	t.Cleanup(knowledgeAPI.Close)
	var knowledge service.KnowledgeTools = mcpknowledge.NewClient(knowledgeAPI.URL, "test-secret")
	if len(customKnowledge) > 0 {
		knowledge = customKnowledge[0]
	}
	app := service.NewService(store, git, model, teams, llm.NewRunner(model, git, teams, nil), knowledge)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- app.Run(ctx) }()
	t.Cleanup(func() {
		cancel()
		if err := <-done; err != nil {
			t.Error(err)
		}
	})
	api := httptest.NewServer(httpapi.NewHTTPHandler(app, http.NotFoundHandler(), nil))
	t.Cleanup(api.Close)
	return root, api
}

func reviewArguments(line int) map[string]any {
	return map[string]any{"scope": "Анализ доступного diff", "task": "Причина ошибки не сохранена", "ci_summary": "TestCreateRepositoryError подтверждает потерю причины ошибки.", "suggested_tests": []string{"Проверьте errors.Is(actual, expected) при ошибке repository.Create."}, "findings": []any{map[string]any{
		"id": "generated", "kind": "problem", "title": "Сохраните причину ошибки", "description": "errors.New заменяет исходную ошибку. Используйте fmt.Errorf с %w и добавьте TestCreateRepositoryError.",
		"path": "internal/orders/service.go", "line": line, "rule_id": "GO-ERR-01",
	}}}
}

func TestLiveHTTPReviewUsesSkillAndMCPAndPreservesConfirmation(t *testing.T) {
	var changed atomic.Bool
	var analyses atomic.Int32
	root, api := liveAPI(t, func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Messages []struct {
				Content string `json:"content"`
			} `json:"messages"`
		}
		if json.NewDecoder(r.Body).Decode(&body) != nil || len(body.Messages) != 2 {
			t.Error("invalid model request")
			w.WriteHeader(500)
			return
		}
		input := body.Messages[1].Content
		if strings.Contains(input, `"untrusted_pull_request"`) {
			analyses.Add(1)
			if !strings.Contains(input, "errors.New") || !strings.Contains(input, "GO-ERR-01") || !strings.Contains(input, "# Самопроверка PR") || !strings.Contains(input, "expected wrapped storage error") || !strings.Contains(input, `CI-`) {
				t.Error("missing MCP data, rules or skill")
			}
			if changed.Load() && !strings.Contains(input, "AUDIT_CHANGED_INSTRUCTIONS") {
				t.Error("updated instructions were not sent to the model")
			}
			modelCall(w, "submit_review", reviewArguments(38))
		} else if strings.Contains(input, "Объясни") {
			if !strings.Contains(input, "Сохраните причину ошибки") {
				t.Error("current review not provided")
			}
			modelCall(w, "answer_question", map[string]any{"text": "Нужна цепочка ошибок для errors.Is.", "document_ids": []string{"review-rules"}, "mentor_draft": ""})
		} else {
			modelCall(w, "run_pr_self_review", map[string]any{"pr_ref": "17", "run_checks": true})
		}
	})
	client := sessionClient(t)
	first := waitForOperation(t, client, api.URL, submitForTest(t, client, api.URL, "/messages", "live-review", map[string]string{"text": "Посмотри мои изменения в PR #17 и запусти тесты"}))
	if first.State != "SUCCEEDED" || first.Result == nil || first.Result.Review == nil {
		t.Fatalf("live review failed: %+v", first)
	}
	run := first.Result.Review
	if run.Skill.Source != "LLM" || run.Skill.Model != "contract-model" || len(run.RulesSHA256) != 64 || run.Checks.Status != "FAILED" || len(run.Findings) != 1 || run.Findings[0].Location.Line != 38 || run.Assessment.CI == "" || len(run.Assessment.SuggestedTests) == 0 {
		t.Fatalf("bad live report: %+v", run)
	}
	if first.Result.Messages[1].Agent.Tool != "run_pr_self_review" {
		t.Fatal("missing routing trace")
	}
	var workspace domain.Workspace
	apiCall(t, client, "GET", api.URL+"/api/v1/workspace", "", nil, http.StatusOK, &workspace)
	if workspace.Assistant.Mode != "live" || len(workspace.ReviewRequests) != 0 {
		t.Fatalf("unconfirmed write or wrong mode: %+v", workspace)
	}
	explained := waitForOperation(t, client, api.URL, submitForTest(t, client, api.URL, "/messages", "explain", map[string]string{"text": "Объясни замечание"}))
	if explained.State != "SUCCEEDED" || len(explained.Result.Messages[1].Links) != 1 {
		t.Fatalf("follow-up failed: %+v", explained)
	}
	path := filepath.Join(root, "skills", "pr-self-review", "SKILL.md")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, append(data, []byte("\nAUDIT_CHANGED_INSTRUCTIONS\n")...), 0600); err != nil {
		t.Fatal(err)
	}
	changed.Store(true)
	second := waitForOperation(t, client, api.URL, submitForTest(t, client, api.URL, "/self-reviews", "recheck", map[string]string{"pr_ref": "17"}))
	if second.State != "SUCCEEDED" || second.Result.Review.Skill.InstructionsSHA256 == run.Skill.InstructionsSHA256 || analyses.Load() != 2 {
		t.Fatalf("instructions not applied: %+v", second)
	}
	receipt := waitForOperation(t, client, api.URL, submitForTest(t, client, api.URL, "/review-requests", "confirmed", map[string]string{"review_run_id": second.Result.Review.ID, "expected_mentor_id": run.Mentor.ID}))
	if receipt.State != "SUCCEEDED" || receipt.Result.ReviewRequest.HeadSHA != run.HeadSHA {
		t.Fatalf("confirmed write failed: %+v", receipt)
	}
}

func TestLiveFailureRetryAndUntrustedResults(t *testing.T) {
	var rateLimit atomic.Bool
	rateLimit.Store(true)
	var scenario atomic.Int32
	_, api := liveAPI(t, func(w http.ResponseWriter, r *http.Request) {
		if rateLimit.Load() {
			w.WriteHeader(429)
			w.Write([]byte("provider body with synthetic-key"))
			return
		}
		var body map[string]json.RawMessage
		json.NewDecoder(r.Body).Decode(&body)
		if strings.Contains(string(body["messages"]), `untrusted_pull_request`) {
			modelCall(w, "submit_review", reviewArguments(999))
			return
		}
		switch scenario.Load() {
		case 1:
			modelCall(w, "run_pr_self_review", map[string]string{"pr_ref": "18"})
		case 2:
			modelCall(w, "run_pr_self_review", map[string]string{"pr_ref": "17"})
		case 3:
			modelCall(w, "request_pull_request_review", map[string]string{"pr_ref": "17"})
		default:
			modelCall(w, "answer_question", map[string]any{"text": "Ответ по документам команды.", "document_ids": []string{"context"}, "mentor_draft": ""})
		}
	})
	client := sessionClient(t)
	id := submitForTest(t, client, api.URL, "/messages", "rate-limit", map[string]string{"text": "Как работать в команде?"})
	failed := waitForOperation(t, client, api.URL, id)
	if failed.State != "FAILED" || failed.Error.Code != "LLM_UNAVAILABLE" || !failed.Error.Retryable {
		t.Fatalf("silent fallback: %+v", failed)
	}
	rateLimit.Store(false)
	apiCall(t, client, "POST", api.URL+"/api/v1/operations/"+id+"/retry", "rate-limit", struct{}{}, http.StatusAccepted, nil)
	if retried := waitForOperation(t, client, api.URL, id); retried.State != "SUCCEEDED" {
		t.Fatalf("retry failed: %+v", retried)
	}
	for _, tc := range []struct {
		scenario int32
		code     string
	}{{1, "NOT_FOUND"}, {2, "SKILL_INVALID_RESULT"}, {3, "LLM_INVALID_RESULT"}} {
		scenario.Store(tc.scenario)
		result := waitForOperation(t, client, api.URL, submitForTest(t, client, api.URL, "/messages", tc.code, map[string]string{"text": "Проверь PR"}))
		if result.State != "FAILED" || result.Error.Code != tc.code {
			t.Fatalf("unsafe result accepted: %+v", result)
		}
	}
	var workspace domain.Workspace
	apiCall(t, client, "GET", api.URL+"/api/v1/workspace", "", nil, http.StatusOK, &workspace)
	if len(workspace.Reviews) != 0 || len(workspace.ReviewRequests) != 0 {
		t.Fatal("invalid model result created report or receipt")
	}
}

func TestLiveTransientFailureRecoversWithinOriginalOperation(t *testing.T) {
	var calls atomic.Int32
	_, api := liveAPI(t, func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		modelCall(w, "answer_question", map[string]any{"text": "Ответ по документам команды.", "document_ids": []string{"context"}, "mentor_draft": ""})
	})
	client := sessionClient(t)
	command := map[string]string{"text": "Как работать в команде?"}
	id := submitForTest(t, client, api.URL, "/messages", "automatic-retry", command)
	completed := waitForOperation(t, client, api.URL, id)
	if completed.State != "SUCCEEDED" || calls.Load() != 2 {
		t.Fatalf("temporary failure required a manual retry: %+v, calls %d", completed, calls.Load())
	}
	if duplicate := submitForTest(t, client, api.URL, "/messages", "automatic-retry", command); duplicate != id {
		t.Fatal("original operation identity lost")
	}
	var workspace domain.Workspace
	apiCall(t, client, "GET", api.URL+"/api/v1/workspace", "", nil, http.StatusOK, &workspace)
	if len(workspace.Messages) != 2 || len(workspace.ReviewRequests) != 0 {
		t.Fatalf("automatic retry duplicated messages or wrote to Git: %+v", workspace)
	}
}
