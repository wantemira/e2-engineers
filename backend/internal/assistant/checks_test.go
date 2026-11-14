package assistant

import (
	"context"
	"strings"
	"testing"

	"github.com/wantemira/e2-engineers/backend/internal/domain"
)

func TestCIExplanationSeparatesEvidenceAndUnknownCauses(t *testing.T) {
	checks := domain.Checks{Status: "FAILED", Tests: []domain.TestResult{{Name: "TestCreateRepositoryError", Status: "FAILED", Error: "expected wrapped storage error, got create order failed"}}}
	explanation, tests := assessChecks(checks, true, "")
	if !strings.Contains(explanation, "fmt.Errorf") || !strings.Contains(explanation, "TestCreateRepositoryError") || len(tests) < 1 {
		t.Fatalf("missing explanation: %s %+v", explanation, tests)
	}
	explanation, _ = assessChecks(checks, false, "")
	if !strings.Contains(explanation, "Данных недостаточно") {
		t.Fatal("invented cause without diff evidence")
	}
	for _, status := range []string{"FAILED", "UNKNOWN", "RUNNING", "PASSED"} {
		text, _ := assessChecks(domain.Checks{Status: status}, false, "")
		if text == "" {
			t.Fatalf("missing %s explanation", status)
		}
	}
	message, err := NewLocalAssistant().Answer(context.Background(), domain.ChatInput{Text: "Почему упал тест?", CurrentReview: &domain.ReviewRun{Assessment: &domain.ReviewAssessment{CI: explanation, SuggestedTests: tests}}})
	if err != nil || !strings.Contains(message.Text, "Рекомендуемые тесты") {
		t.Fatalf("follow-up lost CI: %+v %v", message, err)
	}
}
