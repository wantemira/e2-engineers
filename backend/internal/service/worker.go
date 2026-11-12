package service

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/wantemira/e2-engineers/backend/internal/domain"
)

func (service *Service) signal() {
	select {
	case service.wake <- struct{}{}:
	default:
	}
}

func (service *Service) Run(ctx context.Context) error {
	if err := service.repository.RecoverOperations(); err != nil {
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
	job, found, err := service.repository.ClaimNext()
	if err != nil || !found {
		return false, err
	}
	workspace, err := service.repository.Workspace(job.Owner)
	if err != nil {
		return false, err
	}
	operationContext, cancel := context.WithTimeout(ctx, 30*time.Second)
	result, runErr := service.process(operationContext, job, workspace)
	cancel()
	if ctx.Err() != nil {
		return false, nil
	}

	operation := job.Operation
	if runErr != nil {
		operation.Error = domain.PublicError(runErr)
		operation.State = "FAILED"
		if job.Command.Kind == "request" && operation.Error.Retryable {
			operation.State = "RESULT_UNKNOWN"
		}
	} else {
		operation.State = "SUCCEEDED"
		operation.Result = &result
	}
	return true, service.repository.Complete(operation)
}

func (service *Service) process(ctx context.Context, op domain.Job, state domain.Workspace) (domain.Result, error) {
	team, err := service.teams.Load()
	if err != nil {
		return domain.Result{}, fmt.Errorf("load team context: %w", err)
	}
	var current *domain.ReviewRun
	if reviews := state.Reviews; len(reviews) > 0 {
		current = &reviews[0]
	}
	actor := op.ActorID
	if actor == "" {
		return domain.Result{}, domain.Problem("NOT_FOUND", "Сессия недоступна.")
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
		history := state.Messages
		if len(history) > 20 {
			history = history[len(history)-20:]
		}
		message, err := service.assistant.Answer(ctx, domain.ChatInput{Text: op.Command.Text, History: history, CurrentReview: current, Team: team})
		if err != nil {
			return domain.Result{}, err
		}
		message.ID = "assistant_" + op.Operation.ID
		message.Role = "assistant"
		return domain.Result{Messages: []domain.Message{{ID: "user_" + op.Operation.ID, Role: "user", Text: op.Command.Text}, message}}, nil
	case "review":
		return service.review(ctx, actor, op.Command.PRRef, team, op)
	case "request":
		return service.requestReview(ctx, actor, team, op, state)
	default:
		return domain.Result{}, domain.Problem("INVALID_INPUT", "Неизвестное действие.")
	}
}

func containsAny(text string, fragments ...string) bool {
	for _, fragment := range fragments {
		if strings.Contains(text, fragment) {
			return true
		}
	}
	return false
}
