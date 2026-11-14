package service

import (
	"context"
	"log/slog"

	"github.com/wantemira/e2-engineers/backend/internal/domain"
)

type Agent interface {
	Plan(context.Context, domain.ChatInput) (domain.AgentDecision, error)
	Info() domain.AssistantInfo
}

func (service *Service) agentMessage(ctx context.Context, agent Agent, actor string, team domain.Team, op domain.Job, state domain.Workspace, current *domain.ReviewRun) (domain.Result, error) {
	history := state.Messages
	if len(history) > 20 {
		history = history[len(history)-20:]
	}
	decision, err := agent.Plan(ctx, domain.ChatInput{Text: op.Command.Text, History: history, CurrentReview: current, Team: team})
	if err != nil {
		return domain.Result{}, err
	}
	var result domain.Result
	switch decision.Tool {
	case "answer_question":
		message := decision.Message
		message.ID, message.Role = "assistant_"+op.Operation.ID, "assistant"
		result.Messages = []domain.Message{{ID: "user_" + op.Operation.ID, Role: "user", Text: op.Command.Text}, message}
	case "run_pr_self_review":
		op.Command.RunChecks = decision.RunChecks
		result, err = service.review(ctx, actor, decision.PRRef, team, op)
	case "get_training_status":
		if team.Training == nil {
			return domain.Result{}, domain.Problem("ENVIRONMENT_UNAVAILABLE", "Учебное окружение не настроено.")
		}
		result, err = service.training(ctx, actor, decision.PRRef, team, op)
	case "execute_message_skill":
		found := false
		for _, skill := range team.Skills {
			if skill.ID == decision.SkillID && skill.Contract == domain.MessageSkillContract {
				result, err = service.skillMessage(ctx, actor, skill, op)
				found = true
				break
			}
		}
		if !found {
			return domain.Result{}, domain.Problem("SKILL_UNAVAILABLE", "Скилл не подключён к команде.")
		}
	default:
		return domain.Result{}, domain.Problem("INVALID_INPUT", "Модель выбрала неподдерживаемый сценарий.")
	}
	if err != nil {
		return domain.Result{}, err
	}
	trace := &domain.AgentTrace{Model: agent.Info().Model, Tool: decision.Tool}
	for index := range result.Messages {
		if result.Messages[index].Role == "assistant" {
			result.Messages[index].Agent = trace
		}
	}
	slog.Info("agent scenario completed", "tool", trace.Tool, "model", trace.Model)
	return result, nil
}
