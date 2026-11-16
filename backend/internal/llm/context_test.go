package llm

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/wantemira/e2-engineers/backend/internal/domain"
)

func TestRoutingContextKeepsEvidenceAndDoesNotMutateSavedReview(t *testing.T) {
	team := domain.Team{Documents: []domain.Document{
		{ID: "context", Body: "Team context"}, {ID: "guide", Body: "Relevant guide"}, {ID: "unrelated", Body: "UNRELATED_BODY"},
	}}
	review := &domain.ReviewRun{Diff: strings.Repeat("LARGE_DIFF", 10000), Findings: []domain.Finding{{Title: "Actual finding"}}}
	history := []domain.Message{{Role: "user", Text: strings.Repeat("Я", 6000), Skill: &domain.SkillExecution{RunID: "not-needed-for-routing"}}}
	input := domain.ChatInput{Text: "Explain", Team: team, CurrentReview: review, History: history,
		Knowledge: &domain.Investigation{Search: domain.KnowledgeSearch{Hits: []domain.KnowledgeHit{{Reference: "guide#section-1"}}}}}
	payload, err := json.Marshal(routingInput(input, "Trusted skill instructions"))
	if err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{"Team context", "Relevant guide", "Actual finding", "Trusted skill instructions"} {
		if !strings.Contains(string(payload), expected) {
			t.Fatalf("lost routing context: %s", expected)
		}
	}
	for _, unwanted := range []string{"UNRELATED_BODY", "LARGE_DIFF", "not-needed-for-routing"} {
		if strings.Contains(string(payload), unwanted) {
			t.Fatalf("unbounded routing context: %s", unwanted)
		}
	}
	if team.Documents[2].Body != "UNRELATED_BODY" || review.Diff == "" || len([]rune(history[0].Text)) != 6000 {
		t.Fatal("saved data mutated")
	}
}
