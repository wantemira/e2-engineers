// Package teamhelp serves trusted local documents, access links and assistant handoffs.
package teamhelp

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"regexp"
	"strings"

	"github.com/wantemira/e2-engineers/backend/internal/domain"
)

func Enabled(team domain.Team) bool {
	if team.Help == nil {
		return false
	}
	for _, skill := range team.Skills {
		if skill.ID == team.Capabilities["team_help"] && skill.Contract == domain.HelpSkillContract {
			return true
		}
	}
	return false
}

func Trace(team domain.Team, kind, target string) *domain.HelpTrace {
	for _, skill := range team.Skills {
		if skill.ID == team.Capabilities["team_help"] {
			digest := sha256.Sum256([]byte(skill.Instructions))
			return &domain.HelpTrace{Kind: kind, TargetID: target, Skill: skill.SkillRef, InstructionsSHA256: hex.EncodeToString(digest[:])}
		}
	}
	return nil
}

func missing() error {
	return domain.Problem("HELP_NOT_FOUND", "Источник, ресурс или помощник не настроен для команды.")
}

func Document(team domain.Team, reference string) (domain.Message, error) {
	link, body, ok := domain.DocumentSource(team, reference)
	if !ok || strings.TrimSpace(body) == "" {
		return domain.Message{}, missing()
	}
	return domain.Message{Role: "assistant", Text: body, Links: []domain.SourceLink{link}, Help: Trace(team, "docs", reference)}, nil
}

func Access(input domain.ChatInput, id string) (domain.Message, error) {
	if !Enabled(input.Team) {
		return domain.Message{}, missing()
	}
	for _, resource := range input.Team.Help.Access {
		if resource.ID != id {
			continue
		}
		message, err := Document(input.Team, resource.DocumentID+"#"+resource.Section)
		if err != nil {
			return domain.Message{}, err
		}
		message.Links = append(message.Links, domain.SourceLink{Label: "Заявка: " + resource.Name + " (демо)", URL: resource.RequestURL})
		message.Help = Trace(input.Team, "access", id)
		return message, nil
	}
	return domain.Message{}, missing()
}

func Handoff(input domain.ChatInput, id string) (domain.Message, error) {
	if !Enabled(input.Team) {
		return domain.Message{}, missing()
	}
	for _, route := range input.Team.Help.Assistants {
		if route.ID != id {
			continue
		}
		link, _, ok := domain.DocumentSource(input.Team, route.DocumentID+"#"+route.Section)
		if !ok {
			return domain.Message{}, missing()
		}
		link.Label = "Профиль: " + route.Name + " (демо)"
		if route.URL != "" {
			link.URL = route.URL
		}
		return domain.Message{Role: "assistant", Text: fmt.Sprintf("С этим вопросом поможет %s. %s\n\nЭто демонстрационная маршрутизация: сообщение не отправлено. Откройте профиль помощника и передайте вопрос:\n%s", route.Name, route.Description, input.Text), Links: []domain.SourceLink{link}, Help: Trace(input.Team, "handoff", id)}, nil
	}
	return domain.Message{}, missing()
}

func score(text string, keywords []string) int {
	best := 0
	for _, word := range keywords {
		matched := strings.Contains(text, strings.ToLower(word))
		if len([]rune(word)) <= 3 {
			matched = mentions(text, []string{word})
		}
		if matched && len([]rune(word)) > best {
			best = len([]rune(word))
		}
	}
	return best
}

func mentions(text string, aliases []string) bool {
	for _, alias := range aliases {
		pattern := `(?i)(^|[^\pL\pN])` + regexp.QuoteMeta(alias) + `($|[^\pL\pN])`
		if regexp.MustCompile(pattern).MatchString(text) {
			return true
		}
	}
	return false
}

// Answer is a deterministic offline workflow; Live chooses targets semantically.
func Answer(input domain.ChatInput) domain.Message {
	if !Enabled(input.Team) {
		return Escalate(input)
	}
	text := strings.ToLower(strings.TrimSpace(input.Text))
	if WantsEscalation(text) {
		return Escalate(input)
	}
	if strings.Contains(text, "доступ") || strings.Contains(text, "access") || strings.Contains(text, "permission") {
		for _, resource := range input.Team.Help.Access {
			if mentions(text, append([]string{resource.Name}, resource.Aliases...)) {
				message, _ := Access(input, resource.ID)
				return message
			}
		}
		if strings.TrimSpace(text) == "нужен доступ" || strings.TrimSpace(text) == "как получить доступ?" {
			names := []string{}
			for _, resource := range input.Team.Help.Access {
				names = append(names, resource.Name)
			}
			return domain.Message{Role: "assistant", Text: "К какому ресурсу нужен доступ? В каталоге есть: " + strings.Join(names, ", ") + ".", Help: Trace(input.Team, "clarify", "")}
		}
		return SearchRequest(input)
	}
	best, selected := 0, ""
	for _, route := range input.Team.Help.Assistants {
		if value := score(text, route.Keywords); value > best {
			best, selected = value, route.ID
		}
	}
	if selected != "" {
		message, _ := Handoff(input, selected)
		return message
	}
	if text == "кто мой наставник?" || text == "кто мой ментор?" {
		message := domain.Message{Role: "assistant", Text: "Ваш наставник — " + input.Team.Mentor.Name + ".", Help: Trace(input.Team, "docs", "context")}
		if link, _, ok := domain.DocumentSource(input.Team, "context#Обращение к наставнику"); ok {
			message.Links = []domain.SourceLink{link}
		}
		return message
	}
	if contains(text, "не могу", "не получается", "не знаю", "уже пробовал", "уже проверил", "не помогло", "502", "500", "timeout") {
		return SearchRequest(input)
	}
	best, selected = 0, ""
	for _, topic := range input.Team.Help.Topics {
		if value := score(text, topic.Keywords); value > best {
			best, selected = value, topic.ID
		}
	}
	for _, topic := range input.Team.Help.Topics {
		if topic.ID == selected {
			message, _ := Document(input.Team, topic.DocumentID+"#"+topic.Section)
			return message
		}
	}
	if contains(text, "наставник", "ментор") {
		return Escalate(input)
	}
	if contains(text, "задач", "сервис", "репозит", "депло", "deploy", "ошиб", "окружен", "настро", "рабоч") {
		return SearchRequest(input)
	}
	return domain.Message{Role: "assistant", Text: "В материалах команды не нашёл ответа. Уточните рабочий вопрос: что хотите сделать и где возникло затруднение?", Help: Trace(input.Team, "clarify", "")}
}

func contains(text string, values ...string) bool {
	for _, value := range values {
		if strings.Contains(text, value) {
			return true
		}
	}
	return false
}

// SearchRequest is an internal directive, resolved before storing an assistant message.
func SearchRequest(input domain.ChatInput) domain.Message {
	return domain.Message{Role: "assistant", Help: Trace(input.Team, "search", "")}
}

// Escalation requires the user's intent; an unknown question alone is not consent.
func WantsEscalation(text string) bool {
	text = strings.ToLower(text)
	return contains(text, "наставник", "ментор") && contains(text, "подготовь вопрос", "подготовь обращение", "подготовь сообщение", "подготовить вопрос", "черновик", "сформируй", "обратиться")
}
