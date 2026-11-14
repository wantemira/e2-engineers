// Package mcpskills implements the mock boundary of the corporate skill service.
package mcpskills

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/wantemira/e2-engineers/backend/internal/domain"
	"github.com/wantemira/e2-engineers/backend/internal/integration/mcptransport"
)

type GitReader interface {
	GetPullRequest(context.Context, string, int) (domain.PullRequest, error)
	GetChecks(context.Context, string, int, string) (domain.Checks, error)
}

type TeamLoader interface{ Load() (domain.Team, error) }
type Reviewer interface {
	Review(context.Context, domain.PullRequest, domain.Checks, domain.Team) (domain.ReviewAnalysis, error)
}

func NewHandler(path, secret string, git GitReader, teams TeamLoader, reviewer Reviewer) (http.Handler, error) {
	if secret == "" || git == nil || teams == nil || reviewer == nil {
		return nil, errors.New("internal skills require authentication and dependencies")
	}
	if _, err := loadDefinitions(path); err != nil {
		return nil, err
	}
	handler := mcp.NewStreamableHTTPHandler(func(r *http.Request) *mcp.Server {
		server := mcp.NewServer(&mcp.Implementation{Name: "ai-buddy-internal-skills-mock", Version: "1.0.0"}, nil)
		mcp.AddTool(server, &mcp.Tool{Name: "execute_skill", Description: "Execute a versioned mock internal skill; never writes to Git or runs PR code"},
			func(ctx context.Context, _ *mcp.CallToolRequest, call domain.SkillCall) (*mcp.CallToolResult, domain.SkillReply, error) {
				reply, err := execute(ctx, path, r.Header.Get("X-Buddy-Actor"), call, git, teams, reviewer)
				if err != nil {
					data, _ := json.Marshal(domain.PublicError(err))
					err = errors.New(string(data))
				}
				return nil, reply, err
			})
		return server
	}, &mcp.StreamableHTTPOptions{Stateless: true, JSONResponse: true})
	return mcptransport.Authenticate(secret, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.Body = http.MaxBytesReader(w, r.Body, 256*1024)
		handler.ServeHTTP(w, r)
	})), nil
}

func execute(ctx context.Context, path, actor string, call domain.SkillCall, git GitReader, teams TeamLoader, reviewer Reviewer) (domain.SkillReply, error) {
	var reply domain.SkillReply
	if err := ctx.Err(); err != nil {
		return reply, err
	}
	config, err := loadDefinitions(path)
	if err != nil {
		return reply, err
	}
	if !slices.Contains(config.AllowedActors, actor) {
		return reply, domain.Problem("NOT_FOUND", "Скилл недоступен.")
	}
	index := slices.IndexFunc(config.Skills, func(skill definition) bool { return skill.SkillRef == call.Skill })
	if index < 0 {
		return reply, domain.Problem("SKILL_UNAVAILABLE", "Запрошенная версия внутреннего скилла недоступна.")
	}
	skill := config.Skills[index]
	if skill.Contract != call.Contract || strings.TrimSpace(call.Instructions) == "" || len(call.Instructions) > 64*1024 {
		return reply, domain.Problem("INVALID_INPUT", "Некорректный контракт или инструкции скилла.")
	}
	var output any
	switch skill.Executor {
	case "review-analyzer":
		var input domain.ReviewSkillInput
		if domain.DecodeSkillPayload(call.Input, &input) != nil || input.PRNumber < 1 || input.HeadSHA == "" {
			return reply, domain.Problem("INVALID_INPUT", "Укажите PR и версию для внутреннего ревью.")
		}
		pr, err := git.GetPullRequest(ctx, actor, input.PRNumber)
		if err != nil {
			return reply, err
		}
		team, err := teams.Load()
		if err != nil {
			return reply, err
		}
		if pr.OwnerID != actor || pr.Repository != team.Repository || pr.Number != team.PRNumber {
			return reply, domain.Problem("NOT_FOUND", "PR недоступен внутреннему скиллу.")
		}
		if pr.HeadSHA != input.HeadSHA {
			return reply, domain.Problem("STALE_HEAD", "PR обновился до вызова внутреннего ревью.")
		}
		checks, err := git.GetChecks(ctx, actor, pr.Number, pr.HeadSHA)
		if err != nil {
			if domain.PublicError(err).Code == "STALE_HEAD" {
				return reply, err
			}
			checks = domain.Checks{Status: "UNKNOWN", Label: "Нет данных о проверках"}
		}
		analysis, err := reviewer.Review(ctx, pr, checks, team)
		if err != nil {
			return reply, err
		}
		output = domain.ReviewSkillOutput{PRNumber: pr.Number, HeadSHA: pr.HeadSHA, Checks: checks, Analysis: analysis}
	case "fixture":
		var input domain.MessageSkillInput
		if domain.DecodeSkillPayload(call.Input, &input) != nil || strings.TrimSpace(input.Text) == "" || utf8.RuneCountInString(input.Text) > 8000 {
			return reply, domain.Problem("INVALID_INPUT", "Некорректный вход текстового скилла.")
		}
		output = skill.Output
	}
	payload, err := domain.SkillPayload(output)
	if err != nil {
		return reply, err
	}
	digest := sha256.Sum256([]byte(call.Instructions))
	return domain.SkillReply{Skill: call.Skill, Contract: call.Contract, Source: "MOCK", RunID: "skill_" + rand.Text(),
		InstructionsSHA256: hex.EncodeToString(digest[:]), Output: payload}, nil
}
