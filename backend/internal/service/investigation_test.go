package service

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/wantemira/e2-engineers/backend/internal/domain"
	"github.com/wantemira/e2-engineers/backend/internal/integration/mcpknowledge"
	"github.com/wantemira/e2-engineers/backend/internal/teamconfig"
	"github.com/wantemira/e2-engineers/backend/internal/teamhelp"
)

type knowledgeStub struct {
	team    domain.Team
	err     error
	foreign bool
}

func (s knowledgeStub) Search(_ context.Context, actor, query string) (domain.KnowledgeSearch, error) {
	if actor != "intern-1" {
		panic("actor was not propagated")
	}
	result := mcpknowledge.Search(s.team, query)
	if s.foreign {
		result.Hits = []domain.KnowledgeHit{{Reference: "foreign#private"}}
	}
	// A corrupted adapter cannot invent content or source URLs.
	for i := range result.Hits {
		result.Hits[i].Excerpt = "INVENTED"
		result.Hits[i].Source.URL = "https://evil.example"
	}
	return result, s.err
}

type diagnosticGit struct {
	GitTools
	reads              int
	stale, unavailable bool
}

func (g *diagnosticGit) GetPullRequest(context.Context, string, int) (domain.PullRequest, error) {
	g.reads++
	sha := "sha"
	if g.stale && g.reads > 1 {
		sha = "new-sha"
	}
	return domain.PullRequest{PRInfo: domain.PRInfo{Number: 17, Repository: "e2-engineers"}, OwnerID: "intern-1", HeadSHA: sha}, nil
}
func (g *diagnosticGit) GetChecks(context.Context, string, int, string) (domain.Checks, error) {
	if g.unavailable {
		return domain.Checks{}, errors.New("down")
	}
	return domain.Checks{Status: "FAILED", Label: "test failed"}, nil
}
func (g *diagnosticGit) GetTrainingEnvironment(context.Context, string, int, string) (domain.TrainingEnvironment, error) {
	if g.unavailable {
		return domain.TrainingEnvironment{}, errors.New("down")
	}
	return domain.TrainingEnvironment{Service: "orders-training", Name: "training", Source: "MOCK", Status: "STOPPED", PRNumber: 17, HeadSHA: "sha"}, nil
}
func helpTeam(t *testing.T) domain.Team {
	t.Helper()
	team, err := teamconfig.New("../../..").Load()
	if err != nil {
		t.Fatal(err)
	}
	return team
}
func TestInvestigationSearchesBeforeEscalatingAndKeepsToolEvidence(t *testing.T) {
	team := helpTeam(t)
	git := &diagnosticGit{}
	app := Service{git: git, knowledge: knowledgeStub{team: team}}
	input := domain.ChatInput{Team: team, Text: "Не могу запустить orders: 502"}
	message, err := app.investigate(context.Background(), "intern-1", input, input.Text)
	if err != nil || message.MentorDraft != "" || message.Help.Investigation == nil || !strings.Contains(message.Text, "STOPPED") || !strings.Contains(message.Text, "make training-up") || strings.Contains(message.Text, "INVENTED") {
		t.Fatalf("bad evidence: %+v %v", message, err)
	}
	for _, link := range message.Links {
		if strings.Contains(link.URL, "evil") {
			t.Fatal("adapter injected a link")
		}
	}
	input.History = []domain.Message{{Role: "user", Text: input.Text}, message}
	input.Text = "Не помогло"
	second, err := app.investigate(context.Background(), "intern-1", input, input.Text)
	if err != nil || !strings.Contains(second.Text, "Предыдущая рекомендация не помогла") || !strings.Contains(second.Help.Investigation.Search.Query, "502") {
		t.Fatalf("lost followup: %+v %v", second, err)
	}
	input.History = append(input.History, domain.Message{Role: "user", Text: input.Text}, second)
	input.Text = "Подготовь вопрос наставнику"
	draft := teamhelp.Escalate(input)
	for _, fact := range []string{"502", "STOPPED", "Проверки Buddy", "troubleshooting#section-1", "Диагностика синтетическая"} {
		if !strings.Contains(draft.MentorDraft, fact) {
			t.Fatalf("missing %s in %s", fact, draft.MentorDraft)
		}
	}
}
func TestInvestigationDoesNotTreatOutageOrStaleSnapshotAsNoResults(t *testing.T) {
	team := helpTeam(t)
	for _, scenario := range []string{"search down", "foreign source", "stale head", "diagnostics down", "no results"} {
		t.Run(scenario, func(t *testing.T) {
			git := &diagnosticGit{}
			knowledge := knowledgeStub{team: team}
			query := "orders 502"
			switch scenario {
			case "search down":
				knowledge.err = &domain.AppError{Code: "DEPENDENCY_UNAVAILABLE", Retryable: true}
			case "foreign source":
				knowledge.foreign = true
			case "stale head":
				git.stale = true
			case "diagnostics down":
				git.unavailable = true
			case "no results":
				query = "Kubernetes"
			}
			app := Service{git: git, knowledge: knowledge}
			message, err := app.investigate(context.Background(), "intern-1", domain.ChatInput{Team: team, Text: query}, query)
			if scenario == "diagnostics down" {
				if err != nil || !strings.Contains(message.Text, "проверка не выполнена") || strings.Contains(strings.Join(message.Help.Investigation.Observations, " "), "STOPPED") {
					t.Fatalf("invented diagnosis: %+v %v", message, err)
				}
				return
			}
			if scenario == "no results" {
				if err != nil || message.MentorDraft != "" || len(message.Help.Investigation.Search.Hits) != 0 || git.reads != 0 {
					t.Fatalf("bad empty search: %+v %v", message, err)
				}
				return
			}
			if err == nil {
				t.Fatal("invalid evidence accepted")
			}
		})
	}
}
