package teamhelp_test

import (
	"strings"
	"testing"

	"github.com/wantemira/e2-engineers/backend/internal/domain"
	"github.com/wantemira/e2-engineers/backend/internal/teamconfig"
	"github.com/wantemira/e2-engineers/backend/internal/teamhelp"
)

func load(t *testing.T) domain.Team {
	t.Helper()
	team, err := teamconfig.New("../../..").Load()
	if err != nil {
		t.Fatal(err)
	}
	return team
}

func TestTeamAnswersHaveSpecificSourcesAndTargets(t *testing.T) {
	team := load(t)
	for _, tc := range []struct{ question, kind, target, text string }{
		{"Что писать в описании задачи?", "docs", "process-guide#Работа с задачей", "критерии приёмки"},
		{"Как подготовить PR к ревью наставника?", "docs", "review-rules#Самопроверка", "gofmt"},
		{"Как получить доступ к Grafana?", "access", "grafana", "Viewer"},
		{"Как получить доступ к репозиторию?", "access", "repository", "Developer"},
		{"Как оформить больничный?", "handoff", "hr", "HR-помощник"},
		{"Как оформить отпуск?", "handoff", "vacation", "Помощник по отпускам"},
		{"Кому задать вопрос по PostgreSQL?", "handoff", "databases", "Помощник юнита БД"},
	} {
		t.Run(tc.question, func(t *testing.T) {
			message := teamhelp.Answer(domain.ChatInput{Text: tc.question, Team: team})
			if message.Help == nil || message.Help.Kind != tc.kind || message.Help.TargetID != tc.target || !strings.Contains(message.Text, tc.text) || len(message.Links) == 0 || len(message.Help.InstructionsSHA256) != 64 {
				t.Fatalf("unexpected answer: %+v", message)
			}
			if tc.kind == "handoff" && (!strings.Contains(message.Text, "сообщение не отправлено") || message.MentorDraft != "") {
				t.Fatal("handoff should not escalate or pretend to send")
			}
			if !strings.Contains(message.Links[0].URL, "#section-") {
				t.Fatal("source must reference a concrete section")
			}
		})
	}
	for _, resource := range []string{"GitLab", "Kubernetes", "unknown"} {
		message := teamhelp.Answer(domain.ChatInput{Text: "Как получить доступ к " + resource + "?", Team: team})
		if message.Help.Kind != "search" || message.MentorDraft != "" || strings.Contains(message.Text, "Viewer") {
			t.Fatalf("unknown resource was mapped to a known one: %+v", message)
		}
	}
}

func TestNewAssistantAndUpdatedDocsNeedOnlyCatalogData(t *testing.T) {
	team := load(t)
	team.Documents = append(team.Documents, domain.Document{ID: "analytics", Title: "Аналитика", Body: "# Demo\n\n## Вопросы\n\nДемонстрационный профиль аналитики."})
	team.Help.Assistants = append(team.Help.Assistants, domain.AssistantRoute{ID: "analytics", Name: "Помощник аналитиков", Description: "Вопросы аналитики.", Keywords: []string{"redash"}, DocumentID: "analytics", Section: "Вопросы"})
	if err := teamhelp.Validate(team); err != nil {
		t.Fatal(err)
	}
	message := teamhelp.Answer(domain.ChatInput{Text: "Кто поможет с Redash?", Team: team})
	if message.Help.TargetID != "analytics" || message.Links[0].URL != "/api/v1/context/documents/analytics#section-1" {
		t.Fatalf("new route not selected: %+v", message)
	}
	for i := range team.Documents {
		if team.Documents[i].ID == "access-guide" {
			team.Documents[i].Body = strings.ReplaceAll(team.Documents[i].Body, "Viewer", "UpdatedReader")
		}
	}
	message = teamhelp.Answer(domain.ChatInput{Text: "Нужен доступ к Grafana", Team: team})
	if !strings.Contains(message.Text, "UpdatedReader") {
		t.Fatal("answer did not read current Markdown")
	}
}

func TestEscalationUsesKnownContextAndClarifiesMissingInformation(t *testing.T) {
	team := load(t)
	message := teamhelp.Escalate(domain.ChatInput{Text: "Подготовь вопрос наставнику", Team: team})
	if message.MentorDraft != "" || !strings.Contains(message.Text, "что вы пытались") {
		t.Fatal("empty question should clarify before drafting")
	}
	message = teamhelp.Escalate(domain.ChatInput{Text: "Подготовь вопрос наставнику", Team: team, History: []domain.Message{
		{Role: "user", Text: "Не могу запустить orders: получаю 502"},
		{Role: "assistant", Text: "Проверьте сеть; уже всё исправлено"},
		{Role: "user", Text: "Уже пробовал перезапуск Docker, ошибка осталась"},
	}, CurrentReview: &domain.ReviewRun{PR: domain.PRInfo{Number: 17}, HeadSHA: "known-sha", Checks: domain.Checks{Status: "FAILED", Label: "test failed"}}})
	for _, value := range []string{"502", "перезапуск Docker", "known-sha", "FAILED"} {
		if !strings.Contains(message.MentorDraft, value) {
			t.Fatalf("missing known context %q", value)
		}
	}
	for _, value := range []string{"[задача]", "[ошибка]", "уже всё исправлено"} {
		if strings.Contains(message.MentorDraft, value) {
			t.Fatal("draft invented a fact or placeholder")
		}
	}
}

