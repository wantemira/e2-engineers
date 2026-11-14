package httpapi_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/wantemira/e2-engineers/backend/internal/assistant"
	"github.com/wantemira/e2-engineers/backend/internal/domain"
	"github.com/wantemira/e2-engineers/backend/internal/integration/mcpgit"
	"github.com/wantemira/e2-engineers/backend/internal/repository"
	"github.com/wantemira/e2-engineers/backend/internal/service"
	"github.com/wantemira/e2-engineers/backend/internal/teamconfig"
	"github.com/wantemira/e2-engineers/backend/internal/transport/httpapi"
)

type loseFirstChecksReply struct {
	service.GitTools
	lost    atomic.Bool
	firstID atomic.Value
}

func (git *loseFirstChecksReply) RunChecks(ctx context.Context, actor string, number int, sha, key string) (domain.Checks, error) {
	checks, err := git.GitTools.RunChecks(ctx, actor, number, sha, key)
	if err == nil && !git.lost.Swap(true) {
		git.firstID.Store(checks.RunID)
		return domain.Checks{}, errors.New("connection lost after CI saved the result")
	}
	return checks, err
}

func TestSelfReviewCIRetryAndRevisionSnapshots(t *testing.T) {
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
	handler, err := mcpgit.NewGitHandler(fixture, filepath.Join(directory, "git.json"), "test-secret")
	if err != nil {
		t.Fatal(err)
	}
	gitAPI := httptest.NewServer(handler)
	defer gitAPI.Close()
	git := &loseFirstChecksReply{GitTools: mcpgit.NewMCPGit(gitAPI.URL, "test-secret")}
	store, err := repository.NewStore(filepath.Join(directory, "application.json"))
	if err != nil {
		t.Fatal(err)
	}
	app := service.NewService(store, git, assistant.NewLocalAssistant(), teamconfig.New(root), skillTestClient(t, root, git))
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
	id := submitForTest(t, client, api.URL, "/messages", "combined", map[string]string{"text": "Проверь PR #17 и запусти тесты"})
	failed := waitForOperation(t, client, api.URL, id)
	if failed.State != "FAILED" || !failed.Error.Retryable {
		t.Fatalf("expected temporary CI error: %+v", failed)
	}
	apiCall(t, client, "POST", api.URL+"/api/v1/operations/"+id+"/retry", "combined", struct{}{}, http.StatusAccepted, nil)
	retried := waitForOperation(t, client, api.URL, id)
	if retried.State != "SUCCEEDED" || retried.Result.Review == nil {
		t.Fatalf("retry failed: %+v", retried)
	}
	initial := retried.Result.Review
	if initial.Checks.RunID != git.firstID.Load().(string) || initial.Checks.Status != "FAILED" || !strings.Contains(initial.Assessment.CI, "fmt.Errorf") || len(initial.Assessment.SuggestedTests) == 0 {
		t.Fatalf("missing CI evidence or duplicate run: %+v", initial)
	}
	explained := waitForOperation(t, client, api.URL, submitForTest(t, client, api.URL, "/messages", "explain-ci", map[string]string{"text": "Почему упал тест?"}))
	if explained.State != "SUCCEEDED" || !strings.Contains(explained.Result.Messages[1].Text, "TestCreateRepositoryError") {
		t.Fatalf("missing CI follow-up: %+v", explained)
	}
	useFixedRevision(t, fixture, data)
	fixed := waitForOperation(t, client, api.URL, submitForTest(t, client, api.URL, "/self-reviews", "fixed", map[string]any{"pr_ref": "17", "run_checks": true}))
	if fixed.State != "SUCCEEDED" || fixed.Result.Review.Checks.Status != "PASSED" || fixed.Result.Review.HeadSHA == initial.HeadSHA || fixed.Result.Review.Checks.RunID == initial.Checks.RunID {
		t.Fatalf("wrong new revision CI: %+v", fixed)
	}
	var workspace domain.Workspace
	apiCall(t, client, "GET", api.URL+"/api/v1/workspace", "", nil, http.StatusOK, &workspace)
	if len(workspace.Reviews) != 2 || workspace.Reviews[1].Checks.RunID != initial.Checks.RunID || workspace.Reviews[1].Checks.Tests[1].Error == "" {
		t.Fatal("CI snapshots were overwritten")
	}
}
