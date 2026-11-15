package service

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/wantemira/e2-engineers/backend/internal/domain"
	"github.com/wantemira/e2-engineers/backend/internal/teamhelp"
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
	operationContext, cancel := context.WithTimeout(ctx, 110*time.Second)
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

func (service *Service) process(ctx context.Context, op domain.Job, state domain.Workspace) (result domain.Result, processErr error) {
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
	var evidence *domain.Investigation
	defer func() {
		if processErr != nil || evidence == nil {
			return
		}
		for i := range result.Messages {
			m := &result.Messages[i]
			if m.Role != "assistant" {
				continue
			}
			if m.Help == nil {
				m.Help = teamhelp.Trace(team, "search", "")
			}
			if m.Help.Investigation == nil {
				m.Help.Investigation = evidence
			}
		}
	}()
	switch op.Command.Kind {
	case "message":
		history := state.Messages
		if len(history) > 20 {
			history = history[len(history)-20:]
		}
		input := domain.ChatInput{Text: op.Command.Text, History: history, CurrentReview: current, Team: team}
		// In the configured application every message searches before routing or LLM.
		// The optional port preserves legacy consumers without the help integration.
		if teamhelp.Enabled(team) && service.knowledge != nil {
			purpose := "information"
			if teamhelp.IsAccessQuestion(input.Text) {
				purpose = "access"
			}
			query := input.Text
			lower := strings.ToLower(input.Text)
			if containsAny(lower, "не помогло", "всё равно", "все равно", "ошибка осталась", "уже пробовал", "уже проверил") && !containsAny(lower, "не могу", "не получается") {
				for i := len(history) - 1; i >= 0; i-- {
					m := history[i]
					if m.Role == "assistant" {
						if m.Help != nil && m.Help.Investigation != nil {
							query = m.Help.Investigation.Search.Query + "\n" + input.Text
							purpose = m.Help.Investigation.Purpose
						}
						break
					}
				}
			}
			evidence, err = service.searchEvidence(ctx, actor, input, query, purpose)
			if err != nil {
				return domain.Result{}, err
			}
			input.Knowledge = evidence
			var message domain.Message
			handled := false
			if teamhelp.WantsEscalation(input.Text) || teamhelp.AcceptsEscalation(input) {
				message = teamhelp.Escalate(input)
				handled = true
			} else if len(evidence.Search.Hits) == 0 {
				lower := strings.ToLower(strings.TrimSpace(input.Text))
				knownAction := referenceInMessage(input.Text) != "" || wantsCheckRun(lower) || (team.Training != nil && trainingQuestion(lower))
				reviewFollowup := current != nil && (containsAny(lower, "замечан", "пункт", "перепроверь", "повторная проверка") || lower == "почему?" || lower == "объясни" || lower == "подробнее")
				if !teamhelp.IsSocialMessage(input.Text) && !knownAction && !reviewFollowup {
					message = teamhelp.MissingKnowledge(input)
					handled = true
				}
			}
			if handled {
				message.ID = "assistant_" + op.Operation.ID
				return domain.Result{Messages: []domain.Message{{ID: "user_" + op.Operation.ID, Role: "user", Text: op.Command.Text}, message}}, nil
			}
		}
		if agent, ok := service.assistant.(Agent); ok {
			return service.agentMessage(ctx, agent, actor, team, op, state, current, evidence)
		}
		text := strings.ToLower(op.Command.Text)
		ref := referenceInMessage(op.Command.Text)
		if wantsCheckRun(text) {
			if ref == "" {
				if current != nil {
					ref = current.PR.URL
				} else {
					ref = strconv.Itoa(team.PRNumber)
				}
			}
			op.Command.RunChecks = true
			return service.review(ctx, actor, ref, team, op)
		}
		if ref != "" && (standalonePR.MatchString(op.Command.Text) || containsAny(text, "проверь", "проверить", "самопроверк", "проанализируй", "check ", "review ")) {
			return service.review(ctx, actor, ref, team, op)
		}
		if team.Training != nil && trainingQuestion(text) {
			return service.training(ctx, actor, ref, team, op)
		}
		if current != nil && (strings.Contains(text, "перепроверь") || strings.Contains(text, "проверь ещё") || strings.Contains(text, "проверь еще") || strings.Contains(text, "повторная проверка")) {
			return service.review(ctx, actor, current.PR.URL, team, op)
		}
		if skill := matchingMessageSkill(team, text); skill != nil {
			return service.skillMessage(ctx, actor, *skill, op)
		}
		message, err := service.assistant.Answer(ctx, input)
		if err == nil && message.Help != nil && message.Help.Kind == "search" {
			message, err = service.investigate(ctx, actor, input, op.Command.Text)
		}
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

func wantsCheckRun(text string) bool {
	return !containsAny(text, "как ", "где ") && containsAny(text, "запусти тест", "запустить тест", "прогони тест", "перезапусти тест", "запусти ci", "перезапусти ci")
}
