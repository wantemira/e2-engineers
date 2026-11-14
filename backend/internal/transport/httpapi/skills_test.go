package httpapi_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/wantemira/e2-engineers/backend/internal/assistant"
	"github.com/wantemira/e2-engineers/backend/internal/domain"
	"github.com/wantemira/e2-engineers/backend/internal/integration/mcpgit"
	"github.com/wantemira/e2-engineers/backend/internal/integration/mcpskills"
	"github.com/wantemira/e2-engineers/backend/internal/repository"
	"github.com/wantemira/e2-engineers/backend/internal/service"
	"github.com/wantemira/e2-engineers/backend/internal/teamconfig"
	"github.com/wantemira/e2-engineers/backend/internal/transport/httpapi"
)

func skillTestClient(t *testing.T, root string, git service.GitTools) *mcpskills.Client {
	t.Helper()
	handler, err := mcpskills.NewHandler(filepath.Join(root, "mocks", "skills.json"), "skill-secret", git, teamconfig.New(root), assistant.NewLocalAssistant())
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	return mcpskills.NewClient(server.URL, "skill-secret")
}

func TestAdditionalSkillNeedsOnlyFilesAndConfiguration(t *testing.T) {
	root := t.TempDir()
	for _, directory := range []string{"teams", "skills", "mocks"} {
		if err := os.CopyFS(filepath.Join(root, directory), os.DirFS(filepath.Join("../../../..", directory))); err != nil {
			t.Fatal(err)
		}
	}
	writeJSON := func(path string, value any) {
		t.Helper()
		data, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	configuration := filepath.Join(root, "teams", "demo-backend", "context.json")
	data, err := os.ReadFile(configuration)
	if err != nil {
		t.Fatal(err)
	}
	var team map[string]any
	if err := json.Unmarshal(data, &team); err != nil {
		t.Fatal(err)
	}
	team["skills"] = []string{"pr-self-review", "mentor-handoff"}
	writeJSON(configuration, team)
	directory := filepath.Join(root, "skills", "mentor-handoff")
	if err := os.MkdirAll(directory, 0700); err != nil {
		t.Fatal(err)
	}
	skill := domain.SkillDefinition{SkillRef: domain.SkillRef{ID: "mentor-handoff", Version: "1.0.0"}, Target: domain.SkillRef{ID: "internal-handoff", Version: "2.0.0"}, Contract: domain.MessageSkillContract, Description: "Test extension", Triggers: []string{"подготовь контекст наставнику"}}
	writeJSON(filepath.Join(directory, "skill.json"), skill)
	instructions := filepath.Join(directory, "SKILL.md")
	if err := os.WriteFile(instructions, []byte("Prepare context; never submit a review."), 0600); err != nil {
		t.Fatal(err)
	}
	writeJSON(filepath.Join(root, "mocks", "skills.json"), map[string]any{
		"allowed_actors": []string{"intern-1"},
		"skills":         []map[string]any{{"id": "internal-handoff", "version": "2.0.0", "contract": domain.MessageSkillContract, "executor": "fixture", "output": map[string]string{"text": "Ответ нового внутреннего скилла"}}},
	})
	gitHandler, err := mcpgit.NewGitHandler(filepath.Join(root, "mocks", "pr-17.json"), filepath.Join(root, "git.json"), "secret")
	if err != nil {
		t.Fatal(err)
	}
	gitAPI := httptest.NewServer(gitHandler)
	defer gitAPI.Close()
	git := mcpgit.NewMCPGit(gitAPI.URL, "secret")
	runner := skillTestClient(t, root, git)
	statePath := filepath.Join(root, "application.json")
	store, err := repository.NewStore(statePath)
	if err != nil {
		t.Fatal(err)
	}
	app := service.NewService(store, git, assistant.NewLocalAssistant(), teamconfig.New(root), runner)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- app.Run(ctx) }()
	defer func() {
		cancel()
		if err := <-done; err != nil {
			t.Error(err)
		}
	}()
	api := httptest.NewServer(httpapi.NewHTTPHandler(app, http.NotFoundHandler(), nil))
	defer api.Close()
	client := sessionClient(t)
	run := func(key string) domain.Operation {
		return waitForOperation(t, client, api.URL, submitForTest(t, client, api.URL, "/messages", key, map[string]string{"text": "Подготовь контекст наставнику"}))
	}
	first := run("extension-1")
	if first.Result == nil || len(first.Result.Messages) != 2 {
		t.Fatalf("new skill was not invoked: %+v", first)
	}
	message := first.Result.Messages[1]
	if !strings.Contains(message.Text, "Ответ нового внутреннего скилла") || !strings.Contains(message.Text, "Источник — mock") || message.Skill == nil || message.Skill.Skill != skill.SkillRef || message.Skill.Target != skill.Target || message.Skill.Source != "MOCK" || message.Receipt != nil {
		t.Fatalf("new skill lost its identity or performed an action: %+v", message)
	}
	if err := os.WriteFile(instructions, []byte("Updated instructions; preserve original context."), 0600); err != nil {
		t.Fatal(err)
	}
	second := run("extension-2")
	if second.Result == nil || second.Result.Messages[1].Skill.InstructionsSHA256 == message.Skill.InstructionsSHA256 {
		t.Fatal("changed instructions were not transmitted to the skill")
	}
	reopened, err := repository.NewStore(statePath)
	if err != nil {
		t.Fatal(err)
	}
	restored := httptest.NewServer(httpapi.NewHTTPHandler(service.NewService(reopened, git, assistant.NewLocalAssistant(), teamconfig.New(root), runner), http.NotFoundHandler(), nil))
	defer restored.Close()
	var workspace domain.Workspace
	apiCall(t, client, "GET", restored.URL+"/api/v1/workspace", "", nil, http.StatusOK, &workspace)
	if len(workspace.Messages) != 4 || workspace.Messages[1].Skill == nil || workspace.Messages[1].Skill.RunID != message.Skill.RunID {
		t.Fatalf("skill execution was not persisted: %+v", workspace)
	}
	// No configured internal review exists. The application must fail explicitly,
	// rather than silently running the old local analyzer.
	failed := waitForOperation(t, client, api.URL, submitForTest(t, client, api.URL, "/self-reviews", "unavailable", map[string]string{"pr_ref": "17"}))
	if failed.State != "FAILED" || failed.Result != nil || failed.Error == nil || failed.Error.Code != "SKILL_UNAVAILABLE" {
		t.Fatalf("unavailable skill silently fell back: %+v", failed)
	}
	apiCall(t, client, "GET", api.URL+"/api/v1/workspace", "", nil, http.StatusOK, &workspace)
	if len(workspace.Reviews) != 0 || len(workspace.ReviewRequests) != 0 {
		t.Fatal("failed skill produced a review or a Git action")
	}
}
