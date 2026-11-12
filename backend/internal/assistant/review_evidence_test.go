package assistant

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/wantemira/e2-engineers/backend/internal/domain"
)

func TestReviewReportsTeamRulesAndTaskEvidence(t *testing.T) {
	patch := `diff --git a/service.go b/service.go
--- a/service.go
+++ b/service.go
@@ -1,3 +1,10 @@
 func Create(ctx context.Context) error {
+    ctx, _ = context.WithCancel(ctx)
+    err := save(ctx)
+    if err != nil {
+        return nil
+    }
+    // TestCreate appears here as a comment only.
+    _ = "TestCreate"
     return nil
 }
`
	task := "Изменить `service.go` в `Create`. Нужно добавить тест `TestCreate`."
	analysis, err := NewLocalAssistant().Review(context.Background(), domain.PullRequest{Diff: patch, Task: task}, domain.Checks{Status: "UNKNOWN"}, loadTestTeam(t))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []struct {
		id, kind string
		line     int
	}{
		{"GO-FMT-01", "problem", 2},
		{"GO-CTX-01", "assumption", 2},
		{"GO-ERR-02", "assumption", 5},
		{"GO-TEST-01", "assumption", 2},
	} {
		index := slices.IndexFunc(analysis.Findings, func(finding domain.Finding) bool {
			return finding.Rule != nil && finding.Rule.ID == want.id
		})
		if index < 0 {
			t.Errorf("rule %s missing in %+v", want.id, analysis.Findings)
			continue
		}
		finding := analysis.Findings[index]
		if finding.Kind != want.kind || finding.Location == nil || finding.Location.Line != want.line {
			t.Errorf("unexpected finding for %s: %+v", want.id, finding)
		}
	}
	for _, want := range []string{"service.go, Create", "не найдены: TestCreate", "Задача требует теста"} {
		if !strings.Contains(analysis.Assessment.Task, want) {
			t.Errorf("task assessment lacks %q: %s", want, analysis.Assessment.Task)
		}
	}
	if !strings.Contains(analysis.Assessment.Scope, "Сборку и тесты PR агент не запускал") {
		t.Fatalf("scope must explain the checks that were not run: %s", analysis.Assessment.Scope)
	}
}

func TestReviewReportsLimitsOfTaskEvidence(t *testing.T) {
	for _, tc := range []struct {
		task     string
		diff     string
		expected string
	}{
		{"", "", "Описание задачи отсутствует"},
		{"Улучшить взаимодействие компонентов", "", "не смог выделить критерии"},
		{"Сохранить причину ошибки.", "", "не найдено проверяемого подтверждения"},
		{"Сохранить причину ошибки.", testDiff, "заменяет исходную ошибку"},
		{"Сохранить причину ошибки.", strings.Replace(testDiff, `errors.New("save failed")`, `fmt.Errorf("save: %w", err)`, 1), "видно оборачивание"},
		{"Сохранить причину ошибки.", strings.Replace(testDiff, `errors.New("save failed")`, `fmt.Errorf("save: %%w", err)`, 1), "заменяет исходную ошибку"},
		{"Добавить тест.", addedTest, "есть изменения в *_test.go"},
	} {
		analysis, err := NewLocalAssistant().Review(context.Background(), domain.PullRequest{Diff: tc.diff, Task: tc.task}, domain.Checks{}, loadTestTeam(t))
		if err != nil || !strings.Contains(analysis.Assessment.Task, tc.expected) {
			t.Errorf("expected %q in assessment of %q, got %+v, %v", tc.expected, tc.task, analysis, err)
		}
		if tc.diff == "" && !strings.Contains(analysis.Assessment.Scope, "нет добавленных") {
			t.Errorf("scope must report missing changes: %s", analysis.Assessment.Scope)
		}
	}
}

func TestReviewDistinguishesErrorBranchFromElseAndLiterals(t *testing.T) {
	patch := "diff --git a/service.go b/service.go\n" +
		"--- a/service.go\n+++ b/service.go\n@@ -1,4 +1,12 @@\n" +
		" func Save(err error) error {\n" +
		" \tif err != nil {\n" +
		"+\t\t_ = \"}\"\n" +
		"+\t\t_ = `sample\n+return nil\n+`\n" +
		"+\t\treturn errors.New(\"save failed\")\n" +
		"+\t} else {\n" +
		"+\t\treturn nil\n" +
		" \t}\n }\n" + addedTest
	analysis, err := NewLocalAssistant().Review(context.Background(), domain.PullRequest{Diff: patch}, domain.Checks{}, loadTestTeam(t))
	if err != nil {
		t.Fatal(err)
	}
	lostCause := 0
	for _, finding := range analysis.Findings {
		if finding.Rule.ID == "GO-ERR-02" {
			t.Errorf("unexpected nil-return finding: %+v", finding)
		}
		if finding.Rule.ID == "GO-ERR-01" {
			lostCause++
			if finding.Location.Line != 7 {
				t.Errorf("lost-cause finding points to line %d, want 7", finding.Location.Line)
			}
		}
	}
	if lostCause != 1 {
		t.Fatalf("expected only the real error return to lose the cause: %+v", analysis.Findings)
	}
}
