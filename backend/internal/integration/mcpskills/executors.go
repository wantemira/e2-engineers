package mcpskills

import (
	"context"
	"strings"
	"unicode/utf8"

	"github.com/wantemira/e2-engineers/backend/internal/domain"
)

type dependencies struct {
	git      GitReader
	teams    TeamLoader
	reviewer Reviewer
}

type executor struct {
	contract string
	validate func(definition) bool
	run      func(context.Context, string, domain.SkillCall, definition, dependencies) (any, error)
}

// Adding a mock implementation requires only an adapter and one registration.
// Skill IDs and versions stay in configuration, not in this registry.
var executors = map[string]executor{
	"review-analyzer": {contract: domain.ReviewSkillContract, run: reviewAdapter},
	"fixture": {contract: domain.MessageSkillContract, run: fixtureAdapter,
		validate: func(skill definition) bool {
			return strings.TrimSpace(skill.Output.Text) != "" && len(skill.Output.Text) <= 32000
		}},
}

func reviewAdapter(ctx context.Context, actor string, call domain.SkillCall, _ definition, dependencies dependencies) (any, error) {
	var input domain.ReviewSkillInput
	if domain.DecodeSkillPayload(call.Input, &input) != nil || input.PRNumber < 1 || input.HeadSHA == "" {
		return nil, domain.Problem("INVALID_INPUT", "Укажите PR и версию для внутреннего ревью.")
	}
	pr, err := dependencies.git.GetPullRequest(ctx, actor, input.PRNumber)
	if err != nil {
		return nil, err
	}
	team, err := dependencies.teams.Load()
	if err != nil {
		return nil, err
	}
	if pr.OwnerID != actor || pr.Repository != team.Repository || pr.Number != team.PRNumber {
		return nil, domain.Problem("NOT_FOUND", "PR недоступен внутреннему скиллу.")
	}
	if pr.HeadSHA != input.HeadSHA {
		return nil, domain.Problem("STALE_HEAD", "PR обновился до вызова внутреннего ревью.")
	}
	checks, err := dependencies.git.GetChecks(ctx, actor, pr.Number, pr.HeadSHA)
	if err != nil {
		if domain.PublicError(err).Code == "STALE_HEAD" {
			return nil, err
		}
		checks = domain.Checks{Status: "UNKNOWN", Label: "Нет данных о проверках"}
	}
	if domain.ValidateChecks(checks, pr.Number, pr.HeadSHA) != nil {
		return nil, domain.Problem("SKILL_INVALID_RESULT", "CI не соответствует проверяемой версии PR.")
	}
	analysis, err := dependencies.reviewer.Review(ctx, pr, checks, team)
	if err != nil {
		return nil, err
	}
	return domain.ReviewSkillOutput{PRNumber: pr.Number, HeadSHA: pr.HeadSHA, Checks: checks, Analysis: analysis}, nil
}

func fixtureAdapter(_ context.Context, _ string, call domain.SkillCall, skill definition, _ dependencies) (any, error) {
	var input domain.MessageSkillInput
	if domain.DecodeSkillPayload(call.Input, &input) != nil || strings.TrimSpace(input.Text) == "" || utf8.RuneCountInString(input.Text) > 8000 {
		return nil, domain.Problem("INVALID_INPUT", "Некорректный вход текстового скилла.")
	}
	return skill.Output, nil
}
