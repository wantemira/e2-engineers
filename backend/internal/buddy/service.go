package buddy

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
)

type Service struct {
	store     *Store
	git       GitTools
	assistant Assistant
	root      string
	wake      chan struct{}
}

func NewService(store *Store, git GitTools, assistant Assistant, projectRoot string) *Service {
	return &Service{store: store, git: git, assistant: assistant, root: projectRoot, wake: make(chan struct{}, 1)}
}

func tokenHash(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

func (service *Service) session(token string) (session, string, error) {
	data, err := service.store.snapshot()
	if err != nil {
		return session{}, "", err
	}
	if token != "" {
		if existing, ok := data.Sessions[tokenHash(token)]; ok {
			return existing, "", nil
		}
	}
	token = newID("session")
	created := session{ID: newID("actor-session"), ActorID: "intern-1", CreatedAt: time.Now().UTC()}
	err = service.store.update(func(state *diskState) error {
		state.Sessions[tokenHash(token)] = created
		return nil
	})
	return created, token, err
}

func (service *Service) Submit(owner session, key string, command Command) (string, error) {
	var id string
	index := owner.ID + "\x00" + command.Kind + "\x00" + key
	err := service.store.update(func(state *diskState) error {
		if previous, ok := state.Idempotency[index]; ok {
			if state.Operations[previous].Command != command {
				return Problem("IDEMPOTENCY_CONFLICT", "Ключ уже использован для другого запроса.")
			}
			id = previous
			return nil
		}
		id = newID("op")
		state.Operations[id] = savedOperation{
			Operation: Operation{ID: id, State: "QUEUED", CreatedAt: time.Now().UTC()},
			Owner:     owner.ID, Key: key, Command: command,
		}
		state.Idempotency[index] = id
		if command.Kind == "message" {
			state.Messages[owner.ID] = append(state.Messages[owner.ID], Message{ID: "user_" + id, Role: "user", Text: command.Text})
		}
		return nil
	})
	if err == nil {
		service.signal()
	}
	return id, err
}

func (service *Service) signal() {
	select {
	case service.wake <- struct{}{}:
	default:
	}
}

func (service *Service) Operation(owner, id string) (Operation, error) {
	state, err := service.store.snapshot()
	if err != nil {
		return Operation{}, err
	}
	op, ok := state.Operations[id]
	if !ok || op.Owner != owner {
		return Operation{}, Problem("NOT_FOUND", "Операция недоступна.")
	}
	return op.Operation, nil
}

func (service *Service) Retry(owner, id, key string) error {
	err := service.store.update(func(state *diskState) error {
		op, ok := state.Operations[id]
		if !ok || op.Owner != owner {
			return Problem("NOT_FOUND", "Операция недоступна.")
		}
		if key != op.Key {
			return Problem("IDEMPOTENCY_CONFLICT", "Для повтора нужен исходный ключ операции.")
		}
		if op.Operation.State == "FAILED" {
			return Problem("INVALID_STATE", "Операция завершилась ошибкой. Создайте новый запрос.")
		}
		if op.Operation.State == "RESULT_UNKNOWN" {
			op.Operation.State = "QUEUED"
			op.Operation.Error = nil
			state.Operations[id] = op
		}
		return nil
	})
	if err == nil {
		service.signal()
	}
	return err
}

func (service *Service) Run(ctx context.Context) error {
	if err := service.store.update(func(state *diskState) error {
		for id, op := range state.Operations {
			if op.Operation.State == "RUNNING" {
				op.Operation.State = "QUEUED"
				state.Operations[id] = op
			}
		}
		return nil
	}); err != nil {
		return fmt.Errorf("recover operations: %w", err)
	}
	for {
		worked, err := service.runNext(ctx)
		if err != nil {
			return err
		}
		if worked {
			continue
		}
		select {
		case <-ctx.Done():
			return nil
		case <-service.wake:
		}
	}
}

func (service *Service) runNext(ctx context.Context) (bool, error) {
	if ctx.Err() != nil {
		return false, nil
	}
	state, err := service.store.snapshot()
	if err != nil {
		return false, err
	}
	var selected *savedOperation
	for _, op := range state.Operations {
		if op.Operation.State == "QUEUED" && (selected == nil || op.Operation.CreatedAt.Before(selected.Operation.CreatedAt)) {
			copy := op
			selected = &copy
		}
	}
	if selected == nil {
		return false, nil
	}
	op := *selected
	if err := service.store.update(func(state *diskState) error {
		current := state.Operations[op.Operation.ID]
		current.Operation.State = "RUNNING"
		state.Operations[op.Operation.ID] = current
		return nil
	}); err != nil {
		return false, err
	}
	operationContext, cancel := context.WithTimeout(ctx, 30*time.Second)
	result, runErr := service.process(operationContext, op, state)
	cancel()
	if ctx.Err() != nil {
		return false, nil
	}
	err = service.store.update(func(state *diskState) error {
		current := state.Operations[op.Operation.ID]
		if runErr != nil {
			problem := applicationError(runErr)
			current.Operation.State = "FAILED"
			if op.Command.Kind == "request" && problem.Retryable {
				current.Operation.State = "RESULT_UNKNOWN"
			}
			current.Operation.Error = problem
		} else {
			current.Operation.State = "SUCCEEDED"
			current.Operation.Result = &result
			for _, message := range result.Messages {
				if !slices.ContainsFunc(state.Messages[op.Owner], func(existing Message) bool { return existing.ID == message.ID }) {
					state.Messages[op.Owner] = append(state.Messages[op.Owner], message)
				}
			}
			if result.Review != nil {
				state.Reviews[op.Owner] = append([]ReviewRun{*result.Review}, state.Reviews[op.Owner]...)
			}
		}
		state.Operations[op.Operation.ID] = current
		return nil
	})
	return true, err
}

func applicationError(err error) *AppError {
	var problem *AppError
	if errors.As(err, &problem) {
		return problem
	}
	return &AppError{Code: "DEPENDENCY_UNAVAILABLE", Message: "Сервис временно недоступен. Повторите запрос.", Retryable: true}
}

func (service *Service) process(ctx context.Context, op savedOperation, state diskState) (Result, error) {
	team, err := LoadTeam(service.root)
	if err != nil {
		return Result{}, fmt.Errorf("load team context: %w", err)
	}
	var current *ReviewRun
	if reviews := state.Reviews[op.Owner]; len(reviews) > 0 {
		current = &reviews[0]
	}
	var actor string
	for _, owner := range state.Sessions {
		if owner.ID == op.Owner {
			actor = owner.ActorID
			break
		}
	}
	if actor == "" {
		return Result{}, Problem("NOT_FOUND", "Сессия недоступна.")
	}
	switch op.Command.Kind {
	case "message":
		text := strings.ToLower(op.Command.Text)
		ref := referenceInMessage(op.Command.Text)
		if ref != "" && (standalonePR.MatchString(op.Command.Text) || containsAny(text, "проверь", "проверить", "самопроверк", "проанализируй", "check ", "review ")) {
			return service.review(ctx, actor, ref, team, op)
		}
		if current != nil && (strings.Contains(text, "перепроверь") || strings.Contains(text, "проверь ещё") || strings.Contains(text, "проверь еще") || strings.Contains(text, "повторная проверка")) {
			return service.review(ctx, actor, current.PR.URL, team, op)
		}
		history := state.Messages[op.Owner]
		if len(history) > 20 {
			history = history[len(history)-20:]
		}
		message, err := service.assistant.Answer(ctx, ChatInput{Text: op.Command.Text, History: history, CurrentReview: current, Team: team})
		if err != nil {
			return Result{}, err
		}
		message.ID = "assistant_" + op.Operation.ID
		message.Role = "assistant"
		return Result{Messages: []Message{{ID: "user_" + op.Operation.ID, Role: "user", Text: op.Command.Text}, message}}, nil
	case "review":
		return service.review(ctx, actor, op.Command.PRRef, team, op)
	case "request":
		return service.requestReview(ctx, actor, team, op, state)
	default:
		return Result{}, Problem("INVALID_INPUT", "Неизвестное действие.")
	}
}

func (service *Service) review(ctx context.Context, actor, ref string, team Team, op savedOperation) (Result, error) {
	number, err := resolvePR(ref, team)
	if err != nil {
		return Result{}, err
	}
	pr, err := service.git.GetPullRequest(ctx, actor, number)
	if err != nil {
		return Result{}, err
	}
	if pr.Repository != team.Repository || pr.Number != team.PRNumber || pr.OwnerID != actor {
		return Result{}, Problem("NOT_FOUND", "PR недоступен.")
	}
	if len(pr.Diff) > 200*1024 {
		return Result{}, Problem("DIFF_TOO_LARGE", "Изменения превышают размер одной проверки.")
	}
	checks, checksErr := service.git.GetChecks(ctx, actor, number, pr.HeadSHA)
	if checksErr != nil {
		if applicationError(checksErr).Code == "STALE_HEAD" {
			return Result{}, checksErr
		}
		checks = Checks{Status: "UNKNOWN", Label: "Нет данных о проверках"}
	}
	findings, err := service.assistant.Review(ctx, pr, checks, team)
	if err != nil {
		return Result{}, err
	}
	if findings == nil {
		findings = []Finding{}
	}
	latest, err := service.git.GetPullRequest(ctx, actor, number)
	if err != nil {
		return Result{}, err
	}
	run := ReviewRun{ID: "review_" + op.Operation.ID, HeadSHA: pr.HeadSHA, Mentor: team.Mentor, PR: pr.PRInfo,
		Findings: findings, Checks: checks, CreatedAt: time.Now().UTC(), IsStale: latest.HeadSHA != pr.HeadSHA}
	summary := "В проверенном diff замечаний не нашёл. Можно отправить PR наставнику."
	if len(findings) > 0 {
		summary = fmt.Sprintf("Проверил diff PR #%d по правилам команды. Замечаний: %d. Разберём их по одному — можно спросить про конкретный пункт.", number, len(findings))
	}
	if run.IsStale {
		summary = "PR обновился во время проверки. Отчёт сохранён для предыдущей версии. Запустите проверку повторно."
	}
	messages := []Message{}
	if op.Command.Kind == "message" {
		messages = append(messages, Message{ID: "user_" + op.Operation.ID, Role: "user", Text: op.Command.Text})
	}
	messages = append(messages, Message{ID: "assistant_" + op.Operation.ID, Role: "assistant", Text: summary})
	return Result{Messages: messages, Review: &run}, nil
}

func (service *Service) requestReview(ctx context.Context, actor string, team Team, op savedOperation, state diskState) (Result, error) {
	index := slices.IndexFunc(state.Reviews[op.Owner], func(run ReviewRun) bool { return run.ID == op.Command.ReviewRunID })
	if index < 0 {
		return Result{}, Problem("NOT_FOUND", "Проверка недоступна.")
	}
	run := state.Reviews[op.Owner][index]
	if run.IsStale {
		return Result{}, Problem("STALE_HEAD", "Версия PR устарела.")
	}
	if team.Mentor.ID != op.Command.ExpectedMentorID || run.Mentor.ID != team.Mentor.ID {
		return Result{}, Problem("INVALID_STATE", "Наставник изменился. Обновите проверку и подтвердите отправку.")
	}
	receipt, err := service.git.RequestReview(ctx, actor, run.PR.Number, run.HeadSHA, team.Mentor, op.Operation.ID)
	if err != nil {
		return Result{}, err
	}
	return Result{Messages: []Message{{ID: "receipt_" + receipt.ID, Role: "assistant", Text: "", Receipt: &receipt}}, ReviewRequest: &receipt}, nil
}

var prNumberPattern = regexp.MustCompile(`(?i)(?:\bpr|пр)\s*#?\s*(\d+)`)
var urlPattern = regexp.MustCompile(`https?://[^\s<>]+`)
var standalonePR = regexp.MustCompile(`(?i)^\s*(?:(?:pr|пр)\s*)?#?\s*\d+\s*$|^\s*https?://\S+\s*$`)

func referenceInMessage(text string) string {
	for _, candidate := range urlPattern.FindAllString(text, -1) {
		if strings.Contains(candidate, "/pull/") {
			return strings.TrimRight(candidate, ".,;!?)")
		}
	}
	if match := prNumberPattern.FindStringSubmatch(text); len(match) > 1 {
		return match[1]
	}
	trimmed := strings.TrimSpace(strings.TrimPrefix(text, "#"))
	if _, err := strconv.Atoi(trimmed); err == nil {
		return trimmed
	}
	return ""
}

func resolvePR(ref string, team Team) (int, error) {
	value := strings.TrimSpace(ref)
	if strings.Contains(value, "://") {
		parsed, err := url.Parse(value)
		base, baseErr := url.Parse(team.RepositoryURL)
		if err != nil || baseErr != nil || parsed.Scheme != "https" || parsed.Host != base.Host || parsed.User != nil || parsed.RawQuery != "" {
			return 0, Problem("INVALID_INPUT", "Проверьте ссылку на PR команды.")
		}
		prefix := strings.TrimSuffix(base.Path, "/") + "/pull/"
		if !strings.HasPrefix(parsed.Path, prefix) {
			return 0, Problem("NOT_FOUND", "PR недоступен.")
		}
		value = strings.TrimPrefix(parsed.Path, prefix)
	} else {
		value = strings.TrimSpace(strings.TrimPrefix(strings.ToUpper(value), "PR"))
		value = strings.TrimSpace(strings.TrimPrefix(value, "#"))
	}
	number, err := strconv.Atoi(value)
	if err != nil || number < 1 {
		return 0, Problem("INVALID_INPUT", "Укажите номер или ссылку на PR.")
	}
	if number != team.PRNumber {
		return 0, Problem("NOT_FOUND", "PR недоступен.")
	}
	return number, nil
}
