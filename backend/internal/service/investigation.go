package service

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/wantemira/e2-engineers/backend/internal/domain"
	"github.com/wantemira/e2-engineers/backend/internal/teamhelp"
)

type KnowledgeTools interface {
	Search(context.Context, string, string) (domain.KnowledgeSearch, error)
}

type KnowledgeResponder interface {
	AnswerFromKnowledge(context.Context, domain.ChatInput, domain.Investigation) (domain.Message, error)
}

func (service *Service) investigate(ctx context.Context, actor string, input domain.ChatInput, query string, purposes ...string) (domain.Message, error) {
	if !teamhelp.Enabled(input.Team) {
		return domain.Message{}, domain.Problem("HELP_NOT_FOUND", "Помощь команды не настроена.")
	}
	if service.knowledge == nil {
		return domain.Message{}, &domain.AppError{Code: "DEPENDENCY_UNAVAILABLE", Message: "Поиск по материалам временно недоступен.", Retryable: true}
	}
	purpose := "information"
	if containsAny(strings.ToLower(input.Text), "не могу", "не получается", "не помогло", "ошиб", "502", "500", "timeout", "уже пробовал", "уже проверил") {
		purpose = "troubleshooting"
	} else if containsAny(strings.ToLower(input.Text), "доступ", "permission", "access") {
		purpose = "access"
	}
	if len(purposes) > 0 && purposes[0] != "" {
		purpose = purposes[0]
	}
	followup := containsAny(strings.ToLower(input.Text), "не помогло", "всё равно", "все равно", "ошибка осталась", "уже пробовал", "уже проверил")
	if containsAny(strings.ToLower(input.Text), "не могу", "не получается") {
		followup = false
	}
	var previous *domain.Investigation
	if followup {
		for i := len(input.History) - 1; i >= 0; i-- {
			message := input.History[i]
			if message.Role == "assistant" {
				if message.Help != nil {
					previous = message.Help.Investigation
				}
				break
			}
		}
		if previous != nil {
			query = previous.Search.Query + "\n" + query
		}
	}
	runes := []rune(query)
	if len(runes) > 2000 {
		query = string(runes[len(runes)-2000:])
	}
	var evidence *domain.Investigation
	var err error
	if input.Knowledge != nil && strings.EqualFold(query, input.Knowledge.Search.Query) {
		copy := *input.Knowledge
		copy.Purpose = purpose
		evidence = &copy
	} else {
		evidence, err = service.searchEvidence(ctx, actor, input, query, purpose)
		if err != nil {
			return domain.Message{}, err
		}
	}
	search := evidence.Search
	message := domain.Message{Role: "assistant", Help: teamhelp.Trace(input.Team, "search", "")}
	message.Help.Investigation = evidence
	if len(search.Hits) == 0 {
		message.Text = "Поиск по материалам демо-команды не нашёл подходящего раздела. Уточните, что означает это название и что именно хотите узнать. Могу подготовить вопрос наставнику по вашей просьбе."
		if purpose == "access" {
			message.Text = "Поиск по материалам демо-команды не нашёл инструкции доступа. Уточните точное название ресурса и для какой задачи он нужен. По просьбе подготовлю вопрос наставнику."
		}
		if purpose == "troubleshooting" {
			message.Text = "Поиск по материалам демо-команды не нашёл подходящего раздела. Уточните название ресурса или сервиса, окружение, точный текст ошибки и что уже пробовали. Проверю с этими данными. Если нужна помощь человека — напишите «Подготовь вопрос наставнику»."
		}
		if previous != nil {
			message.Text = "Повторный поиск с уточнениями не нашёл инструкции. Нужны точный текст ошибки, ресурс и окружение. Могу подготовить вопрос наставнику с результатами поиска — напишите «Подготовь вопрос наставнику»."
		}
		return service.finishInvestigation(ctx, input, message)
	}
	message.Text = "Нашёл в материалах демо-команды:\n"
	for index, hit := range search.Hits {
		if index == 0 {
			message.Text += "\n" + hit.Title + "\n" + hit.Excerpt + "\n"
		}
		message.Links = append(message.Links, hit.Source)
	}
	// Diagnostics are restricted to the configured training service, not any mentioned system.
	technical := strings.ToLower(query)
	training := input.Team.Training
	if purpose == "troubleshooting" && training != nil && mentionsTraining(technical, training.Service) {
		observations, err := service.readHelpDiagnostics(ctx, actor, input.Team)
		if err != nil {
			return domain.Message{}, err
		}
		evidence.Observations = observations
		message.Text += "\nЧто проверил Buddy (mock, только чтение):\n- " + strings.Join(observations, "\n- ") + "\nЭти данные не доказывают причину ошибки реального сервиса."
	}
	if previous != nil {
		message.Text += "\n\nПредыдущая рекомендация не помогла. Пришлите точный URL запроса, окружение, ответ /healthz и последние строки логов без секретов. Отделим проблему запуска от ошибки запроса. Если уже собрали эти данные — напишите «Подготовь вопрос наставнику»: включу результаты поиска и mock-проверок."
	} else if purpose != "information" {
		message.Text += "\n\nСледующий шаг: выполните подходящую проверку из найденной инструкции и пришлите результат. Если инструкция не подходит, уточните сервис, окружение и текст ошибки. Я не запускал сервис и не менял доступы."
	}
	return service.finishInvestigation(ctx, input, message)
}

