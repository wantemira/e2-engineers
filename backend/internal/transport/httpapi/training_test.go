package httpapi_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/wantemira/e2-engineers/backend/internal/assistant"
	"github.com/wantemira/e2-engineers/backend/internal/demo"
	"github.com/wantemira/e2-engineers/backend/internal/domain"
	"github.com/wantemira/e2-engineers/backend/internal/integration/mcpgit"
	"github.com/wantemira/e2-engineers/backend/internal/repository"
	"github.com/wantemira/e2-engineers/backend/internal/service"
	"github.com/wantemira/e2-engineers/backend/internal/teamconfig"
	"github.com/wantemira/e2-engineers/backend/internal/transport/httpapi"
)

func TestTrainingQuestionReadsVersionedToolsAndRestoresAnswer(t *testing.T) {
	root := filepath.Join("..", "..", "..", "..")
	directory := t.TempDir()
	fixture := filepath.Join(directory, "pr.json")
	data, err := os.ReadFile(filepath.Join(root, "mocks", "pr-17.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(fixture, data, 0600); err != nil {
		t.Fatal(err)
	}
	gitHandler, err := mcpgit.NewGitHandler(fixture, filepath.Join(directory, "git.json"), "test-secret")
	if err != nil {
		t.Fatal(err)
	}
	gitAPI := httptest.NewServer(gitHandler)
	defer gitAPI.Close()
	git := mcpgit.NewMCPGit(gitAPI.URL, "test-secret")
	path := filepath.Join(directory, "application.json")
	store, err := repository.NewStore(path)
	if err != nil {
		t.Fatal(err)
	}
	app := service.NewService(store, git, assistant.NewLocalAssistant(), teamconfig.New(root))
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
	initialPR, err := git.GetPullRequest(ctx, "intern-1", 17)
	if err != nil {
		t.Fatal(err)
	}

	initial := waitForOperation(t, client, api.URL, submitForTest(t, client, api.URL, "/messages", "environment", map[string]string{"text": "Где тестовое окружение?"}))
	if initial.Result == nil || len(initial.Result.Messages) != 2 || initial.Result.Review != nil {
		t.Fatalf("expected a training answer: %+v", initial)
	}
	message := initial.Result.Messages[1]
	for _, expected := range []string{"orders-training", "STOPPED", initialPR.HeadSHA, "make training-up", "make training-test", "mock-инструменты"} {
		if !strings.Contains(message.Text, expected) {
			t.Errorf("answer lacks %q: %s", expected, message.Text)
		}
	}
	if len(message.Links) != 1 || !strings.HasSuffix(message.Links[0].URL, "/environment-guide") {
		t.Fatalf("missing training guide: %+v", message.Links)
	}
	if err := demo.SetEnvironment(directory, "READY"); err != nil {
		t.Fatal(err)
	}
	if err := demo.Configure(root, directory, "fixed", "FAILED", false); err != nil {
		t.Fatal(err)
	}
	fixedPR, err := git.GetPullRequest(ctx, "intern-1", 17)
	if err != nil {
		t.Fatal(err)
	}
	checked := waitForOperation(t, client, api.URL, submitForTest(t, client, api.URL, "/messages", "tests", map[string]string{"text": "Покажи результат тестов"}))
	if checked.Result == nil {
		t.Fatalf("no result: %+v", checked)
	}
	message = checked.Result.Messages[1]
	for _, expected := range []string{"READY", fixedPR.HeadSHA, "CI — FAILED", "Разберите ошибку CI", "Реальный результат локального запуска смотрите в терминале"} {
		if !strings.Contains(message.Text, expected) {
			t.Errorf("answer lacks %q: %s", expected, message.Text)
		}
	}
	invalid := waitForOperation(t, client, api.URL, submitForTest(t, client, api.URL, "/messages", "foreign", map[string]string{"text": "Проверь тестовое окружение PR #99"}))
	if invalid.Error == nil || invalid.Error.Code != "NOT_FOUND" {
		t.Fatalf("foreign PR was accepted: %+v", invalid)
	}

	reopened, err := repository.NewStore(path)
	if err != nil {
		t.Fatal(err)
	}
	restored := httptest.NewServer(httpapi.NewHTTPHandler(service.NewService(reopened, git, assistant.NewLocalAssistant(), teamconfig.New(root)), http.NotFoundHandler(), nil))
	defer restored.Close()
	var workspace domain.Workspace
	apiCall(t, client, "GET", restored.URL+"/api/v1/workspace", "", nil, http.StatusOK, &workspace)
	found := false
	for _, saved := range workspace.Messages {
		if saved.ID == message.ID && saved.Text == message.Text {
			found = true
		}
	}
	if !found {
		t.Fatal("the versioned training result did not survive reopening storage")
	}
}
