// Package llm implements a bounded OpenAI-compatible Chat Completions adapter.
package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
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
	if config.Timeout < time.Second || config.Timeout > 45*time.Second {
		return nil, fmt.Errorf("LLM_TIMEOUT must be between 1s and 45s")
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

// reason is an application constant, never provider content or a parsing error.
func invalidResult(reason string) error {
	slog.Warn("LLM result rejected", "reason", reason)
	return &domain.AppError{Code: "LLM_INVALID_RESULT", Message: "Модель вернула некорректный технический ответ. Повторите запрос.", Retryable: true, Reason: reason}
}

func unavailable(reason string) error {
	problem := failure("LLM_UNAVAILABLE", true).(*domain.AppError)
	problem.Reason = reason
	slog.Warn("LLM provider unavailable", "reason", reason)
	return problem
}

// choose accepts exactly one declared function call. It never executes model text.
func (client *Client) choose(ctx context.Context, policy string, input any, tools []Tool) (toolCall, error) {
	budget, cancel := context.WithTimeout(ctx, client.config.Timeout)
	defer cancel()
	call, err := client.chooseOnce(budget, policy, input, tools)
	var problem *domain.AppError
	if errors.As(err, &problem) && problem.Code == "LLM_INVALID_RESULT" && problem.Reason == "tool_required" && budget.Err() == nil {
		// No tool has executed yet. Retry the original input once; never promote
		// unvalidated model prose into an answer or replay an executed action.
		slog.Info("retrying LLM output format", "reason", problem.Reason)
		call, err = client.chooseOnce(budget, policy+"\nИсправь формат: предыдущая попытка не вернула вызов функции. Вызови ровно одну из предоставленных функций с аргументами по её JSON Schema. Не отвечай обычным текстом, не описывай вызов в Markdown и не добавляй новых функций.", input, tools)
	}
	if ctx.Err() != nil {
		return toolCall{}, ctx.Err()
	}
	if budget.Err() != nil {
		return toolCall{}, failure("LLM_TIMEOUT", true)
	}
	return call, err
}

func (client *Client) chooseOnce(ctx context.Context, policy string, input any, tools []Tool) (toolCall, error) {
	var empty toolCall
	data, err := json.Marshal(input)
	if err != nil {
		return empty, err
	}

	var choice any = "required"
	if len(tools) == 1 {
		choice = map[string]any{"type": "function", "function": map[string]string{"name": tools[0].Function.Name}}
	}
	parameters := map[string]any{
		"model":    client.config.Model,
		"messages": []map[string]string{{"role": "system", "content": policy}, {"role": "user", "content": string(data)}},
		"tools":    tools, "tool_choice": choice, "parallel_tool_calls": false, "max_completion_tokens": 8192,
	}

	payload, err := json.Marshal(parameters)
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
		return empty, unavailable("network")
	}
	defer response.Body.Close()

	if response.StatusCode < 200 || response.StatusCode >= 300 {
		// Status codes are safe to log; provider bodies may contain sensitive data.
		slog.Warn("LLM HTTP request rejected", "status", response.StatusCode)
		if response.StatusCode == 429 {
			return empty, unavailable("rate_limit")
		}
		if response.StatusCode == 408 || response.StatusCode >= 500 {
			return empty, unavailable("provider_error")
		}
		return empty, failure("LLM_CONFIGURATION", false)
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, 512*1024+1))
	if err != nil {
		return empty, unavailable("response_read")
	}

	var result completion
	if len(body) > 512*1024 {
		return empty, invalidResult("response_too_large")
	}
	if json.Unmarshal(body, &result) != nil {
		return empty, invalidResult("response_json")
	}
	if len(result.Choices) != 1 {
		return empty, invalidResult("response_shape")
	}
	if result.Choices[0].FinishReason == "length" {
		return empty, invalidResult("truncated")
	}
	if result.Choices[0].FinishReason != "tool_calls" {
		return empty, invalidResult("tool_required")
	}
	if len(result.Choices[0].Message.ToolCalls) != 1 {
		return empty, invalidResult("tool_call_count")
	}
	call := result.Choices[0].Message.ToolCalls[0]
	if call.Type != "function" || call.ID == "" {
		return empty, invalidResult("tool_call_shape")
	}
	for _, allowed := range tools {
		if call.Function.Name == allowed.Function.Name {
			return call, nil
		}
	}
	return empty, invalidResult("tool_not_allowed")
}

func decode(data string, output any) error {
	decoder := json.NewDecoder(strings.NewReader(data))
	decoder.DisallowUnknownFields()
	if decoder.Decode(output) != nil || decoder.Decode(new(any)) != io.EOF {
		return invalidResult("arguments_json")
	}
	return nil
}
