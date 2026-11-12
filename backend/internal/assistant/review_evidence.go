package assistant

import (
	"go/ast"
	"go/format"
	"go/parser"
	"go/scanner"
	"go/token"
	"regexp"
	"slices"
	"strings"

	"github.com/wantemira/e2-engineers/backend/internal/diff"
)

func inspectGoHunk(lines []diff.Line, add func(string, string, string, string, diff.Line)) bool {
	if !slices.ContainsFunc(lines, func(line diff.Line) bool { return line.Added }) {
		return false
	}

	source := hunkSource(lines)
	// Only format a complete file or a fragment parseable as top-level declarations.
	prefixLines := 0
	files := token.NewFileSet()
	file, err := parser.ParseFile(files, lines[0].Path, source, parser.SkipObjectResolution)
	if err != nil {
		source = "package review\n" + source
		prefixLines = 1
		file, err = parser.ParseFile(files, lines[0].Path, source, parser.SkipObjectResolution)
	}
	if err != nil || len(file.Decls) == 0 {
		return false
	}

	if formatted, err := format.Source([]byte(source)); err == nil {
		original := strings.Split(source, "\n")
		expected := strings.Split(string(formatted), "\n")
		// gofmt can insert a blank line after the synthetic package clause.
		if prefixLines == 1 && len(expected) > 1 && expected[1] == "" && original[1] != "" {
			expected = append(expected[:1], expected[2:]...)
		}
		if len(original) == len(expected) {
			for index, line := range lines {
				if line.Added && original[index+prefixLines] != expected[index+prefixLines] {
					add("problem", "Отформатируйте Go-код через gofmt",
						"Форматирование добавленной строки отличается от вывода gofmt для этого фрагмента. Запустите gofmt для файла перед ревью.",
						"GO-FMT-01", line)
					break
				}
			}
		}
	}

	ast.Inspect(file, func(node ast.Node) bool {
		statement, ok := node.(*ast.AssignStmt)
		if !ok || len(statement.Lhs) != 2 || len(statement.Rhs) != 1 {
			return true
		}
		discarded, ok := statement.Lhs[1].(*ast.Ident)
		call, isCall := statement.Rhs[0].(*ast.CallExpr)
		if !ok || discarded.Name != "_" || !isCall {
			return true
		}
		method, ok := call.Fun.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		pkg, ok := method.X.(*ast.Ident)
		if !ok || pkg.Name != "context" {
			return true
		}
		switch method.Sel.Name {
		case "WithCancel", "WithTimeout", "WithDeadline", "WithCancelCause", "WithTimeoutCause", "WithDeadlineCause":
			index := files.PositionFor(discarded.Pos(), false).Line - 1 - prefixLines
			if lines[index].Added {
				add("assumption", "Проверьте освобождение производного контекста",
					"Вызов context отбрасывает функцию отмены. Если используется стандартный пакет context, ресурсы могут оставаться занятыми до отмены родителя или истечения дедлайна. Сохраните cancel и вызывайте его, когда производный контекст больше не нужен.",
					"GO-CTX-01", lines[index])
			}
		}
		return true
	})
	return true
}

var taskReference = regexp.MustCompile("`([a-zA-Z_][a-zA-Z_0-9./-]*)`")

var errorRequirement = regexp.MustCompile(`(?i)сохран(?:ить|ять) причину ошибки|preserve the (?:original|underlying) error`)
var testRequirement = regexp.MustCompile(`(?i)проверить ветку ошибки тестом|добавить тест|add (?:a )?tests?`)

func assessTask(task string, lines []diff.Line, visibleWrap, lostCause, hasTestChange bool) string {
	if strings.TrimSpace(task) == "" {
		return "Описание задачи отсутствует. Соответствие изменений задаче оценить нельзя."
	}
	matches := taskReference.FindAllStringSubmatch(task, -1)
	var found, missing []string
	seen := make(map[string]bool)
	paths, identifiers := taskEvidence(lines)
	for _, match := range matches {
		ref := match[1]
		if seen[ref] {
			continue
		}
		seen[ref] = true
		present := identifiers[ref]
		present = present || paths[ref]
		if present {
			found = append(found, ref)
		} else {
			missing = append(missing, ref)
		}
	}

	var notes []string
	if len(found) > 0 {
		notes = append(notes, "В изменённых фрагментах найдены ориентиры из задачи: "+strings.Join(found, ", ")+".")
	}
	if len(missing) > 0 {
		notes = append(notes, "В проверенных фрагментах не найдены: "+strings.Join(missing, ", ")+".")
	}
	if errorRequirement.MatchString(task) {
		switch {
		case lostCause:
			notes = append(notes, "Требование сохранить причину ошибки расходится с видимой веткой, которая заменяет исходную ошибку.")
		case visibleWrap:
			notes = append(notes, "Для требования сохранить причину ошибки видно оборачивание через fmt.Errorf с %w.")
		default:
			notes = append(notes, "Для требования сохранить причину ошибки в добавленных строках не найдено проверяемого подтверждения.")
		}
	}
	if testRequirement.MatchString(task) {
		if hasTestChange {
			notes = append(notes, "Для требования о тестах есть изменения в *_test.go. Покрытие нужной ветки требует отдельной проверки.")
		} else {
			notes = append(notes, "Задача требует теста, но добавленных строк в *_test.go не видно.")
		}
	}
	if len(notes) == 0 {
		return "По этому описанию локальный анализатор не смог выделить критерии для сверки. Сопоставьте поведение с задачей вручную."
	}
	return strings.Join(notes, " ") + " Итоговое соответствие всем критериям приёмки требует ревью."
}

func taskEvidence(lines []diff.Line) (map[string]bool, map[string]bool) {
	paths, identifiers := make(map[string]bool), make(map[string]bool)
	for start := 0; start < len(lines); {
		end := start
		hasAddition := false
		for end < len(lines) && lines[end].Hunk == lines[start].Hunk {
			if lines[end].Added {
				paths[lines[end].Path] = true
				hasAddition = true
			}
			end++
		}
		if hasAddition && strings.HasSuffix(lines[start].Path, ".go") {
			previous := ""
			qualifier := ""
			scanGoHunk(lines[start:end], func(_ int, tok token.Token, literal string) {
				switch tok {
				case token.IDENT:
					identifiers[literal] = true
					if qualifier != "" {
						identifiers[qualifier+"."+literal] = true
					}
					previous, qualifier = literal, ""
				case token.PERIOD:
					qualifier, previous = previous, ""
				default:
					previous, qualifier = "", ""
				}
			})
		}
		start = end
	}
	return paths, identifiers
}

func hunkSource(lines []diff.Line) string {
	var source strings.Builder
	for _, line := range lines {
		source.WriteString(line.Text)
		source.WriteByte('\n')
	}
	return source.String()
}

func scanGoHunk(lines []diff.Line, visit func(int, token.Token, string)) {
	source := []byte(hunkSource(lines))
	file := token.NewFileSet().AddFile("", -1, len(source))
	var lexer scanner.Scanner
	lexer.Init(file, source, nil, 0)
	for {
		position, tok, literal := lexer.Scan()
		if tok == token.EOF {
			return
		}
		index := file.PositionFor(position, false).Line - 1
		visit(index, tok, literal)
	}
}
