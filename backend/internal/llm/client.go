// Package llm implements a bounded OpenAI-compatible Chat Completions adapter.
package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/wantemira/e2-engineers/backend/internal/domain"
)

type Config struct {
	BaseURL string
	Model   string
	APIKey  string
	Timeout time.Duration
}

type Client struct {
	config Config
	http   *http.Client
}

func New(config Config) (*Client, error) {
	config.BaseURL = strings.TrimRight(config.BaseURL, "/")
	u, err := url.Parse(config.BaseURL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return nil, fmt.Errorf("LLM_BASE_URL must be an absolute HTTP(S) API base URL without credentials, query or fragment")
	}
	if strings.TrimSpace(config.Model) == "" || strings.TrimSpace(config.APIKey) == "" || strings.ContainsAny(config.APIKey, "\r\n") {
		return nil, fmt.Errorf("live mode requires LLM_MODEL and LLM_API_KEY")
	}
	if config.Timeout == 0 {
		config.Timeout = 20 * time.Second
	}
	if config.Timeout < time.Second || config.Timeout > 25*time.Second {
		return nil, fmt.Errorf("LLM_TIMEOUT must be between 1s and 25s")
	}
	return &Client{config: config, http: &http.Client{
		Timeout: config.Timeout,
		// Do not forward prompts or credentials to a redirect destination.
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}}, nil
}

type Tool struct {
	Type     string   `json:"type"`
	Function Function `json:"function"`
}

type Function struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Parameters  map[string]any `json:"parameters"`
	Strict      bool           `json:"strict"`
}

func tool(name, description string, properties map[string]any) Tool {
	return Tool{Type: "function", Function: Function{Name: name, Description: description, Parameters: object(properties), Strict: true}}
}

func object(properties map[string]any) map[string]any {
	required := make([]string, 0, len(properties))
	for name := range properties {
		required = append(required, name)
	}
	sort.Strings(required)
	return map[string]any{"type": "object", "properties": properties, "required": required, "additionalProperties": false}
}

func str() map[string]any { return map[string]any{"type": "string"} }

type toolCall struct {
	ID       string `json:"id"`
	Type     string `json:"type"`
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
}

type completion struct {
	Choices []struct {
		FinishReason string `json:"finish_reason"`
		Message      struct {
			ToolCalls []toolCall `json:"tool_calls"`
		} `json:"message"`
	} `json:"choices"`
}

func failure(code string, retryable bool) error {
	return &domain.AppError{Code: code, Message: map[string]string{
		"LLM_UNAVAILABLE":    "Модель временно недоступна. Повторите запрос.",
		"LLM_TIMEOUT":        "Модель не успела ответить. Повторите запрос.",
		"LLM_CONFIGURATION":  "Проверьте настройки модели и ключ на сервере.",
		"LLM_INVALID_RESULT": "Модель вернула результат, который не прошёл проверку. Повторите запрос.",
	}[code], Retryable: retryable}
}

// choose accepts exactly one declared function call. It never executes model text.
func (client *Client) choose(ctx context.Context, policy string, input any, tools []Tool) (toolCall, error) {
	var empty toolCall
	data, err := json.Marshal(input)
	if err != nil {
		return empty, err
	}
	payload, err := json.Marshal(map[string]any{
		"model":    client.config.Model,
		"messages": []map[string]string{{"role": "system", "content": policy}, {"role": "user", "content": string(data)}},
		"tools":    tools, "tool_choice": "required", "parallel_tool_calls": false,
		"max_completion_tokens": 4096,
	})
	if err != nil {
		return empty, err
	}
	if len(payload) > 512*1024 {
		return empty, failure("LLM_INVALID_RESULT", false)
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, client.config.BaseURL+"/chat/completions", bytes.NewReader(payload))
	if err != nil {
		return empty, failure("LLM_CONFIGURATION", false)
	}
	request.Header.Set("Authorization", "Bearer "+client.config.APIKey)
	request.Header.Set("Content-Type", "application/json")
	response, err := client.http.Do(request)
	if err != nil {
		if ctx.Err() != nil {
			return empty, ctx.Err()
		}
		if timeout, ok := err.(interface{ Timeout() bool }); ok && timeout.Timeout() {
			return empty, failure("LLM_TIMEOUT", true)
		}
		return empty, failure("LLM_UNAVAILABLE", true)
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		// Provider bodies may echo credentials or prompts: never expose or log them.
		if response.StatusCode == 429 || response.StatusCode == 408 || response.StatusCode >= 500 {
			return empty, failure("LLM_UNAVAILABLE", true)
		}
		return empty, failure("LLM_CONFIGURATION", false)
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, 512*1024+1))
	if err != nil {
		return empty, failure("LLM_UNAVAILABLE", true)
	}
	var result completion
	if len(body) > 512*1024 || json.Unmarshal(body, &result) != nil || len(result.Choices) != 1 || result.Choices[0].FinishReason != "tool_calls" || len(result.Choices[0].Message.ToolCalls) != 1 {
		return empty, failure("LLM_INVALID_RESULT", true)
	}
	call := result.Choices[0].Message.ToolCalls[0]
	if call.Type != "function" || call.ID == "" {
		return empty, failure("LLM_INVALID_RESULT", true)
	}
	for _, allowed := range tools {
		if call.Function.Name == allowed.Function.Name {
			return call, nil
		}
	}
	return empty, failure("LLM_INVALID_RESULT", true)
}

func decode(data string, output any) error {
	decoder := json.NewDecoder(strings.NewReader(data))
	decoder.DisallowUnknownFields()
	if decoder.Decode(output) != nil || decoder.Decode(new(any)) != io.EOF {
		return failure("LLM_INVALID_RESULT", true)
	}
	return nil
}
