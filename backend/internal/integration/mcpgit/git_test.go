package mcpgit

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/wantemira/e2-engineers/backend/internal/domain"
)

func gitTestFiles(t *testing.T) (string, string) {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "..", "..", "..", "mocks", "pr-17.json"))
	if err != nil {
		t.Fatal(err)
	}
	directory := t.TempDir()
	fixture := filepath.Join(directory, "pr.json")
	if err := os.WriteFile(fixture, data, 0600); err != nil {
		t.Fatal(err)
	}
	return fixture, filepath.Join(directory, "git-state.json")
}

func startGitTestServer(t *testing.T, fixture, state string) *httptest.Server {
	t.Helper()
	handler, err := NewGitHandler(fixture, state, "test-secret")
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	return server
}

func requireGitError(t *testing.T, err error, code string) {
	t.Helper()
	var problem *domain.AppError
	if !errors.As(err, &problem) || problem.Code != code {
		t.Fatalf("expected %s, got %v", code, err)
	}
}

func useFixedRevision(t *testing.T, path string) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var fixture gitFixture
	if err := json.Unmarshal(data, &fixture); err != nil {
		t.Fatal(err)
	}
	fixture.CurrentRevision = "fixed"
	data, err = json.Marshal(fixture)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
}

func TestMCPReadsCurrentPRAndChecks(t *testing.T) {
	fixture, state := gitTestFiles(t)
	server := startGitTestServer(t, fixture, state)
	client := NewMCPGit(server.URL, "test-secret")
	ctx := context.Background()
	pr, err := client.GetPullRequest(ctx, "intern-1", 17)
	if err != nil {
		t.Fatal(err)
	}
	if pr.Number != 17 || pr.Repository != "e2-engineers" || !strings.Contains(pr.Diff, "errors.New") {
		t.Fatalf("unexpected PR: %+v", pr)
	}
	checks, err := client.GetChecks(ctx, "intern-1", 17, pr.HeadSHA)
	if err != nil || checks.Status != "UNKNOWN" {
		t.Fatalf("checks = %+v, error = %v", checks, err)
	}
	_, err = client.GetChecks(ctx, "intern-1", 17, "other-commit")
	requireGitError(t, err, "STALE_HEAD")

	useFixedRevision(t, fixture)
	updated, err := client.GetPullRequest(ctx, "intern-1", 17)
	if err != nil {
		t.Fatal(err)
	}
	if updated.HeadSHA == pr.HeadSHA || !strings.Contains(updated.Diff, "%w") {
		t.Fatal("a new tool call did not load the current fixture")
	}
}

func TestMCPRejectsForeignPRAndActor(t *testing.T) {
	fixture, state := gitTestFiles(t)
	server := startGitTestServer(t, fixture, state)
	client := NewMCPGit(server.URL, "test-secret")
	for _, test := range []struct {
		name   string
		actor  string
		number int
	}{
		{name: "foreign actor", actor: "intern-2", number: 17},
		{name: "foreign PR", actor: "intern-1", number: 18},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, err := client.GetPullRequest(context.Background(), test.actor, test.number)
			requireGitError(t, err, "NOT_FOUND")
			_, err = client.RequestReview(context.Background(), test.actor, test.number, "sha", domain.Mentor{ID: "mentor-backend"}, "confirm")
			requireGitError(t, err, "NOT_FOUND")
		})
	}
}

