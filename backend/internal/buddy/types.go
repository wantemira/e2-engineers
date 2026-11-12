package buddy

import (
	"context"
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
	ID      string         `json:"id"`
	Role    string         `json:"role"`
	Text    string         `json:"text"`
	Links   []SourceLink   `json:"links,omitempty"`
	Receipt *ReviewRequest `json:"receipt,omitempty"`
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
	Status string `json:"status"`
	Label  string `json:"label"`
}

type ReviewRun struct {
	ID        string    `json:"id"`
	HeadSHA   string    `json:"head_sha"`
	Mentor    Mentor    `json:"mentor"`
	PR        PRInfo    `json:"pr"`
	Findings  []Finding `json:"findings"`
	Checks    Checks    `json:"checks"`
	CreatedAt time.Time `json:"created_at"`
	IsStale   bool      `json:"is_stale,omitempty"`
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

type Team struct {
	ID            string          `json:"id"`
	Name          string          `json:"name"`
	Repository    string          `json:"repository"`
	RepositoryURL string          `json:"repository_url"`
	PRNumber      int             `json:"pr_number"`
	Mentor        Mentor          `json:"mentor"`
	Documents     []Document      `json:"documents"`
	Rules         map[string]Rule `json:"rules"`
	Skill         string          `json:"skill"`
}

type ChatInput struct {
	Text          string
	History       []Message
	CurrentReview *ReviewRun
	Team          Team
}

type Assistant interface {
	Answer(context.Context, ChatInput) (Message, error)
	Review(context.Context, PullRequest, Checks, Team) ([]Finding, error)
}

type GitTools interface {
	GetPullRequest(context.Context, string, int) (PullRequest, error)
	GetChecks(context.Context, string, int, string) (Checks, error)
	RequestReview(context.Context, string, int, string, Mentor, string) (ReviewRequest, error)
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

type Result struct {
	Messages      []Message      `json:"messages"`
	Review        *ReviewRun     `json:"review,omitempty"`
	ReviewRequest *ReviewRequest `json:"review_request,omitempty"`
}

type Command struct {
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
