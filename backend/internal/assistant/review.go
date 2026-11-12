package assistant

import (
	"bufio"
	"context"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/wantemira/e2-engineers/backend/internal/domain"
)

type diffLine struct {
	path  string
	line  int
	text  string
	added bool
	hunk  int
}

var hunkHeader = regexp.MustCompile(`^@@ -\d+(?:,\d+)? \+(\d+)(?:,\d+)? @@`)
var errorBranch = regexp.MustCompile(`\bif\s+err\s*!=\s*nil\s*\{`)
var returnedError = regexp.MustCompile(`\breturn\b.*(?:errors\.New|fmt\.Errorf)\(\s*"([^"\\]*(?:\\.[^"\\]*)*)"`)

func parseDiff(diff string) ([]diffLine, error) {
	scanner := bufio.NewScanner(strings.NewReader(diff))
	scanner.Buffer(make([]byte, 4096), 256*1024)
	var result []diffLine
	var path string
	var lineNumber int
	var hunk int
	var inHunk bool
	for scanner.Scan() {
		line := scanner.Text()
		switch {
		case strings.HasPrefix(line, "diff --git "):
			path, inHunk = "", false
		case strings.HasPrefix(line, "+++ b/"):
			path = strings.TrimPrefix(line, "+++ b/")
		case strings.HasPrefix(line, "+++ /dev/null"):
			path, inHunk = "", false
		case strings.HasPrefix(line, "@@ "):
			match := hunkHeader.FindStringSubmatch(line)
			if match == nil {
				return nil, fmt.Errorf("invalid diff hunk")
			}
			lineNumber, _ = strconv.Atoi(match[1])
			hunk++
			inHunk = true
		case inHunk && path != "" && (strings.HasPrefix(line, "+") || strings.HasPrefix(line, " ")):
			result = append(result, diffLine{path: path, line: lineNumber, text: line[1:], added: line[0] == '+', hunk: hunk})
			lineNumber++
		}
	}
	return result, scanner.Err()
}

func (*LocalAssistant) Review(ctx context.Context, pr domain.PullRequest, _ domain.Checks, team domain.Team) ([]domain.Finding, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if len(pr.Diff) > 200*1024 {
		return nil, domain.Problem("DIFF_TOO_LARGE", "Изменений слишком много для одной проверки.")
	}
	lines, err := parseDiff(pr.Diff)
	if err != nil {
		return nil, fmt.Errorf("parse PR diff: %w", err)
	}
	findings := make([]domain.Finding, 0)
	var goChange *diffLine
	hasTestChange := false
	depth, branchDepth := 0, -1
	var currentPath string
	var currentHunk int
	for index := range lines {
		line := &lines[index]
		if line.path != currentPath || line.hunk != currentHunk {
			currentPath, depth, branchDepth = line.path, 0, -1
			currentHunk = line.hunk
		}
		if !strings.HasSuffix(line.path, ".go") {
			continue
		}
		text := strings.TrimSpace(line.text)
		isComment := strings.HasPrefix(text, "//") || strings.HasPrefix(text, "/*") || strings.HasPrefix(text, "*")
		if line.added && text != "" && !isComment {
			if strings.HasSuffix(line.path, "_test.go") {
				hasTestChange = true
			} else if goChange == nil {
				goChange = line
			}
		}
		if isComment || strings.HasSuffix(line.path, "_test.go") {
			continue
		}
		if errorBranch.MatchString(text) {
			branchDepth = depth
		}
		if line.added && branchDepth >= 0 {
			match := returnedError.FindStringSubmatch(text)
			if match != nil {
				format, unquoteErr := strconv.Unquote(`"` + match[1] + `"`)
				if unquoteErr == nil && !strings.Contains(format, "%w") {
					rule := team.Rules["GO-ERR-01"]
					findings = append(findings, domain.Finding{
						ID: fmt.Sprintf("F%d", len(findings)+1), Kind: "problem", Title: "Теряется причина ошибки",
						Description: "В ветке err != nil возвращается новая ошибка без исходной причины. Вызывающий код потеряет возможность проверить её через errors.Is или errors.As. Добавьте контекст через fmt.Errorf(\"...: %w\", err), чтобы сохранить цепочку ошибок.",
						Location:    &domain.Location{Path: line.path, Line: line.line}, Rule: &rule,
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
			Location:    &domain.Location{Path: goChange.path, Line: goChange.line}, Rule: &rule,
		})
	}
	return findings, nil
}
