package mcpgit

import (
	"context"
	"github.com/wantemira/e2-engineers/backend/internal/domain"
	"github.com/wantemira/e2-engineers/backend/internal/integration/mcptransport"
)

// Client calls Git tools over the MCP streamable HTTP transport.
type Client struct {
	connection *mcptransport.Client
}

func NewMCPGit(endpoint, secret string) *Client {
	return &Client{connection: mcptransport.NewClient(endpoint, secret)}
}

func (git *Client) GetTrainingEnvironment(ctx context.Context, actor string, number int, sha string) (domain.TrainingEnvironment, error) {
	var output domain.TrainingEnvironment
	err := git.connection.Call(ctx, actor, "get_training_environment", checkArguments{Number: number, HeadSHA: sha}, &output)
	return output, err
}

func (git *Client) GetPullRequest(ctx context.Context, actor string, number int) (domain.PullRequest, error) {
	var output domain.PullRequest
	err := git.connection.Call(ctx, actor, "get_pull_request", prArguments{Number: number}, &output)
	return output, err
}

func (git *Client) GetChecks(ctx context.Context, actor string, number int, sha string) (domain.Checks, error) {
	var output domain.Checks
	err := git.connection.Call(ctx, actor, "get_pull_request_checks", checkArguments{Number: number, HeadSHA: sha}, &output)
	return output, err
}

func (git *Client) RunChecks(ctx context.Context, actor string, number int, sha, key string) (domain.Checks, error) {
	var output domain.Checks
	err := git.connection.Call(ctx, actor, "run_pull_request_checks", runCheckArguments{Number: number, HeadSHA: sha, ActionKey: key}, &output)
	return output, err
}

func (git *Client) RequestReview(ctx context.Context, actor string, number int, sha string, mentor domain.Mentor, key string) (domain.ReviewRequest, error) {
	var output domain.ReviewRequest
	err := git.connection.Call(ctx, actor, "request_pull_request_review", reviewArguments{
		Number: number, ExpectedHeadSHA: sha, MentorID: mentor.ID, ActionKey: key,
	}, &output)
	return output, err
}
