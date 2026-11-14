// Package mcptransport provides authenticated, cancellable calls shared by tool clients.
package mcptransport

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/wantemira/e2-engineers/backend/internal/domain"
)

type Client struct{ endpoint, secret string }

func NewClient(endpoint, secret string) *Client {
	return &Client{endpoint: endpoint, secret: secret}
}

func Authenticate(secret string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if secret == "" || subtle.ConstantTimeCompare([]byte(r.Header.Get("Authorization")), []byte("Bearer "+secret)) != 1 || r.Header.Get("X-Buddy-Actor") == "" {
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
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

func (clientConnection *Client) Call(ctx context.Context, actor, name string, arguments, output any) error {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	client := mcp.NewClient(&mcp.Implementation{Name: "ai-buddy", Version: "0.1.0"}, nil)
	transport := &mcp.StreamableClientTransport{
		Endpoint:             clientConnection.endpoint,
		HTTPClient:           &http.Client{Transport: actorTransport{actor: actor, secret: clientConnection.secret}},
		DisableStandaloneSSE: true,
		MaxRetries:           -1,
	}
	session, err := client.Connect(ctx, transport, nil)
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return &domain.AppError{Code: "DEPENDENCY_UNAVAILABLE", Message: "Внутренний сервис временно недоступен.", Retryable: true}
	}
	defer session.Close()
	result, err := session.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: arguments})
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return &domain.AppError{Code: "DEPENDENCY_UNAVAILABLE", Message: "Не удалось вызвать внутренний инструмент.", Retryable: true}
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
		return &domain.AppError{Code: "DEPENDENCY_UNAVAILABLE", Message: "Внутренний инструмент отклонил вызов.", Retryable: true}
	}
	data, err := json.Marshal(result.StructuredContent)
	if err != nil {
		return fmt.Errorf("encode MCP result: %w", err)
	}
	if string(data) == "null" {
		return &domain.AppError{Code: "DEPENDENCY_UNAVAILABLE", Message: "Внутренний инструмент вернул пустой ответ.", Retryable: true}
	}
	if err := json.Unmarshal(data, output); err != nil {
		return fmt.Errorf("decode MCP result: %w", err)
	}
	return nil
}
