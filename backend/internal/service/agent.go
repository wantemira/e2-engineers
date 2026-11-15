package service

import (
	"context"
	"log/slog"

	"github.com/wantemira/e2-engineers/backend/internal/domain"
	"github.com/wantemira/e2-engineers/backend/internal/teamhelp"
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
	case "search_team_knowledge":
		input := domain.ChatInput{Text: op.Command.Text, History: history, CurrentReview: current, Team: team}
		var message domain.Message
		message, err = service.investigate(ctx, actor, input, decision.Query)
		message.ID = "assistant_" + op.Operation.ID
		result.Messages = []domain.Message{{ID: "user_" + op.Operation.ID, Role: "user", Text: op.Command.Text}, message}
	case "find_team_help":
		input := domain.ChatInput{Text: op.Command.Text, History: history, CurrentReview: current, Team: team}
		if !teamhelp.Enabled(team) {
			return domain.Result{}, domain.Problem("HELP_NOT_FOUND", "Помощь команды не настроена.")
		}
		var message domain.Message
		switch decision.HelpKind {
		case "access":
			message, err = teamhelp.Access(input, decision.TargetID)
		case "handoff":
			message, err = teamhelp.Handoff(input, decision.TargetID)
		case "escalation":
			if teamhelp.WantsEscalation(input.Text) {
				message = teamhelp.Escalate(input)
			} else {
				decision.Tool = "search_team_knowledge"
				message, err = service.investigate(ctx, actor, input, input.Text)
			}
		default:
			return domain.Result{}, domain.Problem("HELP_NOT_FOUND", "Неизвестный сценарий помощи.")
		}
		message.ID = "assistant_" + op.Operation.ID
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
