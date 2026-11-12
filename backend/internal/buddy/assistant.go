package buddy

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

type configuredDocument struct {
	ID    string `json:"id"`
	Title string `json:"title"`
	File  string `json:"file"`
}

type teamConfiguration struct {
	ID            string               `json:"id"`
	Name          string               `json:"name"`
	Repository    string               `json:"repository"`
	RepositoryURL string               `json:"repository_url"`
	PRNumber      int                  `json:"pr_number"`
	Mentor        Mentor               `json:"mentor"`
	Documents     []configuredDocument `json:"documents"`
	Rules         map[string]Rule      `json:"rules"`
}

func LoadTeam(projectRoot string) (Team, error) {
	directory := filepath.Join(projectRoot, "teams", "demo-backend")
	data, err := os.ReadFile(filepath.Join(directory, "context.json"))
	if err != nil {
		return Team{}, fmt.Errorf("read team configuration: %w", err)
	}
	var configuration teamConfiguration
	if err := json.Unmarshal(data, &configuration); err != nil {
		return Team{}, fmt.Errorf("decode team configuration: %w", err)
	}
	if configuration.ID == "" || configuration.Name == "" || configuration.Repository == "" || configuration.RepositoryURL == "" || configuration.PRNumber < 1 || configuration.Mentor.ID == "" || configuration.Mentor.Name == "" {
		return Team{}, fmt.Errorf("team configuration has missing required fields")
	}
	team := Team{
		ID: configuration.ID, Name: configuration.Name, Repository: configuration.Repository,
		PRNumber: configuration.PRNumber, Mentor: configuration.Mentor, Rules: configuration.Rules,
		RepositoryURL: configuration.RepositoryURL,
		Documents:     make([]Document, 0, len(configuration.Documents)),
	}
	seen := make(map[string]bool)
	for _, document := range configuration.Documents {
		if document.ID == "" || document.Title == "" || document.File == "" || filepath.Base(document.File) != document.File || strings.ContainsAny(document.File, `/\`) || seen[document.ID] {
			return Team{}, fmt.Errorf("invalid document configuration %q", document.ID)
		}
		body, err := os.ReadFile(filepath.Join(directory, document.File))
		if err != nil {
			return Team{}, fmt.Errorf("read team document %s: %w", document.ID, err)
		}
		if strings.TrimSpace(string(body)) == "" {
			return Team{}, fmt.Errorf("team document %s is empty", document.ID)
		}
		seen[document.ID] = true
		team.Documents = append(team.Documents, Document{ID: document.ID, Title: document.Title, Body: string(body)})
	}
	for _, id := range []string{"context", "review-rules", "access-guide", "people-guide"} {
		if !seen[id] {
			return Team{}, fmt.Errorf("required team document %s is missing", id)
		}
	}
	for _, id := range []string{"GO-ERR-01", "GO-TEST-01"} {
		rule, ok := team.Rules[id]
		if !ok || rule.ID != id || rule.Label == "" || rule.URL != "/api/v1/context/documents/review-rules#"+id {
			return Team{}, fmt.Errorf("invalid team rule %s", id)
		}
	}
	skill, err := os.ReadFile(filepath.Join(projectRoot, "skills", "pr-self-review", "SKILL.md"))
	if err != nil {
		return Team{}, fmt.Errorf("read self-review skill: %w", err)
	}
	if strings.TrimSpace(string(skill)) == "" {
		return Team{}, fmt.Errorf("self-review skill is empty")
	}
	team.Skill = string(skill)
	return team, nil
}

type localAssistant struct{}

func NewLocalAssistant() Assistant { return localAssistant{} }

var findingReference = regexp.MustCompile(`(?i)(?:пункт|замечани[ея]|пункту|замечанию|f)\s*[№#]?\s*(\d+)`)

func (localAssistant) Answer(ctx context.Context, input ChatInput) (Message, error) {
	if err := ctx.Err(); err != nil {
		return Message{}, err
	}
	text := strings.ToLower(strings.TrimSpace(input.Text))
	if finding, explicit := referencedFinding(text, input.CurrentReview); explicit {
		if finding == nil {
			return reply("Укажите номер замечания из текущего отчёта. Например, «Объясни замечание 1»."), nil
		}
		return explain(*finding), nil
	}
	if containsAny(text, "почему", "объясни", "объяснит", "подробнее", "как исправить", "что значит", "что это значит") && input.CurrentReview != nil {
		if finding := matchingFinding(text, input.CurrentReview); finding != nil {
			return explain(*finding), nil
		}
		if containsAny(text, "это", "так", "подробнее") {
			for index := len(input.History) - 1; index >= 0; index-- {
				previous := input.History[index]
				if previous.Role == "user" && previous.Text != input.Text {
					if finding := matchingFinding(strings.ToLower(previous.Text), input.CurrentReview); finding != nil {
						return explain(*finding), nil
					}
					break
				}
			}
		}
		if len(input.CurrentReview.Findings) == 1 {
			return explain(input.CurrentReview.Findings[0]), nil
		}
		if len(input.CurrentReview.Findings) > 1 && containsAny(text, "замечан", "пункт", "это", "подробнее") {
			return reply("Какое замечание разобрать? Укажите его номер или название из отчёта."), nil
		}
	}

	switch {
	case containsAny(text, "больнич", "болею", "заболел"):
		return documentAnswer(input.Team, "people-guide", "Больничный"), nil
	case containsAny(text, "доступ", "permission", "access"):
		return documentAnswer(input.Team, "access-guide", "Доступ к инструменту"), nil
	case containsAny(text, "наставник", "ментор", "buddy", "кому обратиться", "не получается", "не могу"):
		message := reply(fmt.Sprintf("Ваш наставник — %s. Если вопрос требует его участия, соберите ссылку на задачу или PR, ожидаемый результат, текст ошибки и то, что уже проверили. Могу помочь сформулировать обращение.", input.Team.Mentor.Name))
		message.Links = []SourceLink{documentLink(input.Team, "context")}
		return message, nil
	case containsAny(text, "%w", "обработк", "причин", "errors.is", "errors.as", "обернуть", "оборачива"):
		return documentAnswer(input.Team, "review-rules", "GO-ERR-01"), nil
	case containsAny(text, "тест", "test", "ci", "пайплайн", "сборк"):
		return documentAnswer(input.Team, "review-rules", "GO-TEST-01"), nil
	case containsAny(text, "правил", "соглаш", "ревью", "review", "pull request", "pr", "формат"):
		return documentAnswer(input.Team, "review-rules", "Самопроверка"), nil
	case containsAny(text, "привет", "здравств", "добрый", "доброе", "помоги", "помощь", "что умеешь", "что можешь", "начать"):
		return documentAnswer(input.Team, "context", "Помощь"), nil
	default:
		return reply("Уточните рабочий вопрос: о каком инструменте, правиле или замечании идёт речь? Для проверки пришлите номер или ссылку на PR. Если проблема требует решения команды, помогу подготовить вопрос наставнику."), nil
	}
}

func reply(text string) Message { return Message{Role: "assistant", Text: text} }

func containsAny(text string, values ...string) bool {
	for _, value := range values {
		if strings.Contains(text, value) {
			return true
		}
	}
	return false
}

func referencedFinding(text string, review *ReviewRun) (*Finding, bool) {
	match := findingReference.FindStringSubmatch(text)
	if match == nil {
		return nil, false
	}
	index, err := strconv.Atoi(match[1])
	if err != nil || review == nil || index < 1 || index > len(review.Findings) {
		return nil, true
	}
	return &review.Findings[index-1], true
}

func matchingFinding(text string, review *ReviewRun) *Finding {
	for index := range review.Findings {
		finding := &review.Findings[index]
		if strings.Contains(text, strings.ToLower(finding.Title)) || (finding.Location != nil && strings.Contains(text, strings.ToLower(finding.Location.Path))) {
			return finding
		}
		if finding.Rule != nil {
			if finding.Rule.ID == "GO-ERR-01" && containsAny(text, "ошибк", "причин", "%w", "errors.") {
				return finding
			}
			if finding.Rule.ID == "GO-TEST-01" && containsAny(text, "тест", "test") {
				return finding
			}
		}
	}
	return nil
}

func explain(finding Finding) Message {
	message := reply(finding.Title + "\n\n" + finding.Description)
	if finding.Rule != nil {
		message.Links = []SourceLink{{Label: finding.Rule.Label, URL: finding.Rule.URL}}
	}
	return message
}

func documentLink(team Team, id string) SourceLink {
	for _, document := range team.Documents {
		if document.ID == id {
			return SourceLink{Label: document.Title, URL: "/api/v1/context/documents/" + id}
		}
	}
	return SourceLink{}
}

func documentAnswer(team Team, id, heading string) Message {
	for _, document := range team.Documents {
		if document.ID == id {
			text := section(document.Body, heading)
			if text == "" {
				return reply("В командном документе пока нет ответа на этот вопрос. Уточните порядок у наставника.")
			}
			message := reply(text)
			message.Links = []SourceLink{documentLink(team, id)}
			return message
		}
	}
	return reply("Командный документ недоступен. Уточните порядок у наставника.")
}

func section(body, heading string) string {
	lines := strings.Split(body, "\n")
	start := -1
	for index, line := range lines {
		if strings.TrimSpace(line) == "## "+heading {
			start = index + 1
			continue
		}
		if start >= 0 && strings.HasPrefix(line, "## ") {
			return strings.TrimSpace(strings.Join(lines[start:index], "\n"))
		}
	}
	if start >= 0 {
		return strings.TrimSpace(strings.Join(lines[start:], "\n"))
	}
	return ""
}

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

func (localAssistant) Review(ctx context.Context, pr PullRequest, _ Checks, team Team) ([]Finding, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if len(pr.Diff) > 200*1024 {
		return nil, Problem("DIFF_TOO_LARGE", "Изменений слишком много для одной проверки.")
	}
	lines, err := parseDiff(pr.Diff)
	if err != nil {
		return nil, fmt.Errorf("parse PR diff: %w", err)
	}
	findings := make([]Finding, 0)
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
					findings = append(findings, Finding{
						ID: fmt.Sprintf("F%d", len(findings)+1), Kind: "problem", Title: "Теряется причина ошибки",
						Description: "В ветке err != nil возвращается новая ошибка без исходной причины. Вызывающий код потеряет возможность проверить её через errors.Is или errors.As. Добавьте контекст через fmt.Errorf(\"...: %w\", err), чтобы сохранить цепочку ошибок.",
						Location:    &Location{Path: line.path, Line: line.line}, Rule: &rule,
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
		findings = append(findings, Finding{
			ID: fmt.Sprintf("F%d", len(findings)+1), Kind: "assumption", Title: "Проверьте тесты изменённой логики",
			Description: "В доступном diff изменён Go-код, а изменений в файлах *_test.go не видно. Это не доказывает отсутствие тестов в репозитории. Проверьте существующие тесты и добавьте кейс для новой или изменённой ветки, если она ещё не покрыта.",
			Location:    &Location{Path: goChange.path, Line: goChange.line}, Rule: &rule,
		})
	}
	return findings, nil
}