func (service *Service) finishInvestigation(ctx context.Context, input domain.ChatInput, message domain.Message) (domain.Message, error) {
	if responder, ok := service.assistant.(KnowledgeResponder); ok {
		answer, err := responder.AnswerFromKnowledge(ctx, input, *message.Help.Investigation)
		if err != nil {
			return domain.Message{}, err
		}
		answer.Help = message.Help
		return answer, nil
	}
	return message, nil
}

func (service *Service) readHelpDiagnostics(ctx context.Context, actor string, team domain.Team) ([]string, error) {
	pr, err := service.git.GetPullRequest(ctx, actor, team.PRNumber)
	if err != nil {
		return nil, err
	}
	if pr.Number != team.PRNumber || pr.Repository != team.Repository || pr.OwnerID != actor {
		return nil, domain.Problem("NOT_FOUND", "PR недоступен.")
	}
	observations := []string{fmt.Sprintf("PR #%d, SHA %s.", pr.Number, pr.HeadSHA)}
	env, err := service.git.GetTrainingEnvironment(ctx, actor, pr.Number, pr.HeadSHA)
	if err != nil {
		if domain.PublicError(err).Code == "STALE_HEAD" {
			return nil, err
		}
		observations = append(observations, "Статус окружения недоступен; проверка не выполнена.")
	} else {
		if env.Source != "MOCK" || env.PRNumber != pr.Number || env.HeadSHA != pr.HeadSHA || env.Service != team.Training.Service || env.Name != team.Training.Environment || !containsStatus(env.Status) {
			return nil, fmt.Errorf("invalid environment result")
		}
		observations = append(observations, fmt.Sprintf("Окружение %s / %s: %s (MOCK).", env.Service, env.Name, env.Status))
	}
	checks, err := service.git.GetChecks(ctx, actor, pr.Number, pr.HeadSHA)
	if err != nil {
		if domain.PublicError(err).Code == "STALE_HEAD" {
			return nil, err
		}
		observations = append(observations, "CI недоступен; результат не проверен.")
	} else {
		if err := domain.ValidateChecks(checks, pr.Number, pr.HeadSHA); err != nil {
			return nil, err
		}
		observations = append(observations, fmt.Sprintf("CI: %s — %s. Run ID: %s (MOCK).", checks.Status, checks.Label, checks.RunID))
	}
	latest, err := service.git.GetPullRequest(ctx, actor, pr.Number)
	if err != nil {
		return nil, err
	}
	if latest.HeadSHA != pr.HeadSHA {
		return nil, domain.Problem("STALE_HEAD", "PR изменился во время диагностики. Повторите запрос.")
	}
	return observations, nil
}
func containsStatus(value string) bool {
	return value == "STOPPED" || value == "STARTING" || value == "READY" || value == "FAILED"
}

func mentionsTraining(text, service string) bool {
	aliases := []string{strings.ToLower(service), strings.ToLower(strings.TrimSuffix(service, "-training"))}
	for _, word := range strings.FieldsFunc(text, func(r rune) bool { return !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '-') }) {
		for _, alias := range aliases {
			if word == alias && alias != "" {
				return true
			}
		}
	}
	return false
}

func (service *Service) searchEvidence(ctx context.Context, actor string, input domain.ChatInput, query, purpose string) (*domain.Investigation, error) {
	if service.knowledge == nil {
		return nil, &domain.AppError{Code: "DEPENDENCY_UNAVAILABLE", Message: "Поиск по материалам временно недоступен.", Retryable: true}
	}
	runes := []rune(query)
	if len(runes) > 2000 {
		query = string(runes[:2000])
	}
	search, err := service.knowledge.Search(ctx, actor, query)
	if err != nil {
		return nil, err
	}
	// Reconstruct sources from this team's current documents, ignoring tool supplied URLs/text.
	if len(search.Hits) > 5 {
		return nil, fmt.Errorf("knowledge result exceeded hit limit")
	}
	search.Query = query
	for i, hit := range search.Hits {
		link, body, ok := domain.DocumentSource(input.Team, hit.Reference)
		if !ok {
			return nil, fmt.Errorf("knowledge source outside loaded team")
		}
		excerpt := []rune(body)
		if len(excerpt) > 1200 {
			excerpt = append(excerpt[:1200], []rune("…")...)
		}
		search.Hits[i] = domain.KnowledgeHit{Reference: hit.Reference, Title: link.Label, Excerpt: string(excerpt), Source: link}
	}
	return &domain.Investigation{Question: input.Text, Purpose: purpose, Search: search, CheckedAt: time.Now().UTC().Format(time.RFC3339), Observations: []string{}}, nil
}
