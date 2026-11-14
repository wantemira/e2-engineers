package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/wantemira/e2-engineers/backend/internal/domain"
)

func (service *Service) review(ctx context.Context, actor, ref string, team domain.Team, op domain.Job) (domain.Result, error) {
	number, err := resolvePR(ref, team)
	if err != nil {
		return domain.Result{}, err
	}
	pr, err := service.git.GetPullRequest(ctx, actor, number)
	if err != nil {
		return domain.Result{}, err
	}
	if pr.Repository != team.Repository || pr.Number != team.PRNumber || pr.OwnerID != actor {
		return domain.Result{}, domain.Problem("NOT_FOUND", "PR недоступен.")
	}
	if len(pr.Diff) > 200*1024 {
		return domain.Result{}, domain.Problem("DIFF_TOO_LARGE", "Изменения превышают размер одной проверки.")
	}
	skill, err := skillForCapability(team, "pr_review")
	if err != nil {
		return domain.Result{}, err
	}
	if skill.Contract != domain.ReviewSkillContract {
		return domain.Result{}, invalidSkillReply()
	}
	var output domain.ReviewSkillOutput
	execution, err := service.executeSkill(ctx, actor, skill, domain.ReviewSkillInput{PRNumber: number, HeadSHA: pr.HeadSHA}, &output)
	if err != nil {
		return domain.Result{}, err
	}
	if err := validateSkillReview(output, pr, team); err != nil {
		return domain.Result{}, err
	}
	findings := output.Analysis.Findings
	if findings == nil {
		findings = []domain.Finding{}
	}
	latest, err := service.git.GetPullRequest(ctx, actor, number)
	if err != nil {
		return domain.Result{}, err
	}
	rules, err := json.Marshal(struct {
		Rules     map[string]domain.Rule
		Documents []domain.Document
	}{team.Rules, team.Documents})
	if err != nil {
		return domain.Result{}, err
	}
	digest := sha256.Sum256(rules)
	run := domain.ReviewRun{RulesSHA256: hex.EncodeToString(digest[:]), ID: "review_" + op.Operation.ID, HeadSHA: pr.HeadSHA, Mentor: team.Mentor, PR: pr.PRInfo,
		Findings: findings, Assessment: &output.Analysis.Assessment, Diff: pr.Diff, Checks: output.Checks, Skill: execution, CreatedAt: time.Now().UTC(), IsStale: latest.HeadSHA != pr.HeadSHA}
	for i := range run.Findings {
		if location := run.Findings[i].Location; location != nil {
			location.URL = fmt.Sprintf("/api/v1/self-reviews/%s/diff#finding-%d", run.ID, i+1)
		}
	}
	summary := "По доступным проверкам замечаний нет. Оценка задачи и границы анализа указаны в отчёте."
	if len(findings) > 0 {
		summary = fmt.Sprintf("Проверил diff PR #%d по правилам команды. Замечаний: %d. Разберём их по одному — можно спросить про конкретный пункт.", number, len(findings))
	}
	if run.IsStale {
		summary = "PR обновился во время проверки. Отчёт сохранён для предыдущей версии. Запустите проверку повторно."
	}
	summary += skillSourceText(execution)
	messages := []domain.Message{}
	if op.Command.Kind == "message" {
		messages = append(messages, domain.Message{ID: "user_" + op.Operation.ID, Role: "user", Text: op.Command.Text})
	}
	messages = append(messages, domain.Message{ID: "assistant_" + op.Operation.ID, Role: "assistant", Text: summary, Skill: execution})
	return domain.Result{Messages: messages, Review: &run}, nil
}

func (service *Service) requestReview(ctx context.Context, actor string, team domain.Team, op domain.Job, state domain.Workspace) (domain.Result, error) {
	index := slices.IndexFunc(state.Reviews, func(run domain.ReviewRun) bool { return run.ID == op.Command.ReviewRunID })
	if index < 0 {
		return domain.Result{}, domain.Problem("NOT_FOUND", "Проверка недоступна.")
	}
	run := state.Reviews[index]
	if run.IsStale {
		return domain.Result{}, domain.Problem("STALE_HEAD", "Версия PR устарела.")
	}
	if team.Mentor.ID != op.Command.ExpectedMentorID || run.Mentor.ID != team.Mentor.ID {
		return domain.Result{}, domain.Problem("INVALID_STATE", "Наставник изменился. Обновите проверку и подтвердите отправку.")
	}
	receipt, err := service.git.RequestReview(ctx, actor, run.PR.Number, run.HeadSHA, team.Mentor, op.Operation.ID)
	if err != nil {
		return domain.Result{}, err
	}
	if receipt.Status != "REQUESTED" || receipt.ID == "" || receipt.PRNumber != run.PR.Number || receipt.HeadSHA != run.HeadSHA || receipt.Mentor.ID != team.Mentor.ID {
		return domain.Result{}, &domain.AppError{Code: "DEPENDENCY_UNAVAILABLE", Message: "Git-сервис не подтвердил отправку проверенной версии PR. Уточните результат запроса.", Retryable: true}
	}
	return domain.Result{Messages: []domain.Message{{ID: "receipt_" + receipt.ID, Role: "assistant", Text: "", Receipt: &receipt}}, ReviewRequest: &receipt}, nil
}

var prNumberPattern = regexp.MustCompile(`(?i)(?:\bpr|пр)\s*#?\s*(\d+)`)
var urlPattern = regexp.MustCompile(`https?://[^\s<>]+`)
var standalonePR = regexp.MustCompile(`(?i)^\s*(?:(?:pr|пр)\s*)?#?\s*\d+\s*$|^\s*https?://\S+\s*$`)

func referenceInMessage(text string) string {
	for _, candidate := range urlPattern.FindAllString(text, -1) {
		if strings.Contains(candidate, "/pull/") {
			return strings.TrimRight(candidate, ".,;!?)")
		}
	}
	if match := prNumberPattern.FindStringSubmatch(text); len(match) > 1 {
		return match[1]
	}
	trimmed := strings.TrimSpace(strings.TrimPrefix(text, "#"))
	if _, err := strconv.Atoi(trimmed); err == nil {
		return trimmed
	}
	return ""
}

func resolvePR(ref string, team domain.Team) (int, error) {
	value := strings.TrimSpace(ref)
	if strings.Contains(value, "://") {
		parsed, err := url.Parse(value)
		base, baseErr := url.Parse(team.RepositoryURL)
		if err != nil || baseErr != nil || parsed.Scheme != "https" || parsed.Host != base.Host || parsed.User != nil || parsed.RawQuery != "" {
			return 0, domain.Problem("INVALID_INPUT", "Проверьте ссылку на PR команды.")
		}
		prefix := strings.TrimSuffix(base.Path, "/") + "/pull/"
		if !strings.HasPrefix(parsed.Path, prefix) {
			return 0, domain.Problem("NOT_FOUND", "PR недоступен.")
		}
		value = strings.TrimPrefix(parsed.Path, prefix)
	} else {
		value = strings.TrimSpace(strings.TrimPrefix(strings.ToUpper(value), "PR"))
		value = strings.TrimSpace(strings.TrimPrefix(value, "#"))
	}
	number, err := strconv.Atoi(value)
	if err != nil || number < 1 {
		return 0, domain.Problem("INVALID_INPUT", "Укажите номер или ссылку на PR.")
	}
	if number != team.PRNumber {
		return 0, domain.Problem("NOT_FOUND", "PR недоступен.")
	}
	return number, nil
}
