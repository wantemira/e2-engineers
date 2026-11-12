package buddy

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"net/http"
	"os"
	"sync"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type gitFixture struct {
	CurrentRevision string                 `json:"current_revision"`
	Revisions       map[string]gitRevision `json:"revisions"`
}

type gitRevision struct {
	PR     PullRequest `json:"pr"`
	Checks Checks      `json:"checks"`
}

type gitAction struct {
	Fingerprint string        `json:"fingerprint"`
	Receipt     ReviewRequest `json:"receipt"`
}

type gitState struct {
	Actions map[string]gitAction     `json:"actions"`
	Reviews map[string]ReviewRequest `json:"reviews"`
}

type gitServer struct {
	fixturePath string
	statePath   string
	mu          sync.Mutex
	state       gitState
}

type prArguments struct {
	Number int `json:"number" jsonschema:"Pull request number"`
}

type checkArguments struct {
	Number  int    `json:"number"`
	HeadSHA string `json:"head_sha"`
}

type reviewArguments struct {
	Number          int    `json:"number"`
	ExpectedHeadSHA string `json:"expected_head_sha"`
	MentorID        string `json:"mentor_id"`
	ActionKey       string `json:"action_key"`
}

// NewGitHandler serves the three PR tools over an authenticated MCP HTTP endpoint.
func NewGitHandler(fixturePath, statePath, secret string) (http.Handler, error) {
	if secret == "" {
		return nil, errors.New("MCP shared secret is required")
	}
	server := &gitServer{
		fixturePath: fixturePath,
		statePath:   statePath,
		state:       gitState{Actions: make(map[string]gitAction), Reviews: make(map[string]ReviewRequest)},
	}
	if _, err := server.revision(); err != nil {
		return nil, err
	}
	data, err := os.ReadFile(statePath)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("read Git state: %w", err)
	}
	if err == nil {
		if err := json.Unmarshal(data, &server.state); err != nil {
			return nil, fmt.Errorf("decode Git state: %w", err)
		}
		if server.state.Actions == nil || server.state.Reviews == nil {
			return nil, errors.New("Git state is missing action or review records")
		}
	}

	handler := mcp.NewStreamableHTTPHandler(func(request *http.Request) *mcp.Server {
		return server.tools(request.Header.Get("X-Buddy-Actor"))
	}, &mcp.StreamableHTTPOptions{Stateless: true, JSONResponse: true})

	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		provided := request.Header.Get("Authorization")
		if subtle.ConstantTimeCompare([]byte(provided), []byte("Bearer "+secret)) != 1 || request.Header.Get("X-Buddy-Actor") == "" {
			http.Error(writer, "Unauthorized", http.StatusUnauthorized)
			return
		}
		handler.ServeHTTP(writer, request)
	}), nil
}

func (server *gitServer) tools(actor string) *mcp.Server {
	tools := mcp.NewServer(&mcp.Implementation{Name: "ai-buddy-git", Version: "0.1.0"}, nil)
	mcp.AddTool(tools, &mcp.Tool{Name: "get_pull_request", Description: "Read an allowed pull request and its current diff"},
		func(ctx context.Context, _ *mcp.CallToolRequest, input prArguments) (*mcp.CallToolResult, PullRequest, error) {
			revision, err := server.read(ctx, actor, input.Number)
			return nil, revision.PR, toolError(err)
		})
	mcp.AddTool(tools, &mcp.Tool{Name: "get_pull_request_checks", Description: "Read CI status for a specific pull request version"},
		func(ctx context.Context, _ *mcp.CallToolRequest, input checkArguments) (*mcp.CallToolResult, Checks, error) {
			revision, err := server.read(ctx, actor, input.Number)
			if err == nil && revision.PR.HeadSHA != input.HeadSHA {
				err = Problem("STALE_HEAD", "Версия PR изменилась.")
			}
			return nil, revision.Checks, toolError(err)
		})
	mcp.AddTool(tools, &mcp.Tool{Name: "request_pull_request_review", Description: "Register a confirmed review request for the configured mentor"},
		func(ctx context.Context, _ *mcp.CallToolRequest, input reviewArguments) (*mcp.CallToolResult, ReviewRequest, error) {
			receipt, err := server.requestReview(ctx, actor, input)
			return nil, receipt, toolError(err)
		})
	return tools
}

func (server *gitServer) revision() (gitRevision, error) {
	data, err := os.ReadFile(server.fixturePath)
	if err != nil {
		return gitRevision{}, fmt.Errorf("read PR fixture: %w", err)
	}
	var fixture gitFixture
	if err := json.Unmarshal(data, &fixture); err != nil {
		return gitRevision{}, fmt.Errorf("decode PR fixture: %w", err)
	}
	revision, ok := fixture.Revisions[fixture.CurrentRevision]
	if !ok || revision.PR.Number != 17 || revision.PR.Repository != "e2-engineers" || revision.PR.HeadSHA == "" || revision.PR.Diff == "" || revision.PR.OwnerID != "intern-1" {
		return gitRevision{}, errors.New("invalid current PR fixture")
	}
	switch revision.Checks.Status {
	case "PASSED", "FAILED", "RUNNING", "UNKNOWN":
	default:
		return gitRevision{}, errors.New("invalid CI status in PR fixture")
	}
	return revision, nil
}

