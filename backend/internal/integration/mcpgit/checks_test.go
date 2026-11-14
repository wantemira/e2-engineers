package mcpgit

import (
	"context"
	"github.com/wantemira/e2-engineers/backend/internal/domain"
	"testing"
)

func TestCIRunsAreVersionedDurableAndIdempotent(t *testing.T) {
	fixture, state := gitTestFiles(t)
	server := startGitTestServer(t, fixture, state)
	client := NewMCPGit(server.URL, "test-secret")
	ctx := context.Background()
	pr, err := client.GetPullRequest(ctx, "intern-1", 17)
	if err != nil {
		t.Fatal(err)
	}
	first, err := client.RunChecks(ctx, "intern-1", 17, pr.HeadSHA, "run-1")
	if err != nil || first.Status != "FAILED" || first.RunID == "" || len(first.Tests) != 2 || first.Tests[1].Error == "" || first.Logs == "" || first.HeadSHA != pr.HeadSHA {
		t.Fatalf("bad run: %+v, %v", first, err)
	}
	retry, err := client.RunChecks(ctx, "intern-1", 17, pr.HeadSHA, "run-1")
	if err != nil || retry.RunID != first.RunID {
		t.Fatalf("duplicate run: %+v, %v", retry, err)
	}
	second, err := client.RunChecks(ctx, "intern-1", 17, pr.HeadSHA, "run-2")
	if err != nil || second.RunID == first.RunID {
		t.Fatalf("explicit rerun did not create a new run: %+v, %v", second, err)
	}
	server.Close()
	restarted := startGitTestServer(t, fixture, state)
	client = NewMCPGit(restarted.URL, "test-secret")
	latest, err := client.GetChecks(ctx, "intern-1", 17, pr.HeadSHA)
	if err != nil || latest.RunID != second.RunID {
		t.Fatalf("latest run lost after restart: %+v, %v", latest, err)
	}
	retry, err = client.RunChecks(ctx, "intern-1", 17, pr.HeadSHA, "run-1")
	if err != nil || retry.RunID != first.RunID {
		t.Fatalf("original action lost: %+v, %v", retry, err)
	}
	useFixedRevision(t, fixture)
	fixed, err := client.GetPullRequest(ctx, "intern-1", 17)
	if err != nil {
		t.Fatal(err)
	}
	_, err = client.RunChecks(ctx, "intern-1", 17, pr.HeadSHA, "stale")
	requireGitError(t, err, "STALE_HEAD")
	_, err = client.RunChecks(ctx, "intern-1", 17, fixed.HeadSHA, "run-1")
	requireGitError(t, err, "IDEMPOTENCY_CONFLICT")
	passed, err := client.RunChecks(ctx, "intern-1", 17, fixed.HeadSHA, "fixed")
	if err != nil || passed.Status != "PASSED" || passed.HeadSHA != fixed.HeadSHA || passed.RunID == first.RunID || len(passed.Tests) != 2 || passed.Tests[1].Status != "PASSED" {
		t.Fatalf("bad fixed run: %+v, %v", passed, err)
	}
	// A review request must not erase the saved CI state.
	_, err = client.RequestReview(ctx, "intern-1", 17, fixed.HeadSHA, domain.Mentor{ID: "mentor-backend"}, "review")
	if err != nil {
		t.Fatal(err)
	}
	latest, err = client.GetChecks(ctx, "intern-1", 17, fixed.HeadSHA)
	if err != nil || latest.RunID != passed.RunID {
		t.Fatalf("review erased CI state: %+v, %v", latest, err)
	}
	for _, input := range []struct {
		actor          string
		number         int
		sha, key, code string
	}{
		{"other", 17, fixed.HeadSHA, "other", "NOT_FOUND"},
		{"intern-1", 18, fixed.HeadSHA, "foreign", "NOT_FOUND"},
		{"intern-1", 17, fixed.HeadSHA, "", "INVALID_INPUT"},
	} {
		_, err := client.RunChecks(ctx, input.actor, input.number, input.sha, input.key)
		requireGitError(t, err, input.code)
	}
}