func TestInvalidCatalogLinksSourcesAndAmbiguousResourcesAreRejected(t *testing.T) {
	for _, scenario := range []string{"missing section", "unsafe URL", "ambiguous alias", "disabled skill"} {
		t.Run(scenario, func(t *testing.T) {
			team := load(t)
			switch scenario {
			case "missing section":
				team.Help.Assistants[0].Section = "does not exist"
			case "unsafe URL":
				team.Help.Access[0].RequestURL = "javascript:alert(1)"
			case "ambiguous alias":
				team.Help.Access[1].Aliases = append(team.Help.Access[1].Aliases, "Grafana")
			case "disabled skill":
				delete(team.Capabilities, "team_help")
			}
			if err := teamhelp.Validate(team); err == nil {
				t.Fatal("invalid catalog accepted")
			}
		})
	}
	team := load(t)
	if _, err := teamhelp.Handoff(domain.ChatInput{Team: team}, "invented-bot"); err == nil {
		t.Fatal("invented assistant accepted")
	}
	if _, _, ok := domain.DocumentSource(team, "process-guide#invented-section"); ok {
		t.Fatal("invented source accepted")
	}
}

func TestEscalationUsesEmptySearchAndSeparatesUnrelatedProblems(t *testing.T) {
	team := load(t)
	evidence := &domain.Investigation{Search: domain.KnowledgeSearch{Query: "Как получить доступ к Kubernetes?"}, CheckedAt: "2026-11-16T00:00:00Z"}
	history := []domain.Message{{Role: "user", Text: evidence.Search.Query}, {Role: "assistant", Help: &domain.HelpTrace{Investigation: evidence}}}
	draft := teamhelp.Escalate(domain.ChatInput{Team: team, Text: "Подготовь вопрос наставнику", History: history})
	if !strings.Contains(draft.MentorDraft, "Kubernetes") || !strings.Contains(draft.MentorDraft, "Подходящих разделов не найдено") {
		t.Fatalf("lost unsuccessful search: %+v", draft)
	}
	evidence.Search.Query = "Не могу запустить orders: 502"
	evidence.Observations = []string{"STOPPED"}
	history[0].Text = evidence.Search.Query
	draft = teamhelp.Escalate(domain.ChatInput{Team: team, Text: "Подготовь вопрос наставнику: нужен доступ к Kubernetes", History: history})
	if strings.Contains(draft.MentorDraft, "orders") || strings.Contains(draft.MentorDraft, "STOPPED") {
		t.Fatalf("unrelated evidence included: %s", draft.MentorDraft)
	}
}

func TestEscalationWithNewCheckResultPreservesInvestigatedProblem(t *testing.T) {
	team := load(t)
	evidence := &domain.Investigation{Search: domain.KnowledgeSearch{Query: "Не могу запустить orders: 502"}, Observations: []string{"STOPPED (MOCK)"}}
	draft := teamhelp.Escalate(domain.ChatInput{Team: team, Text: "Уже проверил /healthz: 503. Подготовь вопрос наставнику", History: []domain.Message{
		{Role: "user", Text: evidence.Search.Query}, {Role: "assistant", Help: &domain.HelpTrace{Investigation: evidence}},
	}})
	for _, fact := range []string{"orders", "502", "503", "STOPPED"} {
		if !strings.Contains(draft.MentorDraft, fact) {
			t.Fatalf("lost followup %s: %s", fact, draft.MentorDraft)
		}
	}
}

func TestDraftRequestWordOrderAndInformationIntent(t *testing.T) {
	team := load(t)
	for _, question := range []string{"давай вопрос наставнику подготовим", "помоги составить сообщение ментору", "подготовим обращение наставнику", "Подготовь вопрос наставнику"} {
		if !teamhelp.WantsEscalation(question) {
			t.Fatalf("missed draft intent: %s", question)
		}
	}
	for _, question := range []string{"Как подготовить PR к ревью наставника?", "Как составить вопрос наставнику?", "Не нужно готовить сообщение наставнику"} {
		if teamhelp.WantsEscalation(question) {
			t.Fatalf("non-draft request: %s", question)
		}
	}
	message := teamhelp.Answer(domain.ChatInput{Text: "про графану что знаешь?", Team: team})
	if message.Help.Kind == "access" || message.MentorDraft != "" {
		t.Fatalf("general question became access request: %+v", message)
	}
}
