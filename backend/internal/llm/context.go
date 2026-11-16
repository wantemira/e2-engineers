package llm

import (
	"strings"

	"github.com/wantemira/e2-engineers/backend/internal/domain"
)

// Routing does not need the PR diff or unrelated document bodies. The review
// runner loads the complete authorized PR and team context after tool selection.
func routingInput(input domain.ChatInput, skills any) map[string]any {
	team := input.Team
	if input.Knowledge != nil {
		selected := map[string]bool{"context": true}
		for _, hit := range input.Knowledge.Search.Hits {
			selected[strings.SplitN(hit.Reference, "#", 2)[0]] = true
		}
		team.Documents = append([]domain.Document(nil), team.Documents...)
		for i := range team.Documents {
			if !selected[team.Documents[i].ID] {
				team.Documents[i].Body = ""
			}
		}
	}
	var review *domain.ReviewRun
	if input.CurrentReview != nil {
		copy := *input.CurrentReview
		copy.Diff = ""
		review = &copy
	}
	return map[string]any{
		"question": input.Text, "history": boundedHistory(input.History), "current_review": review,
		"team": team, "skill_instructions": skills, "knowledge_evidence": input.Knowledge,
	}
}

func boundedHistory(history []domain.Message) []domain.Message {
	if len(history) > 20 {
		history = history[len(history)-20:]
	}
	result := make([]domain.Message, 0, len(history))
	for _, message := range history {
		result = append(result, domain.Message{Role: message.Role, Text: historyText(message.Text), Links: message.Links, MentorDraft: historyText(message.MentorDraft)})
	}
	return result
}

func historyText(text string) string {
	runes := []rune(text)
	if len(runes) > 3000 {
		return string(runes[:3000]) + "…"
	}
	return text
}
