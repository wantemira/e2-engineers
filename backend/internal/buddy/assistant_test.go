package buddy

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func loadTestTeam(t *testing.T) Team {
	t.Helper()
	root, err := filepath.Abs("../../..")
	if err != nil {
		t.Fatal(err)
	}
	team, err := LoadTeam(root)
	if err != nil {
		t.Fatal(err)
	}
	return team
}

func TestLoadTeam(t *testing.T) {
	team := loadTestTeam(t)
	if team.ID != "backend" || team.PRNumber != 17 || team.Mentor.ID != "mentor-backend" || team.RepositoryURL != "https://github.com/wantemira/e2-engineers" {
		t.Fatalf("unexpected team configuration: %+v", team)
	}
	if len(team.Documents) != 4 || team.Skill == "" || team.Rules["GO-ERR-01"].ID != "GO-ERR-01" {
		t.Fatalf("team knowledge was not loaded: %+v", team)
	}
}

func TestLoadTeamRejectsDocumentTraversal(t *testing.T) {
	root := t.TempDir()
	directory := filepath.Join(root, "teams", "demo-backend")
	if err := os.MkdirAll(directory, 0755); err != nil {
		t.Fatal(err)
	}
	original, err := os.ReadFile("../../../teams/demo-backend/context.json")
	if err != nil {
		t.Fatal(err)
	}
	data := strings.Replace(string(original), `"file": "context.md"`, `"file": "../../outside.md"`, 1)
	if err := os.WriteFile(filepath.Join(directory, "context.json"), []byte(data), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadTeam(root); err == nil || !strings.Contains(err.Error(), "invalid document") {
		t.Fatalf("expected document path validation, got %v", err)
	}
}

func TestLoadTeamMissingConfiguration(t *testing.T) {
	if _, err := LoadTeam(t.TempDir()); err == nil {
		t.Fatal("missing configuration must fail")
	}
}

func TestLocalAnswersUseTeamDocuments(t *testing.T) {
	team := loadTestTeam(t)
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
			message, err := assistant.Answer(context.Background(), ChatInput{Text: test.question, Team: team})
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
	message, err := assistant.Answer(context.Background(), ChatInput{Text: "Какой прогноз погоды?", Team: team})
	if err != nil || !strings.Contains(message.Text, "Уточните рабочий вопрос") || len(message.Links) != 0 {
		t.Fatalf("unsupported question must ask for context, got %+v, %v", message, err)
	}
}

func TestLocalAnswerReadsUpdatedDocumentContent(t *testing.T) {
	team := loadTestTeam(t)
	for index := range team.Documents {
		if team.Documents[index].ID == "access-guide" {
			team.Documents[index].Body = "# Доступы\n\n## Доступ к инструменту\n\nОбновлённый процесс команды.\n"
		}
	}
	message, err := NewLocalAssistant().Answer(context.Background(), ChatInput{Text: "Нужен доступ", Team: team})
	if err != nil || message.Text != "Обновлённый процесс команды." {
		t.Fatalf("expected answer from current document, got %+v, %v", message, err)
	}
}

func TestLocalFindingExplanations(t *testing.T) {
	team := loadTestTeam(t)
	rule := team.Rules["GO-ERR-01"]
	review := &ReviewRun{Findings: []Finding{
		{ID: "F1", Title: "Теряется причина ошибки", Description: "Сохраните причину через %w.", Rule: &rule},
		{ID: "F2", Title: "Проверьте тесты", Description: "Проверьте ветку ошибки."},
	}}
	for _, question := range []string{"Объясни замечание 1", "Почему теряется причина ошибки?", "Объясни F1"} {
		message, err := NewLocalAssistant().Answer(context.Background(), ChatInput{Text: question, Team: team, CurrentReview: review})
		if err != nil || !strings.Contains(message.Text, "%w") || len(message.Links) != 1 {
			t.Fatalf("unexpected explanation for %q: %+v, %v", question, message, err)
		}
	}
	message, err := NewLocalAssistant().Answer(context.Background(), ChatInput{
		Text: "Почему это важно?", Team: team, CurrentReview: review,
		History: []Message{{Role: "user", Text: "Как сохранить причину ошибки?"}},
	})
	if err != nil || !strings.Contains(message.Text, "%w") {
		t.Fatalf("history reference was lost: %+v, %v", message, err)
	}
	message, err = NewLocalAssistant().Answer(context.Background(), ChatInput{Text: "Объясни замечание 9", Team: team, CurrentReview: review})
	if err != nil || !strings.Contains(message.Text, "Укажите номер") {
		t.Fatalf("invalid reference should clarify: %+v, %v", message, err)
	}
	message, err = NewLocalAssistant().Answer(context.Background(), ChatInput{Text: "Объясни замечания подробнее", Team: team, CurrentReview: review})
	if err != nil || !strings.Contains(message.Text, "Какое замечание") {
		t.Fatalf("ambiguous reference should clarify: %+v, %v", message, err)
	}
}

func TestLocalAssistantCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	assistant := NewLocalAssistant()
	if _, err := assistant.Answer(ctx, ChatInput{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("answer must honor cancellation: %v", err)
	}
	if _, err := assistant.Review(ctx, PullRequest{}, Checks{}, Team{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("review must honor cancellation: %v", err)
	}
}

const testDiff = "diff --git a/service.go b/service.go\n--- a/service.go\n+++ b/service.go\n@@ -39,3 +39,3 @@\n if err != nil {\n- return err\n+ return errors.New(\"save failed\")\n }\n"
const addedTest = "diff --git a/service_test.go b/service_test.go\n--- /dev/null\n+++ b/service_test.go\n@@ -0,0 +1,3 @@\n+func TestSave(t *testing.T) {\n+ t.Fatal(\"assert behavior\")\n+}\n"

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
			findings, err := NewLocalAssistant().Review(context.Background(), PullRequest{Diff: test.diff}, Checks{Status: "UNKNOWN"}, team)
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
	findings, err := NewLocalAssistant().Review(context.Background(), PullRequest{Diff: testDiff}, Checks{}, loadTestTeam(t))
	if err != nil {
		t.Fatal(err)
	}
	if len(findings) != 2 || findings[0].Location.Path != "service.go" || findings[0].Location.Line != 40 || findings[0].Rule.ID != "GO-ERR-01" {
		t.Fatalf("incorrect diff reference: %+v", findings)
	}
}

func TestLocalReviewDoesNotCarryErrorBranchAcrossHunks(t *testing.T) {
	diff := "diff --git a/service.go b/service.go\n--- a/service.go\n+++ b/service.go\n@@ -1,1 +1,1 @@\n if err != nil {\n@@ -30,1 +30,1 @@\n+ return errors.New(\"validation failed\")\n" + addedTest
	findings, err := NewLocalAssistant().Review(context.Background(), PullRequest{Diff: diff}, Checks{}, loadTestTeam(t))
	if err != nil || len(findings) != 0 {
		t.Fatalf("unrelated hunk must not inherit error branch: %+v, %v", findings, err)
	}
}

func TestLocalReviewRejectsLargeAndMalformedDiff(t *testing.T) {
	assistant := NewLocalAssistant()
	_, err := assistant.Review(context.Background(), PullRequest{Diff: strings.Repeat("x", 200*1024+1)}, Checks{}, Team{})
	var appError *AppError
	if !errors.As(err, &appError) || appError.Code != "DIFF_TOO_LARGE" {
		t.Fatalf("expected diff size error, got %v", err)
	}
	_, err = assistant.Review(context.Background(), PullRequest{Diff: "+++ b/service.go\n@@ malformed @@\n"}, Checks{}, Team{})
	if err == nil {
		t.Fatal("malformed hunk must fail")
	}
}
