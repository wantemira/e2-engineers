package assistant

import (
	"context"
	"fmt"
	"go/token"
	"regexp"
	"strconv"
	"strings"

	"github.com/wantemira/e2-engineers/backend/internal/diff"
	"github.com/wantemira/e2-engineers/backend/internal/domain"
)

var errorBranch = regexp.MustCompile(`^if\s+err\s*!=\s*nil\s*\{`)
var returnedError = regexp.MustCompile(`^return\s+(?:[^,;]+,\s*)?(errors\.New|fmt\.Errorf)\(\s*"([^"\\]*(?:\\.[^"\\]*)*)"`)
var wrapVerb = regexp.MustCompile(`%(?:\[\d+\])?w`)
var returnedNil = regexp.MustCompile(`^return(?:\s+[^,;]+,)?\s+nil\s*(?://.*)?$`)

func (*LocalAssistant) Review(ctx context.Context, pr domain.PullRequest, _ domain.Checks, team domain.Team) (domain.ReviewAnalysis, error) {
	if err := ctx.Err(); err != nil {
		return domain.ReviewAnalysis{}, err
	}
	if len(pr.Diff) > 200*1024 {
		return domain.ReviewAnalysis{}, domain.Problem("DIFF_TOO_LARGE", "Изменений слишком много для одной проверки.")
	}
	lines, err := diff.Parse(pr.Diff)
	if err != nil {
		return domain.ReviewAnalysis{}, fmt.Errorf("parse PR diff: %w", err)
	}
	findings := make([]domain.Finding, 0)
	add := func(kind, title, description, ruleID string, line diff.Line) {
		finding := domain.Finding{
			ID: fmt.Sprintf("F%d", len(findings)+1), Kind: kind, Title: title,
			Description: description,
			Location:    &domain.Location{Path: line.Path, Line: line.Number},
		}
		if rule, ok := team.Rules[ruleID]; ok {
			finding.Rule = &rule
		}
		findings = append(findings, finding)
	}

	var goChange *diff.Line
	hasTestChange, visibleWrap, lostCause := false, false, false
	goLines, parsedHunks := 0, 0
	for start := 0; start < len(lines); {
		end := start + 1
		for end < len(lines) && lines[end].Hunk == lines[start].Hunk {
			end++
		}
		hunk := lines[start:end]
		start = end
		if err := ctx.Err(); err != nil {
			return domain.ReviewAnalysis{}, err
		}
		if !strings.HasSuffix(hunk[0].Path, ".go") {
			continue
		}

		isTest := strings.HasSuffix(hunk[0].Path, "_test.go")
		type lineSyntax struct {
			first  token.Token
			braces []token.Token
		}
		syntax := make([]lineSyntax, len(hunk))
		scanGoHunk(hunk, func(index int, tok token.Token, _ string) {
			row := &syntax[index]
			if row.first == token.ILLEGAL && tok != token.SEMICOLON {
				row.first = tok
			}
			if tok == token.LBRACE || tok == token.RBRACE {
				row.braces = append(row.braces, tok)
			}
		})
		depth, branchDepth := 0, -1
		for index := range hunk {
			line := &hunk[index]
			row := syntax[index]
			if row.first == token.ILLEGAL {
				continue
			}
			if line.Added {
				goLines++
				if isTest {
					hasTestChange = true
				} else if goChange == nil {
					goChange = line
				}
			}
			if isTest {
				continue
			}
			text := strings.TrimSpace(line.Text)
			if row.first == token.IF && branchDepth < 0 && errorBranch.MatchString(text) {
				branchDepth = depth
			}
			if line.Added && row.first == token.RETURN && branchDepth >= 0 {
				if match := returnedError.FindStringSubmatch(text); match != nil {
					format, unquoteErr := strconv.Unquote(`"` + match[2] + `"`)
					wraps := match[1] == "fmt.Errorf" && wrapVerb.MatchString(strings.ReplaceAll(format, "%%", ""))
					visibleWrap = visibleWrap || (unquoteErr == nil && wraps)
					if unquoteErr == nil && !wraps {
						lostCause = true
						add("problem", "Теряется причина ошибки",
							"В ветке err != nil возвращается новая ошибка без исходной причины. Вызывающий код потеряет возможность проверить её через errors.Is или errors.As. Добавьте контекст через fmt.Errorf(\"...: %w\", err), чтобы сохранить цепочку ошибок.",
							"GO-ERR-01", *line)
					}
				} else if returnedNil.MatchString(text) {
					add("assumption", "Проверьте возврат nil при ошибке",
						"В ветке err != nil возвращается nil. Если это позиция ошибки в результате, вызывающий код может принять сбой за успех. Верните исходную ошибку с контекстом или явно объясните, почему здесь допустим запасной результат.",
						"GO-ERR-02", *line)
				}
			}
			for _, brace := range row.braces {
				if brace == token.LBRACE {
					depth++
					continue
				}
				depth = max(0, depth-1)
				if depth <= branchDepth {
					branchDepth = -1
				}
			}
		}
		if inspectGoHunk(hunk, add) {
			parsedHunks++
		}
	}

	if goChange != nil && !hasTestChange {
		add("assumption", "Проверьте тесты изменённой логики",
			"В добавленных строках изменён Go-код. Добавленных строк в *_test.go не видно. Проверьте существующие тесты и добавьте кейс для изменённой ветки, если она ещё не покрыта.",
			"GO-TEST-01", *goChange)
	}

	scope := "В доступном diff нет добавленных непустых строк Go-кода для локальных проверок."
	if goLines > 0 {
		scope = fmt.Sprintf("Просмотрены добавленные строки Go-кода (%d без пустых строк и комментариев), видимые ветки обработки ошибок и изменения тестов.", goLines)
		if parsedHunks == 0 {
			scope += " Синтаксически полных Go-фрагментов для проверки gofmt и отмены контекста нет."
		} else {
			scope += fmt.Sprintf(" Для проверки gofmt и отмены контекста разобрано полных Go-фрагментов: %d.", parsedHunks)
		}
	}
	scope += " Сборку и тесты PR агент не запускал."
	return domain.ReviewAnalysis{
		Findings:   findings,
		Assessment: domain.ReviewAssessment{Scope: scope, Task: assessTask(pr.Task, lines, visibleWrap, lostCause, hasTestChange)},
	}, nil
}
