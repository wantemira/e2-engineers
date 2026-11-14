package llm

import (
	"fmt"
	"strings"
	"time"
)

// FromEnvironment never infers live mode from a key and never silently falls back.
func FromEnvironment(getenv func(string) string) (*Client, error) {
	mode := strings.ToLower(strings.TrimSpace(getenv("BUDDY_MODE")))
	if mode == "" || mode == "demo" {
		return nil, nil
	}
	if mode != "live" {
		return nil, fmt.Errorf("BUDDY_MODE must be demo or live")
	}
	provider := getenv("LLM_PROVIDER")
	if provider != "" && provider != "openai-compatible" {
		return nil, fmt.Errorf("LLM_PROVIDER must be openai-compatible")
	}
	timeout := 20 * time.Second
	if value := getenv("LLM_TIMEOUT"); value != "" {
		var err error
		timeout, err = time.ParseDuration(value)
		if err != nil {
			return nil, fmt.Errorf("LLM_TIMEOUT must be a duration, e.g. 20s")
		}
	}
	return New(Config{BaseURL: getenv("LLM_BASE_URL"), Model: getenv("LLM_MODEL"), APIKey: getenv("LLM_API_KEY"), Timeout: timeout})
}
