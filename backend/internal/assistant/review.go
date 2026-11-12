package assistant

import (
	"context"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/wantemira/e2-engineers/backend/internal/diff"
	"github.com/wantemira/e2-engineers/backend/internal/domain"
)

var errorBranch = regexp.MustCompile(`\bif\s+err\s*!=\s*nil\s*\{`)
var returnedError = regexp.MustCompile(`\breturn\b.*(?:errors\.New|fmt\.Errorf)\(\s*"([^"\\]*(?:\\.[^"\\]*)*)"`)

func (*LocalAssistant) Review(ctx context.Context, pr domain.PullRequest, _ domain.Checks, team domain.Team) ([]domain.Finding, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if len(pr.Diff) > 200*1024 {
		return nil, domain.Problem("DIFF_TOO_LARGE", "Изменений слишком много для одной проверки.")
	}
	lines, err := diff.Parse(pr.Diff)
	if err != nil {
		return nil, fmt.Errorf("parse PR diff: %w", err)
	}
	findings := make([]domain.Finding, 0)
	var goChange *diff.Line
	hasTestChange := false
	depth, branchDepth := 0, -1
	var currentPath string
	var currentHunk int
	for index := range lines {
		line := &lines[index]
		if line.Path != currentPath || line.Hunk != currentHunk {
			currentPath, depth, branchDepth = line.Path, 0, -1
			currentHunk = line.Hunk
		}
		if !strings.HasSuffix(line.Path, ".go") {
			continue
		}
		text := strings.TrimSpace(line.Text)
		isComment := strings.HasPrefix(text, "//") || strings.HasPrefix(text, "/*") || strings.HasPrefix(text, "*")
		if line.Added && text != "" && !isComment {
			if strings.HasSuffix(line.Path, "_test.go") {
				hasTestChange = true
			} else if goChange == nil {
				goChange = line
			}
		}
		if isComment || strings.HasSuffix(line.Path, "_test.go") {
			continue
		}
		if errorBranch.MatchString(text) {
			branchDepth = depth
		}
		if line.Added && branchDepth >= 0 {
			match := returnedError.FindStringSubmatch(text)
			if match != nil {
				format, unquoteErr := strconv.Unquote(`"` + match[1] + `"`)
				if unquoteErr == nil && !strings.Contains(format, "%w") {
					rule := team.Rules["GO-ERR-01"]
					findings = append(findings, domain.Finding{
						ID: fmt.Sprintf("F%d", len(findings)+1), Kind: "problem", Title: "Теряется причина ошибки",
						Description: "В ветке err != nil возвращается новая ошибка без исходной причины. Вызывающий код потеряет возможность проверить её через errors.Is или errors.As. Добавьте контекст через fmt.Errorf(\"...: %w\", err), чтобы сохранить цепочку ошибок.",
						Location:    &domain.Location{Path: line.Path, Line: line.Number}, Rule: &rule,
					})
				}
			}
		}
		depth += strings.Count(text, "{") - strings.Count(text, "}")
		if depth <= branchDepth {
			branchDepth = -1
		}
	}
	if goChange != nil && !hasTestChange {
		rule := team.Rules["GO-TEST-01"]
		findings = append(findings, domain.Finding{
			ID: fmt.Sprintf("F%d", len(findings)+1), Kind: "assumption", Title: "Проверьте тесты изменённой логики",
			Description: "В доступном diff изменён Go-код, а изменений в файлах *_test.go не видно. Это не доказывает отсутствие тестов в репозитории. Проверьте существующие тесты и добавьте кейс для новой или изменённой ветки, если она ещё не покрыта.",
			Location:    &domain.Location{Path: goChange.Path, Line: goChange.Number}, Rule: &rule,
		})
	}
	return findings, nil
}
