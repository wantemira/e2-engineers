package domain

import "testing"

func TestValidateVersionedChecks(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*Checks)
		valid  bool
	}{
		{"valid", func(*Checks) {}, true},
		{"wrong SHA", func(c *Checks) { c.HeadSHA = "other" }, false},
		{"wrong PR", func(c *Checks) { c.PRNumber = 18 }, false},
		{"wrong source", func(c *Checks) { c.Source = "REAL" }, false},
		{"missing run", func(c *Checks) { c.RunID = "" }, false},
		{"contradictory pass", func(c *Checks) { c.Status = "PASSED" }, false},
		{"wrong test status", func(c *Checks) { c.Tests[0].Status = "GREEN" }, false},
		{"invalid timestamp", func(c *Checks) { c.CreatedAt = "yesterday" }, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			checks := Checks{Status: "FAILED", Label: "Failed", PRNumber: 17, HeadSHA: "sha", RunID: "run", Source: "MOCK", Tests: []TestResult{{Name: "TestError", Status: "FAILED", Error: "expected error"}}}
			tc.change(&checks)
			if err := ValidateChecks(checks, 17, "sha"); (err == nil) != tc.valid {
				t.Fatalf("unexpected validation: %v", err)
			}
		})
	}
	if err := ValidateChecks(Checks{Status: "UNKNOWN", Label: "Historical report"}, 17, "sha"); err != nil {
		t.Fatalf("legacy report rejected: %v", err)
	}
}
