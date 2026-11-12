package service

import (
	"context"

	"github.com/wantemira/e2-engineers/backend/internal/domain"
)

type Repository interface {
	FindSession(tokenHash string) (domain.Session, bool, error)
	SaveSession(tokenHash string, session domain.Session) error
	Submit(job domain.Job, initialMessage *domain.Message) (string, error)
	GetOperation(owner, id string) (domain.Job, error)
	RequeueUnknown(owner, id string) error
	RecoverOperations() error
	ClaimNext() (domain.Job, bool, error)
	Workspace(owner string) (domain.Workspace, error)
	Complete(operation domain.Operation) error
}

type GitTools interface {
	GetPullRequest(context.Context, string, int) (domain.PullRequest, error)
	GetChecks(context.Context, string, int, string) (domain.Checks, error)
	RequestReview(context.Context, string, int, string, domain.Mentor, string) (domain.ReviewRequest, error)
}

type Assistant interface {
	Answer(context.Context, domain.ChatInput) (domain.Message, error)
	Review(context.Context, domain.PullRequest, domain.Checks, domain.Team) ([]domain.Finding, error)
}

type TeamLoader interface {
	Load() (domain.Team, error)
}
