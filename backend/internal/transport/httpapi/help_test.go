package httpapi_test

import (
	"context"
	"encoding/json"
	"github.com/wantemira/e2-engineers/backend/internal/integration/mcpknowledge"
	"github.com/wantemira/e2-engineers/backend/internal/teamconfig"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/wantemira/e2-engineers/backend/internal/domain"
)

func TestLiveHelpUsesCatalogSourcesAndGroundedEscalation(t *testing.T) {
	root, api := liveAPI(t, func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Messages []struct {
				Content string `json:"content"`
			} `json:"messages"`
		}
		if json.NewDecoder(r.Body).Decode(&body) != nil || len(body.Messages) != 2 {
			t.Error("invalid request")
			w.WriteHeader(500)
			return
		}
		var input struct {
			Question string `json:"question"`
		}
		if json.Unmarshal([]byte(body.Messages[1].Content), &input) != nil {
			t.Error("invalid context")
			w.WriteHeader(500)
			return
		}
		if !strings.Contains(body.Messages[1].Content, "# Первая линия помощи") {
			t.Error("help instructions not provided")
		}
		question := strings.ToLower(input.Question)
		switch {
		case strings.Contains(question, "задач"):
			modelCall(w, "answer_question", map[string]any{"text": "Уточните критерии приёмки задачи.", "document_ids": []string{"process-guide#Работа с задачей"}, "mentor_draft": ""})
		case strings.Contains(question, "grafana"):
			modelCall(w, "find_team_help", map[string]string{"kind": "access", "target_id": "grafana"})
		case strings.Contains(question, "отпуск"):
			modelCall(w, "find_team_help", map[string]string{"kind": "handoff", "target_id": "vacation"})
		case strings.Contains(question, "зарплат"):
			modelCall(w, "find_team_help", map[string]string{"kind": "handoff", "target_id": "payments"})
		case strings.Contains(question, "выдуман"):
			modelCall(w, "find_team_help", map[string]string{"kind": "handoff", "target_id": "invented"})
		case strings.Contains(question, "подготовь"):
			modelCall(w, "find_team_help", map[string]string{"kind": "escalation", "target_id": ""})
		default:
			modelCall(w, "search_team_knowledge", map[string]string{"query": input.Question})
		}
	})
	client := sessionClient(t)
	run := func(key, text string) domain.Operation {
		return waitForOperation(t, client, api.URL, submitForTest(t, client, api.URL, "/messages", key, map[string]string{"text": text}))
	}
	docs := run("docs", "Как оформить задачу?")
	if docs.State != "SUCCEEDED" || docs.Result.Messages[1].Links[0].URL != "/api/v1/context/documents/process-guide#section-1" || docs.Result.Messages[1].Help.Skill.ID != "team-help" {
		t.Fatalf("bad sourced answer: %+v", docs)
	}
	var page string
	apiCall(t, client, "GET", api.URL+docs.Result.Messages[1].Links[0].URL, "", nil, http.StatusOK, &page)
	if !strings.Contains(page, `id="section-1"`) || !strings.Contains(page, "критерии приёмки") {
		t.Fatal("source section is not navigable")
	}
	access := run("access", "Как получить доступ к Grafana?")
	if access.State != "SUCCEEDED" || !strings.Contains(access.Result.Messages[1].Text, "Viewer") || len(access.Result.Messages[1].Links) != 2 || access.Result.Messages[1].MentorDraft != "" {
		t.Fatalf("bad access instruction: %+v", access)
	}
	vacation := run("vacation", "Как оформить отпуск?")
	if vacation.State != "SUCCEEDED" || vacation.Result.Messages[1].Help.TargetID != "vacation" || vacation.Result.Messages[1].Receipt != nil {
		t.Fatalf("bad handoff: %+v", vacation)
	}
	unknown := run("unknown", "Отправь к выдуманному боту")
	if unknown.State != "FAILED" || unknown.Error.Code != "LLM_INVALID_RESULT" {
		t.Fatal("invented assistant accepted")
	}
	problem := run("problem", "Не могу запустить orders: получаю 502")
	if problem.State != "SUCCEEDED" || problem.Result.Messages[1].MentorDraft != "" || problem.Result.Messages[1].Help.Investigation == nil || !strings.Contains(problem.Result.Messages[1].Text, "STOPPED") || problem.Result.Messages[1].Agent.Tool != "search_team_knowledge" {
		t.Fatalf("missing real MCP search/diagnostics: %+v", problem)
	}
	run("attempt", "Уже пробовал перезапуск Docker, ошибка осталась")
	draft := run("draft", "Подготовь вопрос наставнику")
	if draft.State != "SUCCEEDED" || !strings.Contains(draft.Result.Messages[1].MentorDraft, "502") || !strings.Contains(draft.Result.Messages[1].MentorDraft, "перезапуск Docker") || !strings.Contains(draft.Result.Messages[1].MentorDraft, "Проверки Buddy") || !strings.Contains(draft.Result.Messages[1].MentorDraft, "STOPPED") {
		t.Fatalf("ungrounded escalation: %+v", draft)
	}
	// Add an entirely new assistant while the application is running: no Go change.
	path := filepath.Join(root, "teams", "demo-backend", "context.json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var config map[string]any
	if err := json.Unmarshal(data, &config); err != nil {
		t.Fatal(err)
	}
	help := config["help"].(map[string]any)
	help["assistants"] = append(help["assistants"].([]any), map[string]any{"id": "payments", "name": "Помощник по выплатам", "description": "Демонстрационный адресат вопросов о выплатах.", "keywords": []string{"зарплат"}, "document_id": "assistant-directory", "section": "Выплаты"})
	profile := filepath.Join(root, "teams", "demo-backend", "assistant-directory.md")
	file, err := os.OpenFile(profile, os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.WriteString("\n## Выплаты\n\nДемонстрационный профиль помощника по выплатам.\n"); err != nil {
		t.Fatal(err)
	}
	file.Close()
	data, err = json.Marshal(config)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	payments := run("payments", "Кому задать вопрос по зарплате?")
	if payments.State != "SUCCEEDED" || payments.Result.Messages[1].Help.TargetID != "payments" || payments.Result.Messages[1].Links[0].URL != "/api/v1/context/documents/assistant-directory#section-4" {
		t.Fatalf("new catalog route not picked up: %+v", payments)
	}
	var workspace domain.Workspace
	apiCall(t, client, "GET", api.URL+"/api/v1/workspace", "", nil, http.StatusOK, &workspace)
	if len(workspace.ReviewRequests) != 0 || len(workspace.Reviews) != 0 {
		t.Fatal("help created an unrelated Git action")
	}
	if workspace.Messages[len(workspace.Messages)-1].Help.TargetID != "payments" {
		t.Fatal("handoff was not persisted")
	}
}

type failOnceKnowledge struct{ calls atomic.Int32 }

func (k *failOnceKnowledge) Search(_ context.Context, actor, query string) (domain.KnowledgeSearch, error) {
	if k.calls.Add(1) == 1 {
		return domain.KnowledgeSearch{}, &domain.AppError{Code: "DEPENDENCY_UNAVAILABLE", Message: "temporary outage", Retryable: true}
	}
	team, err := teamconfig.New("../../../..").Load()
	if err != nil {
		return domain.KnowledgeSearch{}, err
	}
	return mcpknowledge.Search(team, query), nil
}
func TestLiveSearchOutageRetriesSameOperationWithoutDuplicateMessages(t *testing.T) {
	knowledge := &failOnceKnowledge{}
	_, api := liveAPI(t, func(w http.ResponseWriter, r *http.Request) {
		modelCall(w, "search_team_knowledge", map[string]string{"query": "orders 502"})
	}, knowledge)
	client := sessionClient(t)
	id := submitForTest(t, client, api.URL, "/messages", "search-retry", map[string]string{"text": "Не могу запустить orders: 502"})
	failed := waitForOperation(t, client, api.URL, id)
	if failed.State != "FAILED" || failed.Error == nil || !failed.Error.Retryable {
		t.Fatalf("outage became empty search: %+v", failed)
	}
	apiCall(t, client, "POST", api.URL+"/api/v1/operations/"+id+"/retry", "search-retry", struct{}{}, http.StatusAccepted, nil)
	success := waitForOperation(t, client, api.URL, id)
	if success.State != "SUCCEEDED" || success.Result.Messages[1].Help.Investigation == nil {
		t.Fatalf("retry failed: %+v", success)
	}
	var workspace domain.Workspace
	apiCall(t, client, "GET", api.URL+"/api/v1/workspace", "", nil, http.StatusOK, &workspace)
	if len(workspace.Messages) != 2 || len(workspace.Reviews) != 0 || len(workspace.ReviewRequests) != 0 {
		t.Fatalf("retry duplicated or wrote to Git: %+v", workspace)
	}
}
func TestLivePrematureEscalationRunsSearchInstead(t *testing.T) {
	_, api := liveAPI(t, func(w http.ResponseWriter, r *http.Request) {
		modelCall(w, "find_team_help", map[string]string{"kind": "escalation", "target_id": ""})
	})
	client := sessionClient(t)
	operation := waitForOperation(t, client, api.URL, submitForTest(t, client, api.URL, "/messages", "premature", map[string]string{"text": "Не могу запустить orders: 502"}))
	if operation.State != "SUCCEEDED" || operation.Result.Messages[1].MentorDraft != "" || operation.Result.Messages[1].Help.Investigation == nil {
		t.Fatalf("model bypassed search-first policy: %+v", operation)
	}
}
