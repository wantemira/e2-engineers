package domain

import (
	"errors"
	"time"
)

type Mentor struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type SourceLink struct {
	Label string `json:"label"`
	URL   string `json:"url"`
}

type Message struct {
	Help        *HelpTrace      `json:"help_execution,omitempty"`
	Agent       *AgentTrace     `json:"agent,omitempty"`
	Skill       *SkillExecution `json:"skill_execution,omitempty"`
	ID          string          `json:"id"`
	Role        string          `json:"role"`
	Text        string          `json:"text"`
	Links       []SourceLink    `json:"links,omitempty"`
	MentorDraft string          `json:"mentor_draft,omitempty"`
	Receipt     *ReviewRequest  `json:"receipt,omitempty"`
}

type Location struct {
	Path string `json:"path"`
	Line int    `json:"line"`
	URL  string `json:"url,omitempty"`
}

type Rule struct {
	ID    string `json:"id"`
	Label string `json:"label"`
	URL   string `json:"url"`
}

type Finding struct {
	ID          string    `json:"id"`
	Kind        string    `json:"kind"`
	Title       string    `json:"title"`
	Description string    `json:"description"`
	Location    *Location `json:"location,omitempty"`
	Rule        *Rule     `json:"rule,omitempty"`
}

type PRInfo struct {
	Number     int    `json:"number"`
	Title      string `json:"title"`
	Repository string `json:"repository"`
	URL        string `json:"url"`
}

type PullRequest struct {
	PRInfo
	OwnerID string `json:"owner_id"`
	HeadSHA string `json:"head_sha"`
	BaseSHA string `json:"base_sha"`
	Task    string `json:"task"`
	Diff    string `json:"diff"`
}

type Checks struct {
	PRNumber  int          `json:"pr_number,omitempty"`
	HeadSHA   string       `json:"head_sha,omitempty"`
	RunID     string       `json:"run_id,omitempty"`
	Source    string       `json:"source,omitempty"`
	Tests     []TestResult `json:"tests,omitempty"`
	Logs      string       `json:"logs,omitempty"`
	CreatedAt string       `json:"created_at,omitempty"`
	Status    string       `json:"status"`
	Label     string       `json:"label"`
}

type ReviewAssessment struct {
	CI             string   `json:"ci,omitempty"`
	SuggestedTests []string `json:"suggested_tests,omitempty"`
	Scope          string   `json:"scope"`
	Task           string   `json:"task"`
}

type ReviewAnalysis struct {
	Findings   []Finding        `json:"findings"`
	Assessment ReviewAssessment `json:"assessment"`
}

type ReviewRun struct {
	RulesSHA256 string            `json:"rules_sha256,omitempty"`
	Skill       *SkillExecution   `json:"skill_execution,omitempty"`
	ID          string            `json:"id"`
	HeadSHA     string            `json:"head_sha"`
	Mentor      Mentor            `json:"mentor"`
	PR          PRInfo            `json:"pr"`
	Findings    []Finding         `json:"findings"`
	Assessment  *ReviewAssessment `json:"assessment,omitempty"`
	Diff        string            `json:"diff,omitempty"`
	Checks      Checks            `json:"checks"`
	CreatedAt   time.Time         `json:"created_at"`
	IsStale     bool              `json:"is_stale,omitempty"`
}

type ReviewRequest struct {
	ID       string `json:"id"`
	Status   string `json:"status"`
	Mentor   Mentor `json:"mentor"`
	PRNumber int    `json:"pr_number"`
	HeadSHA  string `json:"head_sha"`
}

type Document struct {
	ID    string `json:"id"`
	Title string `json:"title"`
	Body  string `json:"body"`
}

type TrainingContext struct {
	Service      string `json:"service"`
	Environment  string `json:"environment"`
	GuideID      string `json:"guide_id"`
	StartCommand string `json:"start_command"`
	TestCommand  string `json:"test_command"`
}

type TrainingEnvironment struct {
	Service  string `json:"service"`
	Name     string `json:"name"`
	Status   string `json:"status"`
	Source   string `json:"source"`
	PRNumber int    `json:"pr_number"`
	HeadSHA  string `json:"head_sha"`
}

type Team struct {
	Help           *HelpCatalog      `json:"help,omitempty"`
	ID             string            `json:"id"`
	Name           string            `json:"name"`
	Repository     string            `json:"repository"`
	RepositoryURL  string            `json:"repository_url"`
	PRNumber       int               `json:"pr_number"`
	Mentor         Mentor            `json:"mentor"`
	Documents      []Document        `json:"documents"`
	Rules          map[string]Rule   `json:"rules"`
	PeopleAgentURL string            `json:"people_agent_url,omitempty"`
	Training       *TrainingContext  `json:"training,omitempty"`
	Skills         []SkillDefinition `json:"skills"`
	Capabilities   map[string]string `json:"capabilities"`
}

type ChatInput struct {
	Text          string
	History       []Message
	CurrentReview *ReviewRun
	Team          Team
}

type AppError struct {
	Code      string `json:"code"`
	Message   string `json:"message"`
	Retryable bool   `json:"retryable"`
}

func (err *AppError) Error() string { return err.Code + ": " + err.Message }

func Problem(code, message string) *AppError {
	return &AppError{Code: code, Message: message}
}

func PublicError(err error) *AppError {
	var problem *AppError
	if errors.As(err, &problem) {
		return problem
	}
	return &AppError{Code: "DEPENDENCY_UNAVAILABLE", Message: "Сервис временно недоступен. Повторите запрос.", Retryable: true}
}

type Result struct {
	Messages      []Message      `json:"messages"`
	Review        *ReviewRun     `json:"review,omitempty"`
	ReviewRequest *ReviewRequest `json:"review_request,omitempty"`
}

type Command struct {
	RunChecks        bool   `json:"run_checks,omitempty"`
	Kind             string `json:"kind"`
	Text             string `json:"text,omitempty"`
	PRRef            string `json:"pr_ref,omitempty"`
	ReviewRunID      string `json:"review_run_id,omitempty"`
	ExpectedMentorID string `json:"expected_mentor_id,omitempty"`
}

type Operation struct {
	ID        string    `json:"id"`
	State     string    `json:"state"`
	Result    *Result   `json:"result,omitempty"`
	Error     *AppError `json:"error,omitempty"`
	CreatedAt time.Time `json:"created_at"`
}

type Session struct {
	ID        string    `json:"id"`
	ActorID   string    `json:"actor_id"`
	CreatedAt time.Time `json:"created_at"`
}

type Workspace struct {
	Assistant         *AssistantInfo           `json:"assistant,omitempty"`
	Messages          []Message                `json:"messages"`
	Reviews           []ReviewRun              `json:"reviews"`
	ReviewRequests    map[string]ReviewRequest `json:"review_requests"`
	PendingOperations []PendingOperation       `json:"pending_operations"`
}

type PendingOperation struct {
	OperationID string    `json:"operation_id"`
	State       string    `json:"state"`
	Command     Command   `json:"command"`
	Key         string    `json:"key"`
	CreatedAt   time.Time `json:"created_at"`
}

type Job struct {
	Operation Operation `json:"operation"`
	Owner     string    `json:"owner"`
	ActorID   string    `json:"-"`
	Command   Command   `json:"command"`
	Key       string    `json:"key"`
}
