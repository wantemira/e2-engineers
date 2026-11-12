package assistant

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/wantemira/e2-engineers/backend/internal/domain"
	"github.com/wantemira/e2-engineers/backend/internal/teamconfig"
)

func loadTestTeam(t *testing.T) domain.Team {
	t.Helper()
	root, err := filepath.Abs("../../..")
	if err != nil {
		t.Fatal(err)
	}
	team, err := teamconfig.New(root).Load()
	if err != nil {
		t.Fatal(err)
	}
	return team
}
func TestLocalAnswersUseTeamDocuments(t *testing.T) {
	team := loadTestTeam(t)
	team.PeopleAgentURL = ""
	assistant := NewLocalAssistant()
	tests := []struct {
		name     string
		question string
		contains string
		document string
	}{
		{"greeting", "Привет", "PR №17", "context"},
		{"mentor", "Кто мой наставник?", team.Mentor.Name, "context"},
		{"tests", "Где запускать тесты?", "go test ./...", "review-rules"},
		{"error wrapping", "Почему надо сохранять причину ошибки?", "%w", "review-rules"},
		{"rules", "Как подготовить PR?", "gofmt", "review-rules"},
		{"access", "Как получить доступ к репозиторию?", "уровень доступа", "access-guide"},
		{"people", "Как оформить больничный?", "People", "people-guide"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			message, err := assistant.Answer(context.Background(), domain.ChatInput{Text: test.question, Team: team})
			if err != nil {
				t.Fatal(err)
			}
			if message.Role != "assistant" || !strings.Contains(message.Text, test.contains) {
				t.Fatalf("unexpected answer: %+v", message)
			}
			if len(message.Links) != 1 || message.Links[0].URL != "/api/v1/context/documents/"+test.document {
				t.Fatalf("missing document source: %+v", message.Links)
			}
		})
	}
	message, err := assistant.Answer(context.Background(), domain.ChatInput{Text: "Какой прогноз погоды?", Team: team})
	if err != nil || !strings.Contains(message.Text, "Уточните рабочий вопрос") || len(message.Links) != 0 {
		t.Fatalf("unsupported question must ask for context, got %+v, %v", message, err)
	}
}

func TestPeopleAnswerUsesConfiguredAgentOrMentorDraft(t *testing.T) {
	team := loadTestTeam(t)
	assistant := NewLocalAssistant()
	for _, target := range []string{"", "https://people.example.test/"} {
		team.PeopleAgentURL = target
		message, err := assistant.Answer(context.Background(), domain.ChatInput{Text: "Как оформить больничный?", Team: team})
		if err != nil || len(message.Links) == 0 {
			t.Fatalf("unexpected People answer: %+v, %v", message, err)
		}
		if target == "" && message.MentorDraft == "" {
			t.Fatal("expected a copyable draft for a question to the mentor")
		}
		if target != "" && (message.Links[0].URL != target || message.MentorDraft != "") {
			t.Fatalf("expected the configured People agent: %+v", message)
		}
	}
}

func TestLocalAnswerReadsUpdatedDocumentContent(t *testing.T) {
	team := loadTestTeam(t)
	for index := range team.Documents {
		if team.Documents[index].ID == "access-guide" {
			team.Documents[index].Body = "# Доступы\n\n## Доступ к инструменту\n\nОбновлённый процесс команды.\n"
		}
	}
	message, err := NewLocalAssistant().Answer(context.Background(), domain.ChatInput{Text: "Нужен доступ", Team: team})
	if err != nil || message.Text != "Обновлённый процесс команды." {
		t.Fatalf("expected answer from current document, got %+v, %v", message, err)
	}
}

func TestLocalFindingExplanations(t *testing.T) {
	team := loadTestTeam(t)
	rule := team.Rules["GO-ERR-01"]
	review := &domain.ReviewRun{Findings: []domain.Finding{
		{ID: "F1", Title: "Теряется причина ошибки", Description: "Сохраните причину через %w.", Rule: &rule},
		{ID: "F2", Title: "Проверьте тесты", Description: "Проверьте ветку ошибки."},
	}}
	for _, question := range []string{"Объясни замечание 1", "Почему теряется причина ошибки?", "Объясни F1"} {
		message, err := NewLocalAssistant().Answer(context.Background(), domain.ChatInput{Text: question, Team: team, CurrentReview: review})
		if err != nil || !strings.Contains(message.Text, "%w") || len(message.Links) != 1 {
			t.Fatalf("unexpected explanation for %q: %+v, %v", question, message, err)
		}
	}
	message, err := NewLocalAssistant().Answer(context.Background(), domain.ChatInput{
		Text: "Почему это важно?", Team: team, CurrentReview: review,
		History: []domain.Message{{Role: "user", Text: "Как сохранить причину ошибки?"}},
	})
	if err != nil || !strings.Contains(message.Text, "%w") {
		t.Fatalf("history reference was lost: %+v, %v", message, err)
	}
	message, err = NewLocalAssistant().Answer(context.Background(), domain.ChatInput{Text: "Объясни замечание 9", Team: team, CurrentReview: review})
	if err != nil || !strings.Contains(message.Text, "Укажите номер") {
		t.Fatalf("invalid reference should clarify: %+v, %v", message, err)
	}
	message, err = NewLocalAssistant().Answer(context.Background(), domain.ChatInput{Text: "Объясни замечания подробнее", Team: team, CurrentReview: review})
	if err != nil || !strings.Contains(message.Text, "Какое замечание") {
		t.Fatalf("ambiguous reference should clarify: %+v, %v", message, err)
	}
}

func TestLocalAssistantCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	assistant := NewLocalAssistant()
	if _, err := assistant.Answer(ctx, domain.ChatInput{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("answer must honor cancellation: %v", err)
	}
	if _, err := assistant.Review(ctx, domain.PullRequest{}, domain.Checks{}, domain.Team{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("review must honor cancellation: %v", err)
	}
}

const testDiff = "diff --git a/service.go b/service.go\n--- a/service.go\n+++ b/service.go\n@@ -39,3 +39,3 @@\n if err != nil {\n- return err\n+ return errors.New(\"save failed\")\n }\n"
const addedTest = "diff --git a/service_test.go b/service_test.go\n--- /dev/null\n+++ b/service_test.go\n@@ -0,0 +1,3 @@\n+func TestSave(t *testing.T) {\n+\tt.Fatal(\"assert behavior\")\n+}\n"

func TestLocalReviewDiff(t *testing.T) {
	team := loadTestTeam(t)
	tests := []struct {
		name      string
		diff      string
		findings  int
		firstKind string
	}{
		{"lost cause", testDiff, 2, "problem"},
		{"lost cause with tests", testDiff + addedTest, 1, "problem"},
		{"fixed with tests", strings.Replace(testDiff, `errors.New("save failed")`, `fmt.Errorf("save: %w", err)`, 1) + addedTest, 0, ""},
		{"fixed visible change without tests", strings.Replace(testDiff, `errors.New("save failed")`, `fmt.Errorf("save: %w", err)`, 1), 1, "assumption"},
		{"formatted error drops cause", strings.Replace(testDiff, `errors.New("save failed")`, `fmt.Errorf("save: %v", err)`, 1) + addedTest, 1, "problem"},
		{"empty diff", "", 0, ""},
		{"removed error is ignored", strings.Replace(testDiff, `+ return errors.New("save failed")`, `+ return err`, 1) + addedTest, 0, ""},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			analysis, err := NewLocalAssistant().Review(context.Background(), domain.PullRequest{Diff: test.diff}, domain.Checks{Status: "UNKNOWN"}, team)
			findings := analysis.Findings
			if err != nil {
				t.Fatal(err)
			}
			if findings == nil || len(findings) != test.findings {
				t.Fatalf("expected %d findings, got %+v", test.findings, findings)
			}
			if len(findings) > 0 && findings[0].Kind != test.firstKind {
				t.Fatalf("unexpected finding kind: %+v", findings[0])
			}
		})
	}
}

func TestLocalReviewPreservesNewLineNumber(t *testing.T) {
	analysis, err := NewLocalAssistant().Review(context.Background(), domain.PullRequest{Diff: testDiff}, domain.Checks{}, loadTestTeam(t))
	findings := analysis.Findings
	if err != nil {
		t.Fatal(err)
	}
	if len(findings) != 2 || findings[0].Location.Path != "service.go" || findings[0].Location.Line != 40 || findings[0].Rule.ID != "GO-ERR-01" {
		t.Fatalf("incorrect diff reference: %+v", findings)
	}
}

func TestLocalReviewDoesNotCarryErrorBranchAcrossHunks(t *testing.T) {
	diff := "diff --git a/service.go b/service.go\n--- a/service.go\n+++ b/service.go\n@@ -1,1 +1,1 @@\n if err != nil {\n@@ -30,1 +30,1 @@\n+ return errors.New(\"validation failed\")\n" + addedTest
	analysis, err := NewLocalAssistant().Review(context.Background(), domain.PullRequest{Diff: diff}, domain.Checks{}, loadTestTeam(t))
	findings := analysis.Findings
	if err != nil || len(findings) != 0 {
		t.Fatalf("unrelated hunk must not inherit error branch: %+v, %v", findings, err)
	}
}

func TestLocalReviewRejectsLargeAndMalformedDiff(t *testing.T) {
	assistant := NewLocalAssistant()
	_, err := assistant.Review(context.Background(), domain.PullRequest{Diff: strings.Repeat("x", 200*1024+1)}, domain.Checks{}, domain.Team{})
	var appError *domain.AppError
	if !errors.As(err, &appError) || appError.Code != "DIFF_TOO_LARGE" {
		t.Fatalf("expected diff size error, got %v", err)
	}
	_, err = assistant.Review(context.Background(), domain.PullRequest{Diff: "+++ b/service.go\n@@ malformed @@\n"}, domain.Checks{}, domain.Team{})
	if err == nil {
		t.Fatal("malformed hunk must fail")
	}
}
