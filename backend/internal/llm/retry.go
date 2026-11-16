package llm

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/wantemira/e2-engineers/backend/internal/domain"
)

const maxAttempts = 3

type retryAfterError struct {
	err   error
	delay time.Duration
}

func (err *retryAfterError) Error() string { return err.err.Error() }
func (err *retryAfterError) Unwrap() error { return err.err }

// choose retries generation, never an executed tool or corporate action.
func (client *Client) choose(ctx context.Context, policy string, input any, tools []Tool) (toolCall, error) {
	budget, cancel := context.WithTimeout(ctx, min(time.Duration(maxAttempts)*client.config.Timeout+3*client.retryDelay, 90*time.Second))
	defer cancel()
	formatRepaired := false
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		started := time.Now()
		attemptContext, stop := context.WithTimeout(budget, client.config.Timeout)
		call, err := client.chooseOnce(attemptContext, policy, input, tools)
		if attemptContext.Err() == context.DeadlineExceeded || errors.Is(err, context.DeadlineExceeded) {
			err = failure("LLM_TIMEOUT", true)
		}
		stop()
		if ctx.Err() != nil {
			return toolCall{}, ctx.Err()
		}
		if budget.Err() != nil {
			return toolCall{}, failure("LLM_TIMEOUT", true)
		}
		if err == nil {
			slog.Info("LLM response accepted", "attempt", attempt, "duration_ms", time.Since(started).Milliseconds())
			return call, nil
		}
		var problem *domain.AppError
		if !errors.As(err, &problem) {
			return toolCall{}, err
		}
		retry := problem.Code == "LLM_TIMEOUT" || problem.Code == "LLM_UNAVAILABLE"
		if problem.Code == "LLM_INVALID_RESULT" && (problem.Reason == "tool_required" || problem.Reason == "tool_call_count") && !formatRepaired {
			formatRepaired = true
			retry = true
			policy += "\nИсправь формат: вызови ровно одну из предоставленных функций с аргументами по её JSON Schema. Не отвечай обычным текстом, не описывай вызов в Markdown и не добавляй новых функций."
		}
		if !retry || attempt == maxAttempts {
			return toolCall{}, err
		}
		delay := time.Duration(attempt) * client.retryDelay
		var hint *retryAfterError
		if errors.As(err, &hint) {
			delay = max(delay, hint.delay)
		}
		if deadline, ok := budget.Deadline(); ok && time.Until(deadline) <= delay {
			return toolCall{}, err
		}
		slog.Info("retrying LLM generation", "attempt", attempt, "code", problem.Code, "reason", problem.Reason, "duration_ms", time.Since(started).Milliseconds())
		timer := time.NewTimer(delay)
		select {
		case <-budget.Done():
			timer.Stop()
			if ctx.Err() != nil {
				return toolCall{}, ctx.Err()
			}
			return toolCall{}, failure("LLM_TIMEOUT", true)
		case <-timer.C:
		}
	}
	return toolCall{}, failure("LLM_TIMEOUT", true)
}
