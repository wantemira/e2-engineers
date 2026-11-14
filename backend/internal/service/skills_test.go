package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"testing"

	"github.com/wantemira/e2-engineers/backend/internal/domain"
)

type skillRunnerFunc func(context.Context, string, domain.SkillCall) (domain.SkillReply, error)

func (run skillRunnerFunc) Execute(ctx context.Context, actor string, call domain.SkillCall) (domain.SkillReply, error) {
	return run(ctx, actor, call)
}

func TestSkillReplyMustMatchRequestedIdentityContractAndInstructions(t *testing.T) {
	skill := domain.SkillDefinition{SkillRef: domain.SkillRef{ID: "guide", Version: "1.0.0"}, Target: domain.SkillRef{ID: "internal-guide", Version: "2.0.0"}, Contract: domain.MessageSkillContract, Instructions: "Trusted instructions"}
	digest := sha256.Sum256([]byte(skill.Instructions))
	valid := func() domain.SkillReply {
		return domain.SkillReply{Skill: skill.Target, Contract: skill.Contract, Source: "MOCK", RunID: "run-1", InstructionsSHA256: hex.EncodeToString(digest[:]), Output: map[string]any{"text": "Answer"}}
	}
	for _, tc := range []struct {
		name   string
		change func(*domain.SkillReply)
	}{
		{"valid", func(*domain.SkillReply) {}},
		{"identity", func(r *domain.SkillReply) { r.Skill.ID = "another" }},
		{"version", func(r *domain.SkillReply) { r.Skill.Version = "3.0.0" }},
		{"contract", func(r *domain.SkillReply) { r.Contract = domain.ReviewSkillContract }},
		{"instructions", func(r *domain.SkillReply) { r.InstructionsSHA256 = "old" }},
		{"source", func(r *domain.SkillReply) { r.Source = "UNKNOWN" }},
		{"run id", func(r *domain.SkillReply) { r.RunID = "" }},
		{"invalid output", func(r *domain.SkillReply) { r.Output = map[string]any{"text": 17} }},
		{"unexpected action", func(r *domain.SkillReply) { r.Output["request_review"] = true }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			app := &Service{skills: skillRunnerFunc(func(ctx context.Context, actor string, call domain.SkillCall) (domain.SkillReply, error) {
				if actor != "intern-1" || call.Instructions != skill.Instructions || call.Skill != skill.Target || call.Input["text"] != "Question" {
					t.Fatal("caller identity, instructions or payload changed")
				}
				reply := valid()
				tc.change(&reply)
				return reply, nil
			})}
			var output domain.MessageSkillOutput
			execution, err := app.executeSkill(context.Background(), "intern-1", skill, domain.MessageSkillInput{Text: "Question"}, &output)
			if tc.name == "valid" {
				if err != nil || execution.Skill != skill.SkillRef || execution.Target != skill.Target || output.Text != "Answer" {
					t.Fatalf("valid reply rejected: %+v %v", execution, err)
				}
			} else if err == nil || domain.PublicError(err).Code != "SKILL_INVALID_RESULT" {
				t.Fatalf("invalid reply accepted: %v", err)
			}
		})
	}
	app := &Service{skills: skillRunnerFunc(func(ctx context.Context, _ string, _ domain.SkillCall) (domain.SkillReply, error) {
		return domain.SkillReply{}, ctx.Err()
	})}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := app.executeSkill(ctx, "intern-1", skill, domain.MessageSkillInput{}, new(domain.MessageSkillOutput)); !errors.Is(err, context.Canceled) {
		t.Fatalf("lost cancellation: %v", err)
	}
}

func TestReviewSkillRejectsInventedLocationsRulesAndSHA(t *testing.T) {
	pr := domain.PullRequest{PRInfo: domain.PRInfo{Number: 17}, HeadSHA: "sha", Diff: "diff --git a/main.go b/main.go\n--- a/main.go\n+++ b/main.go\n@@ -1 +1 @@\n+package main\n"}
	for _, tc := range []struct {
		name   string
		change func(*domain.ReviewSkillOutput)
	}{
		{"valid", func(*domain.ReviewSkillOutput) {}},
		{"SHA", func(r *domain.ReviewSkillOutput) { r.HeadSHA = "other" }},
		{"CI", func(r *domain.ReviewSkillOutput) { r.Checks.Status = "GREEN" }},
		{"line", func(r *domain.ReviewSkillOutput) { r.Analysis.Findings[0].Location.Line = 2 }},
		{"path", func(r *domain.ReviewSkillOutput) { r.Analysis.Findings[0].Location.Path = "other.go" }},
		{"rule", func(r *domain.ReviewSkillOutput) { r.Analysis.Findings[0].Rule = &domain.Rule{ID: "invented"} }},
		{"scope", func(r *domain.ReviewSkillOutput) { r.Analysis.Assessment.Scope = "" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			output := domain.ReviewSkillOutput{PRNumber: 17, HeadSHA: "sha", Checks: domain.Checks{Status: "UNKNOWN", Label: "No CI"}, Analysis: domain.ReviewAnalysis{Assessment: domain.ReviewAssessment{Scope: "diff only", Task: "unknown"}, Findings: []domain.Finding{{ID: "F1", Kind: "assumption", Title: "Check", Description: "Why", Location: &domain.Location{Path: "main.go", Line: 1}}}}}
			tc.change(&output)
			err := validateSkillReview(output, pr, domain.Team{})
			if (tc.name == "valid") != (err == nil) {
				t.Fatalf("unexpected validation: %v", err)
			}
		})
	}
}
