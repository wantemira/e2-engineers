package teamhelp

import (
	"fmt"
	"strings"

	"github.com/wantemira/e2-engineers/backend/internal/domain"
)

func Escalate(input domain.ChatInput) domain.Message {
	question := strings.TrimSpace(input.Text)
	generic := contains(strings.ToLower(question), "наставник", "ментор") && !contains(strings.ToLower(question), "ошиб", "не могу", "не получается", "403", "500", "502", "pr #", "доступ", "ресурс", ":")
	continuation := contains(strings.ToLower(question), "уже проверил", "уже пробовал", "не помогло", "всё равно", "все равно")
	context := []string{}
	seen := map[string]bool{}
	for _, message := range input.History {
		text := strings.TrimSpace(message.Text)
		if message.Role != "user" || text == "" || text == question || seen[text] || (!generic && !continuation && !related(question, text)) {
			continue
		}
		if contains(strings.ToLower(text), "не могу", "не получается", "ошиб", "timeout", "403", "502", "пробовал", "проверил", "ожидаю") {
			context = append(context, text)
			seen[text] = true
		}
	}
	if len(context) > 6 {
		context = context[len(context)-6:]
	}
	var lastSearch *domain.Investigation
	for i := len(input.History) - 1; i >= 0; i-- {
		m := input.History[i]
		if m.Role == "assistant" && m.Help != nil && m.Help.Investigation != nil {
			lastSearch = m.Help.Investigation
			break
		}
	}
	if generic && len(context) == 0 && lastSearch != nil {
		question = lastSearch.Search.Query
	}
	if generic && len(context) == 0 && input.CurrentReview == nil && lastSearch == nil {
		return domain.Message{Role: "assistant", Text: "Опишите, что вы пытались сделать, какую ошибку получили и что уже проверили. По этим данным подготовлю обращение наставнику.", Help: Trace(input.Team, "clarify", "")}
	}
	if generic && len(context) > 0 {
		question = context[len(context)-1]
	}
	if generic && len(context) == 0 && input.CurrentReview != nil && lastSearch == nil {
		question = fmt.Sprintf("Нужна помощь с результатом самопроверки PR #%d. Конкретный вопрос пока не уточнён.", input.CurrentReview.PR.Number)
	}
	if len([]rune(question)) > 1200 {
		question = string([]rune(question)[:1200]) + "…"
	}
	draft := fmt.Sprintf("Привет, %s! Нужна помощь.\n\nВопрос: %s", input.Team.Mentor.Name, question)
	if len(context) > 0 {
		draft += "\n\nКонтекст и уже выполненные проверки со слов стажёра:"
		for _, line := range context {
			runes := []rune(line)
			if len(runes) > 1200 {
				line = string(runes[:1200]) + "…"
			}
			draft += "\n- " + line
		}
	} else {
		draft += "\n\nУже выполненные проверки: стажёр пока не указал; перед отправкой стоит дополнить."
	}
	if review := input.CurrentReview; review != nil {
		draft += fmt.Sprintf("\n\nТекущая самопроверка: PR #%d, SHA %s. CI: %s — %s.", review.PR.Number, review.HeadSHA, review.Checks.Status, review.Checks.Label)
		if review.PR.URL != "" {
			draft += "\nСсылка: " + review.PR.URL
		}
		if review.Checks.RunID != "" {
			draft += "\nID mock CI: " + review.Checks.RunID
		}
		draft += "\nСамопроверка не является одобрением PR и не означает запуск настоящих тестов."
	}
	// Only persisted tool evidence is included, never assistant prose as proof.
	for i := len(input.History) - 1; i >= 0; i-- {
		m := input.History[i]
		if m.Role != "assistant" || m.Help == nil || m.Help.Investigation == nil {
			continue
		}
		evidence := m.Help.Investigation
		if !generic && !continuation && !related(question, evidence.Search.Query) {
			continue
		}
		draft += "\n\nПроверки Buddy (" + evidence.CheckedAt + "):\nПоиск по локальным материалам демо-команды: " + evidence.Search.Query
		if len(evidence.Search.Hits) == 0 {
			draft += "\nПодходящих разделов не найдено."
		}
		for _, hit := range evidence.Search.Hits {
			if link, _, ok := domain.DocumentSource(input.Team, hit.Reference); ok {
				draft += "\nИсточник: " + link.Label + " — " + link.URL
			}
		}
		for _, observation := range evidence.Observations {
			draft += "\n- " + observation
		}
		draft += "\nДиагностика синтетическая; проверка реального сервиса не выполнялась."
		break
	}
	draft += "\n\nПодскажешь следующий шаг или правильного владельца вопроса?"
	message := domain.Message{Role: "assistant", Text: "Собрал известный контекст для наставника; проверьте и при необходимости дополните черновик. Сообщение не отправлено.", MentorDraft: draft, Help: Trace(input.Team, "escalation", "")}
	if link, _, ok := domain.DocumentSource(input.Team, "context#Обращение к наставнику"); ok {
		message.Links = []domain.SourceLink{link}
	}
	return message
}

// Keep an explicit new question from inheriting checks about an unrelated problem.
func related(a, b string) bool {
	a = strings.ToLower(a)
	b = strings.ToLower(b)
	for _, word := range strings.FieldsFunc(a, func(r rune) bool { return !(r >= 'а' && r <= 'я' || r >= 'a' && r <= 'z' || r >= '0' && r <= '9') }) {
		if len([]rune(word)) < 3 || contains("подготовь подготовить вопрос обращение сообщение наставнику наставник ментор сформируй доступ нужен получаю ошибка ошибки могу запустить уже пробовал проверил", word) {
			continue
		}
		if strings.Contains(b, word) {
			return true
		}
	}
	return false
}
