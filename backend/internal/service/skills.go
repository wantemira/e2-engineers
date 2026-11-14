package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"slices"
	"strings"

	"github.com/wantemira/e2-engineers/backend/internal/diff"
	"github.com/wantemira/e2-engineers/backend/internal/domain"
)

func invalidSkillReply() error {
	return &domain.AppError{Code: "SKILL_INVALID_RESULT", Message: "Внутренний скилл вернул некорректный результат. Повторите проверку.", Retryable: true}
}

func (service *Service) executeSkill(ctx context.Context, actor string, skill domain.SkillDefinition, input any, output any) (*domain.SkillExecution, error) {
	if service.skills == nil {
		return nil, domain.Problem("SKILL_UNAVAILABLE", "Сервис внутренних скиллов не настроен.")
	}
	payload, err := domain.SkillPayload(input)
	if err != nil {
		return nil, err
	}
	reply, err := service.skills.Execute(ctx, actor, domain.SkillCall{Skill: skill.Target, Contract: skill.Contract, Instructions: skill.Instructions, Input: payload})
	if err != nil {
		return nil, err
	}
	digest := sha256.Sum256([]byte(skill.Instructions))
	hash := hex.EncodeToString(digest[:])
	data, err := json.Marshal(reply.Output)
	if err != nil || len(data) > 256*1024 || reply.Skill != skill.Target || reply.Contract != skill.Contract ||
		(reply.Source != "MOCK" && reply.Source != "INTERNAL" && reply.Source != "LLM") || (reply.Source == "LLM" && strings.TrimSpace(reply.Model) == "") || strings.TrimSpace(reply.RunID) == "" || len(reply.RunID) > 128 || reply.InstructionsSHA256 != hash ||
		domain.DecodeSkillPayload(reply.Output, output) != nil {
		return nil, invalidSkillReply()
	}
	return &domain.SkillExecution{Model: reply.Model, Skill: skill.SkillRef, Target: skill.Target, Contract: skill.Contract, Source: reply.Source, RunID: reply.RunID, InstructionsSHA256: hash}, nil
}

func skillForCapability(team domain.Team, capability string) (domain.SkillDefinition, error) {
	index := slices.IndexFunc(team.Skills, func(skill domain.SkillDefinition) bool { return skill.ID == team.Capabilities[capability] })
	if index < 0 {
		return domain.SkillDefinition{}, domain.Problem("SKILL_UNAVAILABLE", "Скилл для этого сценария не настроен.")
	}
	return team.Skills[index], nil
}

func matchingMessageSkill(team domain.Team, text string) *domain.SkillDefinition {
	for _, skill := range team.Skills {
		if skill.Contract == domain.MessageSkillContract && containsAny(text, skill.Triggers...) {
			return &skill
		}
	}
	return nil
}

func skillSourceText(execution *domain.SkillExecution) string {
	if execution.Source == "LLM" {
		return "\n\nИсточник анализа — Live LLM (" + execution.Model + "). Данные PR и CI — mock Git через MCP."
	}
	if execution.Source == "MOCK" {
		return "\n\nИсточник — mock внутреннего скилла."
	}
	return "\n\nИсточник — внутренний скилл."
}

func (service *Service) skillMessage(ctx context.Context, actor string, skill domain.SkillDefinition, op domain.Job) (domain.Result, error) {
	var output domain.MessageSkillOutput
	execution, err := service.executeSkill(ctx, actor, skill, domain.MessageSkillInput{Text: op.Command.Text}, &output)
	if err != nil {
		return domain.Result{}, err
	}
	if strings.TrimSpace(output.Text) == "" || len(output.Text) > 32000 {
		return domain.Result{}, invalidSkillReply()
	}
	return domain.Result{Messages: []domain.Message{
		{ID: "user_" + op.Operation.ID, Role: "user", Text: op.Command.Text},
		{ID: "assistant_" + op.Operation.ID, Role: "assistant", Text: output.Text + skillSourceText(execution), Skill: execution},
	}}, nil
}

func validateSkillReview(output domain.ReviewSkillOutput, pr domain.PullRequest, team domain.Team) error {
	if domain.ValidateChecks(output.Checks, pr.Number, pr.HeadSHA) != nil || len(output.Analysis.Assessment.CI) > 16000 || len(output.Analysis.Assessment.SuggestedTests) > 20 {
		return invalidSkillReply()
	}
	for _, suggestion := range output.Analysis.Assessment.SuggestedTests {
		if strings.TrimSpace(suggestion) == "" || len(suggestion) > 4000 {
			return invalidSkillReply()
		}
	}
	if output.PRNumber != pr.Number || output.HeadSHA != pr.HeadSHA ||
		!slices.Contains([]string{"UNKNOWN", "RUNNING", "FAILED", "PASSED"}, output.Checks.Status) || output.Checks.Label == "" ||
		strings.TrimSpace(output.Analysis.Assessment.Scope) == "" || strings.TrimSpace(output.Analysis.Assessment.Task) == "" || len(output.Analysis.Findings) > 200 {
		return invalidSkillReply()
	}
	lines, err := diff.Parse(pr.Diff)
	if err != nil {
		return err
	}
	type position struct {
		path string
		line int
	}
	positions := make(map[position]bool, len(lines))
	for _, line := range lines {
		positions[position{line.Path, line.Number}] = true
	}
	ids := make(map[string]bool)
	for _, finding := range output.Analysis.Findings {
		if finding.ID == "" || ids[finding.ID] || (finding.Kind != "problem" && finding.Kind != "assumption") || strings.TrimSpace(finding.Title) == "" || strings.TrimSpace(finding.Description) == "" {
			return invalidSkillReply()
		}
		ids[finding.ID] = true
		if finding.Location != nil && !positions[position{finding.Location.Path, finding.Location.Line}] {
			return invalidSkillReply()
		}
		if finding.Rule != nil {
			if rule, ok := team.Rules[finding.Rule.ID]; !ok || rule != *finding.Rule {
				return invalidSkillReply()
			}
		}
	}
	return nil
}
