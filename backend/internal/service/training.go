package service

import (
	"context"
	"fmt"
	"strconv"

	"github.com/wantemira/e2-engineers/backend/internal/domain"
)

func trainingQuestion(text string) bool {
	if containsAny(text, "замечан", "пункт") && containsAny(text, "объясни", "почему", "исправ") {
		return false
	}
	return containsAny(text, "окружен", "учебн", "домашн", "как запустить сервис",
		"запусти тест", "запустить тест", "где запускать тест", "результат тест", "статус ci", "проверь ci")
}

func (service *Service) training(ctx context.Context, actor, ref string, team domain.Team, op domain.Job) (domain.Result, error) {
	if ref == "" {
		ref = strconv.Itoa(team.PRNumber)
	}
	number, err := resolvePR(ref, team)
	if err != nil {
		return domain.Result{}, err
	}
	pr, err := service.git.GetPullRequest(ctx, actor, number)
	if err != nil {
		return domain.Result{}, err
	}
	if pr.Repository != team.Repository || pr.Number != team.PRNumber || pr.OwnerID != actor {
		return domain.Result{}, domain.Problem("NOT_FOUND", "PR недоступен.")
	}
	environment, err := service.git.GetTrainingEnvironment(ctx, actor, number, pr.HeadSHA)
	if err != nil {
		return domain.Result{}, err
	}
	training := team.Training
	if environment.Service != training.Service || environment.Name != training.Environment ||
		environment.Source != "MOCK" || environment.PRNumber != number || environment.HeadSHA != pr.HeadSHA {
		return domain.Result{}, fmt.Errorf("training environment does not match the requested PR")
	}
	checks, err := service.git.GetChecks(ctx, actor, number, pr.HeadSHA)
	if err != nil {
		if domain.PublicError(err).Code == "STALE_HEAD" {
			return domain.Result{}, err
		}
		checks = domain.Checks{Status: "UNKNOWN", Label: "Результат CI недоступен"}
	}
	latest, err := service.git.GetPullRequest(ctx, actor, number)
	if err != nil {
		return domain.Result{}, err
	}
	if latest.HeadSHA != pr.HeadSHA {
		return domain.Result{}, domain.Problem("STALE_HEAD", "PR обновился. Запросите состояние окружения повторно.")
	}
	next := "Запустите учебный сервис и проверьте /healthz по инструкции."
	if environment.Status == "STARTING" {
		next = "Дождитесь готовности окружения и запросите его состояние повторно."
	}
	if environment.Status == "FAILED" {
		next = "Посмотрите логи учебного сервиса по инструкции и обратитесь к наставнику с текстом ошибки."
	}
	if environment.Status == "READY" {
		switch checks.Status {
		case "PASSED":
			next = "Запустите самопроверку PR и разберите замечания перед отправкой наставнику."
		case "FAILED":
			next = "Разберите ошибку CI, исправьте код и повторите локальную проверку."
		case "RUNNING":
			next = "Дождитесь завершения CI и запросите статус повторно."
		default:
			next = "Запустите локальные тесты по инструкции и проверьте результат."
		}
	}
	message := domain.Message{
		ID: "assistant_" + op.Operation.ID, Role: "assistant",
		Text: fmt.Sprintf("Учебный сервис — %s. Окружение — %s, статус %s.\n"+
			"Источник состояния окружения и CI — mock-инструменты.\n"+
			"PR #%d, версия %s. CI — %s: %s.\n\n"+
			"Задание: %s\n\nЗапуск сервиса: %s\nЛокальные тесты: %s\n"+
			"Реальный результат локального запуска смотрите в терминале.\n\nСледующий шаг — %s",
			training.Service, training.Environment, environment.Status, number, pr.HeadSHA,
			checks.Status, checks.Label, pr.Task, training.StartCommand, training.TestCommand, next),
	}
	for _, document := range team.Documents {
		if document.ID == training.GuideID {
			message.Links = []domain.SourceLink{{Label: document.Title, URL: "/api/v1/context/documents/" + document.ID}}
		}
	}
	return domain.Result{Messages: []domain.Message{
		{ID: "user_" + op.Operation.ID, Role: "user", Text: op.Command.Text}, message,
	}}, nil
}