func TestMCPRequiresServiceAuthentication(t *testing.T) {
	fixture, state := gitTestFiles(t)
	server := startGitTestServer(t, fixture, state)
	response, err := http.Post(server.URL, "application/json", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`))
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", response.StatusCode)
	}
}

func TestMCPReviewRequestIsDurableAndIdempotent(t *testing.T) {
	fixture, state := gitTestFiles(t)
	server := startGitTestServer(t, fixture, state)
	client := NewMCPGit(server.URL, "test-secret")
	ctx := context.Background()
	pr, err := client.GetPullRequest(ctx, "intern-1", 17)
	if err != nil {
		t.Fatal(err)
	}
	mentor := domain.Mentor{ID: "mentor-backend", Name: "Caller cannot replace the mentor name"}
	receipt, err := client.RequestReview(ctx, "intern-1", 17, pr.HeadSHA, mentor, "confirm-1")
	if err != nil {
		t.Fatal(err)
	}
	if receipt.ID == "" || receipt.Mentor.Name != "Алексей, Backend" || receipt.Status != "REQUESTED" {
		t.Fatalf("invalid receipt: %+v", receipt)
	}

	useFixedRevision(t, fixture)
	repeated, err := client.RequestReview(ctx, "intern-1", 17, pr.HeadSHA, mentor, "confirm-1")
	if err != nil || repeated != receipt {
		t.Fatalf("exact retry = %+v, error = %v", repeated, err)
	}
	_, err = client.RequestReview(ctx, "intern-1", 17, "different", mentor, "confirm-1")
	requireGitError(t, err, "IDEMPOTENCY_CONFLICT")
	_, err = client.RequestReview(ctx, "intern-1", 17, pr.HeadSHA, mentor, "confirm-2")
	requireGitError(t, err, "STALE_HEAD")

	server.Close()
	restarted := startGitTestServer(t, fixture, state)
	client = NewMCPGit(restarted.URL, "test-secret")
	restored, err := client.RequestReview(ctx, "intern-1", 17, pr.HeadSHA, mentor, "confirm-1")
	if err != nil || restored != receipt {
		t.Fatalf("restored retry = %+v, error = %v", restored, err)
	}
	current, err := client.GetPullRequest(ctx, "intern-1", 17)
	if err != nil {
		t.Fatal(err)
	}
	duplicate, err := client.RequestReview(ctx, "intern-1", 17, current.HeadSHA, mentor, "confirm-new-key")
	if err != nil || duplicate.ID != receipt.ID {
		t.Fatalf("business duplicate = %+v, error = %v", duplicate, err)
	}
}

func TestConcurrentMCPReviewRequestsCreateOneReceipt(t *testing.T) {
	fixture, state := gitTestFiles(t)
	server := startGitTestServer(t, fixture, state)
	client := NewMCPGit(server.URL, "test-secret")
	pr, err := client.GetPullRequest(context.Background(), "intern-1", 17)
	if err != nil {
		t.Fatal(err)
	}
	var workers sync.WaitGroup
	results := make(chan domain.ReviewRequest, 4)
	errors := make(chan error, 4)
	for range 4 {
		workers.Go(func() {
			receipt, err := client.RequestReview(context.Background(), "intern-1", 17, pr.HeadSHA, domain.Mentor{ID: "mentor-backend"}, "same-key")
			results <- receipt
			errors <- err
		})
	}
	workers.Wait()
	close(results)
	close(errors)
	for err := range errors {
		if err != nil {
			t.Fatal(err)
		}
	}
	var first string
	for receipt := range results {
		if first == "" {
			first = receipt.ID
		}
		if receipt.ID != first {
			t.Fatal("concurrent writes created different review IDs")
		}
	}
}

func TestMCPRejectsInvalidReviewConfirmation(t *testing.T) {
	fixture, state := gitTestFiles(t)
	server := startGitTestServer(t, fixture, state)
	client := NewMCPGit(server.URL, "test-secret")
	pr, err := client.GetPullRequest(context.Background(), "intern-1", 17)
	if err != nil {
		t.Fatal(err)
	}
	for _, input := range []struct {
		name   string
		sha    string
		mentor string
		key    string
	}{
		{name: "missing SHA", mentor: "mentor-backend", key: "confirm"},
		{name: "missing key", sha: pr.HeadSHA, mentor: "mentor-backend"},
		{name: "unknown mentor", sha: pr.HeadSHA, mentor: "other", key: "confirm"},
	} {
		t.Run(input.name, func(t *testing.T) {
			_, err := client.RequestReview(context.Background(), "intern-1", 17, input.sha, domain.Mentor{ID: input.mentor}, input.key)
			requireGitError(t, err, "INVALID_INPUT")
		})
	}
	if _, err := os.Stat(state); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("invalid confirmation created state: %v", err)
	}
}

func TestMCPPropagatesContextCancellation(t *testing.T) {
	fixture, state := gitTestFiles(t)
	server := startGitTestServer(t, fixture, state)
	client := NewMCPGit(server.URL, "test-secret")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := client.GetPullRequest(ctx, "intern-1", 17)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected cancellation, got %v", err)
	}
}

func TestGitHandlerRejectsBrokenConfiguration(t *testing.T) {
	fixture, state := gitTestFiles(t)
	if _, err := NewGitHandler(fixture, state, ""); err == nil {
		t.Fatal("empty service secret accepted")
	}
	if _, err := NewGitHandler(filepath.Join(t.TempDir(), "missing.json"), state, "secret"); err == nil {
		t.Fatal("missing fixture accepted")
	}
	if err := os.WriteFile(state, []byte(`{"actions":null,"reviews":null}`), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := NewGitHandler(fixture, state, "secret"); err == nil {
		t.Fatal("invalid stored state accepted")
	}
	if err := os.WriteFile(state, []byte(`invalid JSON`), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := NewGitHandler(fixture, state, "secret"); err == nil {
		t.Fatal("corrupt stored state accepted")
	}
}

func TestMCPReadRejectsBrokenFixtureWithoutDisclosingIt(t *testing.T) {
	fixture, state := gitTestFiles(t)
	server := startGitTestServer(t, fixture, state)
	client := NewMCPGit(server.URL, "test-secret")
	if err := os.WriteFile(fixture, []byte(`not valid JSON`), 0600); err != nil {
		t.Fatal(err)
	}
	_, err := client.GetPullRequest(context.Background(), "intern-1", 17)
	requireGitError(t, err, "DEPENDENCY_UNAVAILABLE")
	if strings.Contains(err.Error(), fixture) {
		t.Fatal("internal fixture path disclosed in API error")
	}
}
