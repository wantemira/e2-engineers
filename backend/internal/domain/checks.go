package domain

import (
	"fmt"
	"slices"
	"strings"
	"time"
)

type TestResult struct {
	Name   string `json:"name"`
	Status string `json:"status"`
	Error  string `json:"error,omitempty"`
}

// ValidateChecks accepts historical status/label reports, but binds rich results to a PR/SHA.
func ValidateChecks(checks Checks, number int, sha string) error {
	if !slices.Contains([]string{"PASSED", "FAILED", "RUNNING", "UNKNOWN"}, checks.Status) || strings.TrimSpace(checks.Label) == "" || len(checks.Label) > 2000 || len(checks.Logs) > 16000 || len(checks.Tests) > 100 {
		return fmt.Errorf("invalid CI result")
	}
	rich := checks.RunID != "" || checks.HeadSHA != "" || checks.PRNumber != 0 || checks.Source != "" || checks.Tests != nil || checks.Logs != "" || checks.CreatedAt != ""
	if rich && (checks.PRNumber != number || checks.HeadSHA != sha || checks.RunID == "" || len(checks.RunID) > 128 || checks.Source != "MOCK") {
		return fmt.Errorf("CI result does not match requested PR/SHA or source")
	}
	if checks.CreatedAt != "" {
		if _, err := time.Parse(time.RFC3339Nano, checks.CreatedAt); err != nil {
			return fmt.Errorf("invalid CI timestamp")
		}
	}
	names := map[string]bool{}
	failed := false
	for _, test := range checks.Tests {
		if strings.TrimSpace(test.Name) == "" || len(test.Name) > 200 || names[test.Name] || len(test.Error) > 4000 || !slices.Contains([]string{"PASSED", "FAILED", "RUNNING", "SKIPPED"}, test.Status) {
			return fmt.Errorf("invalid CI test")
		}
		names[test.Name] = true
		failed = failed || test.Status == "FAILED"
		if test.Status != "FAILED" && test.Error != "" {
			return fmt.Errorf("non-failing test contains an error")
		}
		if checks.Status == "PASSED" && (test.Status == "FAILED" || test.Status == "RUNNING") {
			return fmt.Errorf("passed CI contains unfinished or failed tests")
		}
	}
	if checks.Status == "FAILED" && len(checks.Tests) > 0 && !failed {
		return fmt.Errorf("failed CI has no failed test")
	}
	return nil
}
