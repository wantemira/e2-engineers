package mcpgit

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/wantemira/e2-engineers/backend/internal/domain"
)

// Client calls Git tools over the MCP streamable HTTP transport.
type Client struct {
	endpoint string
	secret   string
}

func NewMCPGit(endpoint, secret string) *Client {
	return &Client{endpoint: endpoint, secret: secret}
}

func (git *Client) GetPullRequest(ctx context.Context, actor string, number int) (domain.PullRequest, error) {
	var output domain.PullRequest
	err := git.call(ctx, actor, "get_pull_request", prArguments{Number: number}, &output)
	return output, err
}

func (git *Client) GetChecks(ctx context.Context, actor string, number int, sha string) (domain.Checks, error) {
	var output domain.Checks
	err := git.call(ctx, actor, "get_pull_request_checks", checkArguments{Number: number, HeadSHA: sha}, &output)
	return output, err
}

func (git *Client) RequestReview(ctx context.Context, actor string, number int, sha string, mentor domain.Mentor, key string) (domain.ReviewRequest, error) {
	var output domain.ReviewRequest
	err := git.call(ctx, actor, "request_pull_request_review", reviewArguments{
		Number: number, ExpectedHeadSHA: sha, MentorID: mentor.ID, ActionKey: key,
	}, &output)
	return output, err
}

type actorTransport struct {
	actor  string
	secret string
}

func (transport actorTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	copy := request.Clone(request.Context())
	copy.Header.Set("Authorization", "Bearer "+transport.secret)
	copy.Header.Set("X-Buddy-Actor", transport.actor)
	return http.DefaultTransport.RoundTrip(copy)
}

func (git *Client) call(ctx context.Context, actor, name string, arguments, output any) error {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	client := mcp.NewClient(&mcp.Implementation{Name: "ai-buddy", Version: "0.1.0"}, nil)
	transport := &mcp.StreamableClientTransport{
		Endpoint:             git.endpoint,
		HTTPClient:           &http.Client{Transport: actorTransport{actor: actor, secret: git.secret}},
		DisableStandaloneSSE: true,
		MaxRetries:           -1,
	}
	session, err := client.Connect(ctx, transport, nil)
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return &domain.AppError{Code: "DEPENDENCY_UNAVAILABLE", Message: "Git-сервис временно недоступен.", Retryable: true}
	}
	defer session.Close()
	result, err := session.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: arguments})
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return &domain.AppError{Code: "DEPENDENCY_UNAVAILABLE", Message: "Не удалось выполнить Git-операцию.", Retryable: true}
	}
	if result.IsError {
		for _, content := range result.Content {
			text, ok := content.(*mcp.TextContent)
			if !ok {
				continue
			}
			var problem domain.AppError
			if json.Unmarshal([]byte(text.Text), &problem) == nil && problem.Code != "" {
				return &problem
			}
		}
		return &domain.AppError{Code: "DEPENDENCY_UNAVAILABLE", Message: "Git-сервис отклонил операцию.", Retryable: true}
	}
	data, err := json.Marshal(result.StructuredContent)
	if err != nil {
		return fmt.Errorf("encode MCP result: %w", err)
	}
	if string(data) == "null" {
		return &domain.AppError{Code: "DEPENDENCY_UNAVAILABLE", Message: "Git-сервис вернул пустой ответ.", Retryable: true}
	}
	if err := json.Unmarshal(data, output); err != nil {
		return fmt.Errorf("decode MCP result: %w", err)
	}
	return nil
}