func (server *gitServer) read(ctx context.Context, actor string, number int) (gitRevision, error) {
	if err := ctx.Err(); err != nil {
		return gitRevision{}, err
	}
	if actor != "intern-1" || number != 17 {
		return gitRevision{}, Problem("NOT_FOUND", "PR недоступен.")
	}
	return server.revision()
}

func (server *gitServer) requestReview(ctx context.Context, actor string, input reviewArguments) (ReviewRequest, error) {
	if err := ctx.Err(); err != nil {
		return ReviewRequest{}, err
	}
	if actor != "intern-1" || input.Number != 17 {
		return ReviewRequest{}, Problem("NOT_FOUND", "PR недоступен.")
	}
	if input.ActionKey == "" || len(input.ActionKey) > 128 || input.ExpectedHeadSHA == "" {
		return ReviewRequest{}, Problem("INVALID_INPUT", "Не заданы параметры подтверждения.")
	}
	if input.MentorID != "mentor-backend" {
		return ReviewRequest{}, Problem("INVALID_INPUT", "Наставник изменился. Подтвердите отправку заново.")
	}

	server.mu.Lock()
	defer server.mu.Unlock()
	key := actor + ":" + input.ActionKey
	fingerprint := fmt.Sprintf("%d:%s:%s", input.Number, input.ExpectedHeadSHA, input.MentorID)
	if action, exists := server.state.Actions[key]; exists {
		if action.Fingerprint != fingerprint {
			return ReviewRequest{}, Problem("IDEMPOTENCY_CONFLICT", "Ключ уже использован для другого действия.")
		}
		return action.Receipt, nil
	}
	revision, err := server.read(ctx, actor, input.Number)
	if err != nil {
		return ReviewRequest{}, err
	}
	if revision.PR.HeadSHA != input.ExpectedHeadSHA {
		return ReviewRequest{}, Problem("STALE_HEAD", "Версия PR изменилась.")
	}
	businessKey := fmt.Sprintf("%s:%d:%s", actor, input.Number, input.MentorID)
	receipt, exists := server.state.Reviews[businessKey]
	if !exists {
		var random [8]byte
		if _, err := rand.Read(random[:]); err != nil {
			return ReviewRequest{}, fmt.Errorf("generate review ID: %w", err)
		}
		receipt = ReviewRequest{
			ID: "RR-" + hex.EncodeToString(random[:]), Status: "REQUESTED",
			Mentor:   Mentor{ID: input.MentorID, Name: "Алексей, Backend"},
			PRNumber: input.Number, HeadSHA: input.ExpectedHeadSHA,
		}
	}
	updated := gitState{Actions: maps.Clone(server.state.Actions), Reviews: maps.Clone(server.state.Reviews)}
	updated.Actions[key] = gitAction{Fingerprint: fingerprint, Receipt: receipt}
	updated.Reviews[businessKey] = receipt
	if err := writeState(server.statePath, updated); err != nil {
		return ReviewRequest{}, err
	}
	server.state = updated
	return receipt, nil
}

func toolError(err error) error {
	if err == nil {
		return nil
	}
	var problem *AppError
	if !errors.As(err, &problem) {
		problem = &AppError{Code: "DEPENDENCY_UNAVAILABLE", Message: "Git-сервис временно недоступен.", Retryable: true}
	}
	data, _ := json.Marshal(problem)
	return errors.New(string(data))
}

type mcpGit struct {
	endpoint string
	secret   string
}

func NewMCPGit(endpoint, secret string) GitTools {
	return &mcpGit{endpoint: endpoint, secret: secret}
}

func (git *mcpGit) GetPullRequest(ctx context.Context, actor string, number int) (PullRequest, error) {
	var output PullRequest
	err := git.call(ctx, actor, "get_pull_request", prArguments{Number: number}, &output)
	return output, err
}

func (git *mcpGit) GetChecks(ctx context.Context, actor string, number int, sha string) (Checks, error) {
	var output Checks
	err := git.call(ctx, actor, "get_pull_request_checks", checkArguments{Number: number, HeadSHA: sha}, &output)
	return output, err
}

func (git *mcpGit) RequestReview(ctx context.Context, actor string, number int, sha string, mentor Mentor, key string) (ReviewRequest, error) {
	var output ReviewRequest
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

func (git *mcpGit) call(ctx context.Context, actor, name string, arguments, output any) error {
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
		return &AppError{Code: "DEPENDENCY_UNAVAILABLE", Message: "Git-сервис временно недоступен.", Retryable: true}
	}
	defer session.Close()
	result, err := session.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: arguments})
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return &AppError{Code: "DEPENDENCY_UNAVAILABLE", Message: "Не удалось выполнить Git-операцию.", Retryable: true}
	}
	if result.IsError {
		for _, content := range result.Content {
			text, ok := content.(*mcp.TextContent)
			if !ok {
				continue
			}
			var problem AppError
			if json.Unmarshal([]byte(text.Text), &problem) == nil && problem.Code != "" {
				return &problem
			}
		}
		return &AppError{Code: "DEPENDENCY_UNAVAILABLE", Message: "Git-сервис отклонил операцию.", Retryable: true}
	}
	data, err := json.Marshal(result.StructuredContent)
	if err != nil {
		return fmt.Errorf("encode MCP result: %w", err)
	}
	if string(data) == "null" {
		return &AppError{Code: "DEPENDENCY_UNAVAILABLE", Message: "Git-сервис вернул пустой ответ.", Retryable: true}
	}
	if err := json.Unmarshal(data, output); err != nil {
		return fmt.Errorf("decode MCP result: %w", err)
	}
	return nil
}
