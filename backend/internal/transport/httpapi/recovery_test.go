package httpapi_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
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

type loseFirstReviewReply struct {
	service.GitTools
	lost atomic.Bool
}

type failFirstAnswer struct {
	service.Assistant
	failed atomic.Bool
}

func (assistant *failFirstAnswer) Answer(ctx context.Context, input domain.ChatInput) (domain.Message, error) {
	if !assistant.failed.Swap(true) {
		return domain.Message{}, &domain.AppError{Code: "DEPENDENCY_UNAVAILABLE", Message: "temporary failure", Retryable: true}
	}
	return assistant.Assistant.Answer(ctx, input)
}

func TestRetryTransientFailureKeepsOperationAndMessageIdentity(t *testing.T) {
	store, err := repository.NewStore(filepath.Join(t.TempDir(), "application.json"))
	if err != nil {
		t.Fatal(err)
	}
	app := service.NewService(store, nil, &failFirstAnswer{Assistant: assistant.NewLocalAssistant()}, teamconfig.New(filepath.Join("..", "..", "..", "..")), nil)
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
	id := submitForTest(t, client, api.URL, "/messages", "original-key", map[string]string{"text": "Привет"})
	failed := waitForOperation(t, client, api.URL, id)
	if failed.State != "FAILED" || failed.Error == nil || !failed.Error.Retryable {
		t.Fatalf("unexpected failure: %+v", failed)
	}
	endpoint := api.URL + "/api/v1/operations/" + id + "/retry"
	apiCall(t, sessionClient(t), "POST", endpoint, "original-key", struct{}{}, http.StatusNotFound, nil)
	apiCall(t, client, "POST", endpoint, "wrong-key", struct{}{}, http.StatusConflict, nil)
	apiCall(t, client, "POST", endpoint, "original-key", struct{}{}, http.StatusAccepted, nil)
	retried := waitForOperation(t, client, api.URL, id)
	if retried.State != "SUCCEEDED" || retried.Error != nil {
		t.Fatalf("retry failed: %+v", retried)
	}
	// An exact retry after success must not execute the command a third time.
	apiCall(t, client, "POST", endpoint, "original-key", struct{}{}, http.StatusAccepted, nil)
	var workspace domain.Workspace
	apiCall(t, client, "GET", api.URL+"/api/v1/workspace", "", nil, http.StatusOK, &workspace)
	if len(workspace.Messages) != 2 || workspace.Messages[0].ID != "user_"+id || len(workspace.PendingOperations) != 0 {
		t.Fatalf("retry duplicated messages: %+v", workspace)
	}
	invalid := submitForTest(t, client, api.URL, "/self-reviews", "invalid-pr", map[string]string{"pr_ref": "18"})
	if result := waitForOperation(t, client, api.URL, invalid); result.State != "FAILED" || result.Error.Retryable {
		t.Fatalf("expected permanent failure: %+v", result)
	}
	apiCall(t, client, "POST", api.URL+"/api/v1/operations/"+invalid+"/retry", "invalid-pr", struct{}{}, http.StatusConflict, nil)
}

func (git *loseFirstReviewReply) RequestReview(ctx context.Context, actor string, number int, sha string, mentor domain.Mentor, key string) (domain.ReviewRequest, error) {
	receipt, err := git.GitTools.RequestReview(ctx, actor, number, sha, mentor, key)
	if err == nil && !git.lost.Swap(true) {
		return domain.ReviewRequest{}, errors.New("connection lost after the Git service saved the receipt")
	}
	return receipt, err
}

