package service

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"slices"
	"time"

	"github.com/wantemira/e2-engineers/backend/internal/domain"
)

type Service struct {
	repository Repository
	git        GitTools
	assistant  Assistant
	teams      TeamLoader
	wake       chan struct{}
}

func NewService(repository Repository, git GitTools, assistant Assistant, teams TeamLoader) *Service {
	return &Service{repository: repository, git: git, assistant: assistant, teams: teams, wake: make(chan struct{}, 1)}
}

func newID(prefix string) string {
	return prefix + "_" + rand.Text()
}

func tokenHash(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

func (service *Service) Session(token string) (domain.Session, string, error) {
	if token != "" {
		existing, found, err := service.repository.FindSession(tokenHash(token))
		if err != nil {
			return domain.Session{}, "", err
		}
		if found {
			return existing, "", nil
		}
	}
	token = newID("session")
	created := domain.Session{ID: newID("actor-session"), ActorID: "intern-1", CreatedAt: time.Now().UTC()}
	err := service.repository.SaveSession(tokenHash(token), created)
	return created, token, err
}

func (service *Service) Workspace(owner string) (domain.Workspace, error) {
	return service.repository.Workspace(owner)
}

func (service *Service) Document(id string) (domain.Document, map[string]domain.Rule, error) {
	team, err := service.teams.Load()
	if err != nil {
		return domain.Document{}, nil, err
	}
	index := slices.IndexFunc(team.Documents, func(document domain.Document) bool { return document.ID == id })
	if index < 0 {
		return domain.Document{}, nil, domain.Problem("NOT_FOUND", "Документ недоступен.")
	}
	return team.Documents[index], team.Rules, nil
}

func (service *Service) Submit(owner domain.Session, key string, command domain.Command) (string, error) {
	id := newID("op")
	job := domain.Job{
		Operation: domain.Operation{ID: id, State: "QUEUED", CreatedAt: time.Now().UTC()},
		Owner:     owner.ID, Command: command, Key: key,
	}
	var message *domain.Message
	if command.Kind == "message" {
		message = &domain.Message{ID: "user_" + id, Role: "user", Text: command.Text}
	}
	id, err := service.repository.Submit(job, message)
	if err == nil {
		service.signal()
	}
	return id, err
}

func (service *Service) Operation(owner, id string) (domain.Operation, error) {
	job, err := service.repository.GetOperation(owner, id)
	return job.Operation, err
}

func (service *Service) Retry(owner, id, key string) error {
	job, err := service.repository.GetOperation(owner, id)
	if err != nil {
		return err
	}
	if key != job.Key {
		return domain.Problem("IDEMPOTENCY_CONFLICT", "Для повтора нужен исходный ключ операции.")
	}
	if job.Operation.State == "FAILED" {
		return domain.Problem("INVALID_STATE", "Операция завершилась ошибкой. Создайте новый запрос.")
	}
	if job.Operation.State == "RESULT_UNKNOWN" {
		if err := service.repository.RequeueUnknown(owner, id); err != nil {
			return err
		}
	}
	service.signal()
	return nil
}
