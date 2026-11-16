package llm

import (
	"context"
	"encoding/json"
	"net/http"
	"sync/atomic"
	"testing"
	"time"

	"github.com/wantemira/e2-engineers/backend/internal/domain"
)

func TestTransientGenerationRecoversWithoutReplayingTools(t *testing.T) {
	for _, status := range []int{429, 503} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			var calls atomic.Int32
			client := testClient(t, func(w http.ResponseWriter, r *http.Request) {
				if calls.Add(1) < 3 {
					w.WriteHeader(status)
					return
				}
				writeCall(w, "answer_question", `{"text":"Готовый ответ","document_ids":[],"mentor_draft":""}`)
			})
			decision, err := client.Plan(context.Background(), domain.ChatInput{Text: "Привет"})
			if err != nil || decision.Tool != "answer_question" || calls.Load() != 3 {
				t.Fatalf("recovery: %+v, %v, calls %d", decision, err, calls.Load())
			}
		})
	}
}

func TestResponseBodyTimeoutIsRetried(t *testing.T) {
	var calls atomic.Int32
	client := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			w.(http.Flusher).Flush()
			<-r.Context().Done()
			return
		}
		writeCall(w, "answer_question", `{"text":"Ответ после задержки","document_ids":[],"mentor_draft":""}`)
	})
	client.config.Timeout = 40 * time.Millisecond
	client.http.Timeout = time.Second
	decision, err := client.Plan(context.Background(), domain.ChatInput{})
	if err != nil || decision.Message.Text != "Ответ после задержки" || calls.Load() != 2 {
		t.Fatalf("body timeout recovery: %+v, %v, calls %d", decision, err, calls.Load())
	}
}

func TestRetryRespectsCancellationAndLongRetryAfter(t *testing.T) {
	for _, scenario := range []string{"cancel", "retry-after"} {
		t.Run(scenario, func(t *testing.T) {
			var calls atomic.Int32
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			client := testClient(t, func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				if scenario == "cancel" {
					cancel()
				} else {
					w.Header().Set("Retry-After", "86401")
				}
				w.WriteHeader(http.StatusTooManyRequests)
			})
			client.retryDelay = time.Hour
			_, err := client.Plan(ctx, domain.ChatInput{})
			if calls.Load() != 1 {
				t.Fatalf("retry ignored cancellation/delay: %d", calls.Load())
			}
			if scenario == "cancel" && err != context.Canceled {
				t.Fatalf("lost cancellation: %v", err)
			}
			if scenario == "retry-after" && domain.PublicError(err).Reason != "rate_limit" {
				t.Fatalf("lost retry-after error: %v", err)
			}
		})
	}
}

func TestRetryAfterDelayAndContextBound(t *testing.T) {
	var calls atomic.Int32
	client := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Header().Set("Retry-After", "10")
		w.WriteHeader(http.StatusTooManyRequests)
	})
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	_, err := client.Plan(ctx, domain.ChatInput{})
	if calls.Load() != 1 || err == nil {
		t.Fatalf("rate limit retried too early: %d, %v", calls.Load(), err)
	}
}

func TestGenerationTokenBudgetMatchesTask(t *testing.T) {
	for _, name := range []string{"answer_question", "submit_review"} {
		t.Run(name, func(t *testing.T) {
			client := testClient(t, func(w http.ResponseWriter, r *http.Request) {
				var body struct {
					Tokens int `json:"max_completion_tokens"`
				}
				json.NewDecoder(r.Body).Decode(&body)
				want := 4096
				if name == "submit_review" {
					want = 8192
				}
				if body.Tokens != want {
					t.Errorf("token budget: %d, want %d", body.Tokens, want)
				}
				writeCall(w, name, `{}`)
			})
			if _, err := client.choose(context.Background(), "Policy", map[string]string{}, []Tool{tool(name, "Test", map[string]any{})}); err != nil {
				t.Fatal(err)
			}
		})
	}
}