func TestUnknownReviewRequestSurvivesRestartAndUsesOriginalAction(t *testing.T) {
	root := filepath.Join("..", "..", "..", "..")
	directory := t.TempDir()
	fixture := filepath.Join(directory, "pr.json")
	initial, err := os.ReadFile(filepath.Join(root, "mocks", "pr-17.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(fixture, initial, 0600); err != nil {
		t.Fatal(err)
	}
	gitState := filepath.Join(directory, "git.json")
	gitHandler, err := mcpgit.NewGitHandler(fixture, gitState, "test-secret")
	if err != nil {
		t.Fatal(err)
	}
	gitAPI := httptest.NewServer(gitHandler)
	defer gitAPI.Close()
	git := &loseFirstReviewReply{GitTools: mcpgit.NewMCPGit(gitAPI.URL, "test-secret")}
	path := filepath.Join(directory, "application.json")
	start := func() (*httptest.Server, func()) {
		store, err := repository.NewStore(path)
		if err != nil {
			t.Fatal(err)
		}
		app := service.NewService(store, git, assistant.NewLocalAssistant(), teamconfig.New(root), skillTestClient(t, root, git))
		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan error, 1)
		go func() { done <- app.Run(ctx) }()
		api := httptest.NewServer(httpapi.NewHTTPHandler(app, http.NotFoundHandler(), nil))
		return api, func() {
			api.Close()
			cancel()
			if err := <-done; err != nil {
				t.Error(err)
			}
		}
	}
	api, stop := start()
	client := sessionClient(t)
	result := waitForOperation(t, client, api.URL, submitForTest(t, client, api.URL, "/self-reviews", "review", map[string]string{"pr_ref": "17"}))
	if result.Result == nil || result.Result.Review == nil {
		t.Fatalf("missing review: %+v", result)
	}
	review := result.Result.Review
	id := submitForTest(t, client, api.URL, "/review-requests", "original-confirmation", map[string]string{
		"review_run_id": review.ID, "expected_mentor_id": review.Mentor.ID,
	})
	unknown := waitForOperation(t, client, api.URL, id)
	if unknown.State != "RESULT_UNKNOWN" {
		t.Fatalf("expected an unknown outcome: %+v", unknown)
	}
	stop()

	// Git already saved the original receipt. New commits must not replace the
	// original action key or the review snapshot while reconciling that action.
	useFixedRevision(t, fixture, initial)
	api, stop = start()
	defer stop()
	var workspace domain.Workspace
	apiCall(t, client, "GET", api.URL+"/api/v1/workspace", "", nil, http.StatusOK, &workspace)
	if len(workspace.PendingOperations) != 1 {
		t.Fatalf("missing pending action: %+v", workspace)
	}
	pending := workspace.PendingOperations[0]
	if pending.OperationID != id || pending.Key != "original-confirmation" || pending.State != "RESULT_UNKNOWN" || pending.Command.ReviewRunID != review.ID {
		t.Fatalf("pending action changed identity: %+v", pending)
	}
	other := sessionClient(t)
	var foreign domain.Workspace
	apiCall(t, other, "GET", api.URL+"/api/v1/workspace", "", nil, http.StatusOK, &foreign)
	if len(foreign.PendingOperations) != 0 {
		t.Fatal("another session saw the action key")
	}
	apiCall(t, other, "POST", api.URL+"/api/v1/operations/"+id+"/retry", pending.Key, struct{}{}, http.StatusNotFound, nil)
	apiCall(t, client, "POST", api.URL+"/api/v1/operations/"+id+"/retry", pending.Key, struct{}{}, http.StatusAccepted, nil)
	reconciled := waitForOperation(t, client, api.URL, id)
	if reconciled.Result == nil || reconciled.Result.ReviewRequest == nil || reconciled.Result.ReviewRequest.HeadSHA != review.HeadSHA {
		t.Fatalf("lost the original receipt: %+v", reconciled)
	}
	apiCall(t, client, "GET", api.URL+"/api/v1/workspace", "", nil, http.StatusOK, &workspace)
	if len(workspace.PendingOperations) != 0 || len(workspace.ReviewRequests) != 1 {
		t.Fatalf("action is still pending: %+v", workspace)
	}
	data, err := os.ReadFile(gitState)
	if err != nil {
		t.Fatal(err)
	}
	var state struct{ Actions, Reviews map[string]json.RawMessage }
	if err := json.Unmarshal(data, &state); err != nil {
		t.Fatal(err)
	}
	if len(state.Actions) != 1 || len(state.Reviews) != 1 {
		t.Fatalf("reconciliation duplicated the external action: %s", data)
	}
}

func TestDemoControlsAreReadByRunningMCP(t *testing.T) {
	root := filepath.Join("..", "..", "..", "..")
	directory := t.TempDir()
	gitHandler, err := mcpgit.NewGitHandler(filepath.Join(root, "mocks", "pr-17.json"), filepath.Join(directory, "git.json"), "test-secret")
	if err != nil {
		t.Fatal(err)
	}
	api := httptest.NewServer(gitHandler)
	defer api.Close()
	git := mcpgit.NewMCPGit(api.URL, "test-secret")
	ctx := context.Background()
	if err := demo.Configure(root, directory, "", "", true); err != nil {
		t.Fatal(err)
	}
	initial, err := git.GetPullRequest(ctx, "intern-1", 17)
	if err != nil {
		t.Fatal(err)
	}
	if err := demo.Configure(root, directory, "fixed", "PASSED", false); err != nil {
		t.Fatal(err)
	}
	fixed, err := git.GetPullRequest(ctx, "intern-1", 17)
	if err != nil || fixed.HeadSHA == initial.HeadSHA {
		t.Fatalf("running MCP did not switch revision: %+v, %v", fixed, err)
	}
	checks, err := git.GetChecks(ctx, "intern-1", 17, fixed.HeadSHA)
	if err != nil || checks.Status != "PASSED" {
		t.Fatalf("MCP did not apply CI state: %+v, %v", checks, err)
	}
	if err := demo.Configure(root, directory, "", "", true); err != nil {
		t.Fatal(err)
	}
	restored, err := git.GetPullRequest(ctx, "intern-1", 17)
	if err != nil || restored.HeadSHA != initial.HeadSHA {
		t.Fatalf("reset did not restore initial PR: %+v, %v", restored, err)
	}
	checks, err = git.GetChecks(ctx, "intern-1", 17, initial.HeadSHA)
	if err != nil || checks.Status != "UNKNOWN" {
		t.Fatalf("reset did not restore CI: %+v, %v", checks, err)
	}
}
