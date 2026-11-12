package assistant

import (
	"context"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/wantemira/e2-engineers/backend/internal/domain"
)

type LocalAssistant struct{}

func NewLocalAssistant() *LocalAssistant { return &LocalAssistant{} }

var findingReference = regexp.MustCompile(`(?i)(?:пункт|замечани[ея]|пункту|замечанию|f)\s*[№#]?\s*(\d+)`)

func (*LocalAssistant) Answer(ctx context.Context, input domain.ChatInput) (domain.Message, error) {
	if err := ctx.Err(); err != nil {
		return domain.Message{}, err
	}
	text := strings.ToLower(strings.TrimSpace(input.Text))
	if finding, explicit := referencedFinding(text, input.CurrentReview); explicit {
		if finding == nil {
			return reply("Укажите номер замечания из текущего отчёта. Например, «Объясни замечание 1»."), nil
		}
		return explain(*finding), nil
	}
	if containsAny(text, "больнич", "болею", "заболел") {
		message := documentAnswer(input.Team, "people-guide", "Больничный")
		if input.Team.PeopleAgentURL != "" {
			message.Text = "По порядку оформления больничного обратитесь к People-агенту."
			message.Links = append([]domain.SourceLink{{Label: "Открыть People-агента", URL: input.Team.PeopleAgentURL}}, message.Links...)
		} else {
			message.MentorDraft = "Привет! Подскажи, пожалуйста, как оформить больничный и где найти актуальную инструкцию или контакт People-команды."
		}
		return message, nil
	}
	if containsAny(text, "доступ", "permission", "access") {
		return documentAnswer(input.Team, "access-guide", "Доступ к инструменту"), nil
	}
	if containsAny(text, "наставник", "ментор", "buddy", "кому обратиться", "не получается", "не могу") {
		message := reply(fmt.Sprintf("С этим вопросом может помочь ваш наставник — %s. Заполните поля в черновике перед отправкой.", input.Team.Mentor.Name))
		message.Links = []domain.SourceLink{documentLink(input.Team, "context")}
		message.MentorDraft = "Привет! Нужна помощь с [задача или ссылка на PR]. Ожидаю [результат], сейчас получаю [ошибка или фактический результат]. Уже проверил(а) [что пробовали]. Подскажешь следующий шаг?"
		return message, nil
	}
	if containsAny(text, "почему", "объясни", "объяснит", "подробнее", "как исправить", "что значит", "что это значит") && input.CurrentReview != nil {
		if finding := matchingFinding(text, input.CurrentReview); finding != nil {
			return explain(*finding), nil
		}
		if containsAny(text, "это", "так", "подробнее") {
			for index := len(input.History) - 1; index >= 0; index-- {
				previous := input.History[index]
				if previous.Role == "user" && previous.Text != input.Text {
					if finding := matchingFinding(strings.ToLower(previous.Text), input.CurrentReview); finding != nil {
						return explain(*finding), nil
					}
					break
				}
			}
		}
		if len(input.CurrentReview.Findings) == 1 {
			return explain(input.CurrentReview.Findings[0]), nil
		}
		if len(input.CurrentReview.Findings) > 1 && containsAny(text, "замечан", "пункт", "это", "подробнее") {
			return reply("Какое замечание разобрать? Укажите его номер или название из отчёта."), nil
		}
	}

	switch {
	case containsAny(text, "%w", "обработк", "причин", "errors.is", "errors.as", "обернуть", "оборачива"):
		return documentAnswer(input.Team, "review-rules", "GO-ERR-01"), nil
	case containsAny(text, "тест", "test", "ci", "пайплайн", "сборк"):
		return documentAnswer(input.Team, "review-rules", "GO-TEST-01"), nil
	case containsAny(text, "правил", "соглаш", "ревью", "review", "pull request", "pr", "формат"):
		return documentAnswer(input.Team, "review-rules", "Самопроверка"), nil
	case containsAny(text, "привет", "здравств", "добрый", "доброе", "помоги", "помощь", "что умеешь", "что можешь", "начать"):
		return documentAnswer(input.Team, "context", "Помощь"), nil
	default:
		return reply("Уточните рабочий вопрос: о каком инструменте, правиле или замечании идёт речь? Для проверки пришлите номер или ссылку на PR. Если проблема требует решения команды, помогу подготовить вопрос наставнику."), nil
	}
}

func reply(text string) domain.Message { return domain.Message{Role: "assistant", Text: text} }

func containsAny(text string, values ...string) bool {
	for _, value := range values {
		if strings.Contains(text, value) {
			return true
		}
	}
	return false
}

func referencedFinding(text string, review *domain.ReviewRun) (*domain.Finding, bool) {
	match := findingReference.FindStringSubmatch(text)
	if match == nil {
		return nil, false
	}
	index, err := strconv.Atoi(match[1])
	if err != nil || review == nil || index < 1 || index > len(review.Findings) {
		return nil, true
	}
	return &review.Findings[index-1], true
}

func matchingFinding(text string, review *domain.ReviewRun) *domain.Finding {
	for index := range review.Findings {
		finding := &review.Findings[index]
		if strings.Contains(text, strings.ToLower(finding.Title)) || (finding.Location != nil && strings.Contains(text, strings.ToLower(finding.Location.Path))) {
			return finding
		}
		if finding.Rule != nil {
			if finding.Rule.ID == "GO-ERR-01" && containsAny(text, "ошибк", "причин", "%w", "errors.") {
				return finding
			}
			if finding.Rule.ID == "GO-TEST-01" && containsAny(text, "тест", "test") {
				return finding
			}
		}
	}
	return nil
}

func explain(finding domain.Finding) domain.Message {
	message := reply(finding.Title + "\n\n" + finding.Description)
	if finding.Rule != nil {
		message.Links = []domain.SourceLink{{Label: finding.Rule.Label, URL: finding.Rule.URL}}
	}
	return message
}

func documentLink(team domain.Team, id string) domain.SourceLink {
	for _, document := range team.Documents {
		if document.ID == id {
			return domain.SourceLink{Label: document.Title, URL: "/api/v1/context/documents/" + id}
		}
	}
	return domain.SourceLink{}
}

func documentAnswer(team domain.Team, id, heading string) domain.Message {
	for _, document := range team.Documents {
		if document.ID == id {
			text := section(document.Body, heading)
			if text == "" {
				return reply("В командном документе пока нет ответа на этот вопрос. Уточните порядок у наставника.")
			}
			message := reply(text)
			message.Links = []domain.SourceLink{documentLink(team, id)}
			return message
		}
	}
	return reply("Командный документ недоступен. Уточните порядок у наставника.")
}

func section(body, heading string) string {
	lines := strings.Split(body, "\n")
	start := -1
	for index, line := range lines {
		if strings.TrimSpace(line) == "## "+heading {
			start = index + 1
			continue
		}
		if start >= 0 && strings.HasPrefix(line, "## ") {
			return strings.TrimSpace(strings.Join(lines[start:index], "\n"))
		}
	}
	if start >= 0 {
		return strings.TrimSpace(strings.Join(lines[start:], "\n"))
	}
	return ""
}
