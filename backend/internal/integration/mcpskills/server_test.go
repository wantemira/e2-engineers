package mcpskills

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/wantemira/e2-engineers/backend/internal/assistant"
	"github.com/wantemira/e2-engineers/backend/internal/demo"
	"github.com/wantemira/e2-engineers/backend/internal/domain"
	"github.com/wantemira/e2-engineers/backend/internal/integration/mcpgit"
	"github.com/wantemira/e2-engineers/backend/internal/teamconfig"
)

func TestExecuteSkillOverMCPChecksIdentityVersionAndPR(t *testing.T) {
	root := filepath.Join("..", "..", "..", "..")
	gitState := filepath.Join(t.TempDir(), "git.json")
	if err := demo.Configure(root, filepath.Dir(gitState), "", "", true); err != nil {
		t.Fatal(err)
	}
	gitHandler, err := mcpgit.NewGitHandler(filepath.Join(root, "mocks", "pr-17.json"), gitState, "secret")
	if err != nil {
		t.Fatal(err)
	}
	gitServer := httptest.NewServer(gitHandler)
	defer gitServer.Close()
	git := mcpgit.NewMCPGit(gitServer.URL, "secret")
	handler, err := NewHandler(filepath.Join(root, "mocks", "skills.json"), "secret", git, teamconfig.New(root), assistant.NewLocalAssistant())
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(handler)
	defer server.Close()
	client := NewClient(server.URL, "secret")
	pr, err := git.GetPullRequest(context.Background(), "intern-1", 17)
	if err != nil {
		t.Fatal(err)
	}
	call := domain.SkillCall{Skill: domain.SkillRef{ID: "internal-pr-review", Version: "1.0.0"}, Contract: domain.ReviewSkillContract,
		Instructions: "Use team rules; never write to Git.", Input: map[string]any{"pr_number": 17, "head_sha": pr.HeadSHA}}
	reply, err := client.Execute(context.Background(), "intern-1", call)
	if err != nil {
		t.Fatal(err)
	}
	var output domain.ReviewSkillOutput
	digest := sha256.Sum256([]byte(call.Instructions))
	if domain.DecodeSkillPayload(reply.Output, &output) != nil || output.HeadSHA != pr.HeadSHA || len(output.Analysis.Findings) == 0 || reply.RunID == "" || reply.Source != "MOCK" || reply.InstructionsSHA256 != hex.EncodeToString(digest[:]) {
		t.Fatalf("invalid internal review reply: %+v", reply)
	}
	for _, tc := range []struct {
		name, actor, code string
		change            func(*domain.SkillCall)
	}{
		{"foreign actor", "intern-2", "NOT_FOUND", func(*domain.SkillCall) {}},
		{"unknown version", "intern-1", "SKILL_UNAVAILABLE", func(c *domain.SkillCall) { c.Skill.Version = "9.0.0" }},
		{"wrong contract", "intern-1", "INVALID_INPUT", func(c *domain.SkillCall) { c.Contract = domain.MessageSkillContract }},
		{"no instructions", "intern-1", "INVALID_INPUT", func(c *domain.SkillCall) { c.Instructions = "" }},
		{"foreign PR", "intern-1", "NOT_FOUND", func(c *domain.SkillCall) { c.Input = map[string]any{"pr_number": 99, "head_sha": pr.HeadSHA} }},
		{"stale SHA", "intern-1", "STALE_HEAD", func(c *domain.SkillCall) { c.Input = map[string]any{"pr_number": 17, "head_sha": "old"} }},
		{"extra input", "intern-1", "INVALID_INPUT", func(c *domain.SkillCall) {
			c.Input = map[string]any{"pr_number": 17, "head_sha": pr.HeadSHA, "actor": "intern-2"}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			input := call
			tc.change(&input)
			_, err := client.Execute(context.Background(), tc.actor, input)
			var problem *domain.AppError
			if !errors.As(err, &problem) || problem.Code != tc.code {
				t.Fatalf("want %s, got %v", tc.code, err)
			}
		})
	}
	if _, err := NewClient(server.URL, "wrong-secret").Execute(context.Background(), "intern-1", call); err == nil {
		t.Fatal("unauthenticated service executed a skill")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := client.Execute(ctx, "intern-1", call); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation lost: %v", err)
	}
	if _, err := os.Stat(gitState); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("read-only skill changed Git state: %v", err)
	}
}
