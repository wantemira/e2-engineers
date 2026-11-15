package httpapi_test

import (
	"bytes"
	"context"
	"encoding/json"
	"github.com/wantemira/e2-engineers/backend/internal/integration/mcpknowledge"
	"github.com/wantemira/e2-engineers/backend/internal/teamconfig"
	"io"
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
		if answerKnowledgeForTest(t, w, r) {
			return
		}
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
	if unknown.State != "SUCCEEDED" || unknown.Result.Messages[1].Help.Kind != "not_found" || unknown.Result.Messages[1].Help.TargetID != "" {
		t.Fatal("unknown assistant did not offer grounded escalation")
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
		if answerKnowledgeForTest(t, w, r) {
			return
		}
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
func TestLiveConversationClarifiesUnknownTermThenDraftsWithNaturalWording(t *testing.T) {
	for _, subject := range []string{"обсервер", "пульсар", "зефир"} {
		t.Run(subject, func(t *testing.T) {
			var searches atomic.Int32
			_, api := liveAPI(t, func(w http.ResponseWriter, r *http.Request) {
				if answerKnowledgeForTest(t, w, r) {
					return
				}
				var body struct {
					Messages []struct {
						Content string `json:"content"`
					} `json:"messages"`
				}
				if json.NewDecoder(r.Body).Decode(&body) != nil {
					t.Fatal("invalid request")
				}
				var input struct {
					Question string `json:"question"`
				}
				json.Unmarshal([]byte(body.Messages[1].Content), &input)
				if strings.Contains(input.Question, "наставнику") {
					modelCall(w, "find_team_help", map[string]string{"kind": "escalation", "target_id": ""})
					return
				}
				searches.Add(1)
				modelCall(w, "search_team_knowledge", map[string]string{"query": subject, "purpose": "information"})
			})
			client := sessionClient(t)
			question := "супер! а про " + subject + " слышал? можешь рассказать все что знаешь"
			first := waitForOperation(t, client, api.URL, submitForTest(t, client, api.URL, "/messages", "observer", map[string]string{"text": question}))
			if first.State != "SUCCEEDED" || first.Result.Messages[1].Help.Investigation.Purpose != "information" || len(first.Result.Messages[1].Help.Investigation.Observations) != 0 || strings.Contains(first.Result.Messages[1].Text, "ошибки") {
				t.Fatalf("informational query became diagnosis: %+v", first)
			}
			draft := waitForOperation(t, client, api.URL, submitForTest(t, client, api.URL, "/messages", "observer-draft", map[string]string{"text": "давай вопрос наставнику подготовим"}))
			if draft.State != "SUCCEEDED" || draft.Result.Messages[1].Help.Kind != "escalation" || !strings.Contains(draft.Result.Messages[1].MentorDraft, question) || strings.Contains(draft.Result.Messages[1].MentorDraft, "Grafana") || searches.Load() != 0 {
				t.Fatalf("natural draft request searched mentor instructions: %+v", draft)
			}
			var workspace domain.Workspace
			apiCall(t, client, "GET", api.URL+"/api/v1/workspace", "", nil, http.StatusOK, &workspace)
			if len(workspace.ReviewRequests) != 0 {
				t.Fatal("draft sent a message")
			}
		})
	}
}

// Test provider for the second, evidence-only model call. Routing providers above
// still choose the first tool explicitly; synthesis sees the actual MCP results.
func answerKnowledgeForTest(t *testing.T, w http.ResponseWriter, r *http.Request) bool {
	t.Helper()
	data, err := io.ReadAll(r.Body)
	if err != nil {
		t.Error(err)
		return false
	}
	r.Body = io.NopCloser(bytes.NewReader(data))
	var body struct {
		Messages []struct {
			Content string `json:"content"`
		} `json:"messages"`
		Tools []struct {
			Function struct {
				Name string `json:"name"`
			} `json:"function"`
		} `json:"tools"`
	}
	if json.Unmarshal(data, &body) != nil || len(body.Tools) != 1 || body.Tools[0].Function.Name != "answer_from_knowledge" {
		return false
	}
	var input struct {
		Evidence domain.Investigation `json:"evidence"`
	}
	if len(body.Messages) != 2 || json.Unmarshal([]byte(body.Messages[1].Content), &input) != nil {
		t.Error("invalid evidence")
		w.WriteHeader(500)
		return true
	}
	text := "В материалах команды нет описания. Что означает это название и что вы хотите узнать?"
	refs := []string{}
	if len(input.Evidence.Search.Hits) > 0 {
		text = input.Evidence.Search.Hits[0].Excerpt
		refs = append(refs, input.Evidence.Search.Hits[0].Reference)
	}
	if len(input.Evidence.Observations) > 0 {
		text += "\nMock-проверки: " + strings.Join(input.Evidence.Observations, " ")
	}
	modelCall(w, "answer_from_knowledge", map[string]any{"text": text, "source_references": refs})
	return true
}

func TestLiveUnknownAccessTargetSearchesOriginalQuestion(t *testing.T) {
	_, api := liveAPI(t, func(w http.ResponseWriter, r *http.Request) {
		if answerKnowledgeForTest(t, w, r) {
			return
		}
		modelCall(w, "find_team_help", map[string]string{"kind": "access", "target_id": "invented-resource-id"})
	})
	client := sessionClient(t)
	question := "Как получить доступ к GITLAB CI CD?"
	operation := waitForOperation(t, client, api.URL, submitForTest(t, client, api.URL, "/messages", "unknown-access", map[string]string{"text": question}))
	if operation.State != "SUCCEEDED" {
		t.Fatalf("unknown access became error: %+v", operation)
	}
	message := operation.Result.Messages[1]
	if message.Help.Investigation == nil || message.Help.Investigation.Search.Query != question || message.Help.Investigation.Purpose != "access" || message.Receipt != nil {
		t.Fatalf("fabricated resource wasn't replaced with grounded search: %+v", message)
	}
}

func TestEveryLiveMessageSearchesBeforeModelAndUnknownAccessCanEscalate(t *testing.T) {
	knowledge := &trackingKnowledge{}
	var modelCalls atomic.Int32
	_, api := liveAPI(t, func(w http.ResponseWriter, r *http.Request) {
		if knowledge.calls.Load() == 0 {
			t.Error("model called before mandatory search")
		}
		modelCalls.Add(1)
		modelCall(w, "answer_question", map[string]any{"text": "Привет! Чем помочь?", "document_ids": []string{}, "mentor_draft": ""})
	}, knowledge)
	client := sessionClient(t)
	run := func(key, text string) domain.Operation {
		return waitForOperation(t, client, api.URL, submitForTest(t, client, api.URL, "/messages", key, map[string]string{"text": text}))
	}
	greeting := run("mandatory-greeting", "Привет")
	if greeting.State != "SUCCEEDED" || knowledge.calls.Load() != 1 || greeting.Result.Messages[1].Help.Investigation == nil {
		t.Fatalf("greeting skipped search: %+v", greeting)
	}
	unknown := run("mandatory-unknown", "Как получить доступ к GITLAB CI CD?")
	if unknown.State != "SUCCEEDED" || knowledge.calls.Load() != 2 || modelCalls.Load() != 1 || !unknown.Result.Messages[1].Help.EscalationOffered || unknown.Result.Messages[1].MentorDraft != "" {
		t.Fatalf("unknown access failed or called model unnecessarily: %+v", unknown)
	}
	yes := run("mandatory-confirm", "Да")
	if yes.State != "SUCCEEDED" || knowledge.calls.Load() != 3 || modelCalls.Load() != 1 || !strings.Contains(yes.Result.Messages[1].MentorDraft, "GITLAB CI CD") || !strings.Contains(yes.Result.Messages[1].MentorDraft, "Подходящих разделов не найдено") {
		t.Fatalf("confirmation lost original question: %+v", yes)
	}
}

type trackingKnowledge struct{ calls atomic.Int32 }

func (k *trackingKnowledge) Search(ctx context.Context, actor, query string) (domain.KnowledgeSearch, error) {
	k.calls.Add(1)
	team, err := teamconfig.New("../../../..").Load()
	if err != nil {
		return domain.KnowledgeSearch{}, err
	}
	return mcpknowledge.Search(team, query), nil
}
