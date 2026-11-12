package repository

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"slices"
	"strconv"
	"sync"

	"github.com/wantemira/e2-engineers/backend/internal/domain"
	"github.com/wantemira/e2-engineers/backend/internal/storage/jsonfile"
)

type diskState struct {
	Version     int                           `json:"version"`
	Sessions    map[string]domain.Session     `json:"sessions"`
	Operations  map[string]domain.Job         `json:"operations"`
	Messages    map[string][]domain.Message   `json:"messages"`
	Reviews     map[string][]domain.ReviewRun `json:"reviews"`
	Idempotency map[string]string             `json:"idempotency"`
}

type Store struct {
	mu   sync.Mutex
	path string
	data diskState
}

func NewStore(path string) (*Store, error) {
	store := &Store{path: path}
	data, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("read application state: %w", err)
	}
	if err == nil {
		if err := json.Unmarshal(data, &store.data); err != nil {
			return nil, fmt.Errorf("decode application state: %w", err)
		}
		if store.data.Version != 1 {
			return nil, fmt.Errorf("unsupported state version %d", store.data.Version)
		}
	} else {
		store.data = diskState{
			Version: 1, Sessions: map[string]domain.Session{}, Operations: map[string]domain.Job{},
			Messages: map[string][]domain.Message{}, Reviews: map[string][]domain.ReviewRun{}, Idempotency: map[string]string{},
		}
	}
	if store.data.Sessions == nil || store.data.Operations == nil || store.data.Messages == nil || store.data.Reviews == nil || store.data.Idempotency == nil {
		return nil, errors.New("application state is missing required collections")
	}
	return store, nil
}

func cloneState(state diskState) (diskState, error) {
	data, err := json.Marshal(state)
	if err != nil {
		return diskState{}, fmt.Errorf("encode state snapshot: %w", err)
	}
	var copy diskState
	if err := json.Unmarshal(data, &copy); err != nil {
		return diskState{}, fmt.Errorf("decode state snapshot: %w", err)
	}
	return copy, nil
}

func (store *Store) snapshot() (diskState, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	return cloneState(store.data)
}

func (store *Store) update(change func(*diskState) error) error {
	store.mu.Lock()
	defer store.mu.Unlock()
	copy, err := cloneState(store.data)
	if err != nil {
		return err
	}
	if err := change(&copy); err != nil {
		return err
	}
	if err := jsonfile.Write(store.path, copy); err != nil {
		return err
	}
	store.data = copy
	return nil
}

func (store *Store) FindSession(tokenHash string) (domain.Session, bool, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	value, exists := store.data.Sessions[tokenHash]
	return value, exists, nil
}

func (store *Store) SaveSession(tokenHash string, session domain.Session) error {
	return store.update(func(state *diskState) error {
		state.Sessions[tokenHash] = session
		return nil
	})
}

func (store *Store) Submit(job domain.Job, initialMessage *domain.Message) (string, error) {
	id := job.Operation.ID
	index := job.Owner + "\x00" + job.Command.Kind + "\x00" + job.Key
	err := store.update(func(state *diskState) error {
		if previous, exists := state.Idempotency[index]; exists {
			if state.Operations[previous].Command != job.Command {
				return domain.Problem("IDEMPOTENCY_CONFLICT", "Ключ уже использован для другого запроса.")
			}
			id = previous
			return nil
		}
		state.Operations[id] = job
		state.Idempotency[index] = id
		if initialMessage != nil {
			state.Messages[job.Owner] = append(state.Messages[job.Owner], *initialMessage)
		}
		return nil
	})
	return id, err
}

func (store *Store) GetOperation(owner, id string) (domain.Job, error) {
	state, err := store.snapshot()
	if err != nil {
		return domain.Job{}, err
	}
	job, exists := state.Operations[id]
	if !exists || job.Owner != owner {
		return domain.Job{}, domain.Problem("NOT_FOUND", "Операция недоступна.")
	}
	return job, nil
}

func (store *Store) RequeueUnknown(owner, id string) error {
	return store.update(func(state *diskState) error {
		job, exists := state.Operations[id]
		if !exists || job.Owner != owner {
			return domain.Problem("NOT_FOUND", "Операция недоступна.")
		}
		if job.Operation.State == "RESULT_UNKNOWN" {
			job.Operation.State = "QUEUED"
			job.Operation.Error = nil
			state.Operations[id] = job
		}
		return nil
	})
}

func (store *Store) RecoverOperations() error {
	return store.update(func(state *diskState) error {
		for id, job := range state.Operations {
			if job.Operation.State == "RUNNING" {
				job.Operation.State = "QUEUED"
				state.Operations[id] = job
			}
		}
		return nil
	})
}

var errQueueEmpty = errors.New("no queued operations")

func (store *Store) ClaimNext() (domain.Job, bool, error) {
	var selected domain.Job
	err := store.update(func(state *diskState) error {
		for _, job := range state.Operations {
			if job.Operation.State == "QUEUED" && (selected.Operation.ID == "" || job.Operation.CreatedAt.Before(selected.Operation.CreatedAt)) {
				selected = job
			}
		}
		if selected.Operation.ID == "" {
			return errQueueEmpty
		}
		selected.Operation.State = "RUNNING"
		state.Operations[selected.Operation.ID] = selected
		for _, owner := range state.Sessions {
			if owner.ID == selected.Owner {
				selected.ActorID = owner.ActorID
				break
			}
		}
		return nil
	})
	if errors.Is(err, errQueueEmpty) {
		return domain.Job{}, false, nil
	}
	return selected, err == nil, err
}

func (store *Store) Workspace(owner string) (domain.Workspace, error) {
	state, err := store.snapshot()
	if err != nil {
		return domain.Workspace{}, err
	}
	result := domain.Workspace{
		Messages:       append([]domain.Message{}, state.Messages[owner]...),
		Reviews:        append([]domain.ReviewRun{}, state.Reviews[owner]...),
		ReviewRequests: map[string]domain.ReviewRequest{},
	}
	for _, job := range state.Operations {
		if job.Owner != owner || job.Operation.Result == nil || job.Operation.Result.ReviewRequest == nil {
			continue
		}
		index := slices.IndexFunc(result.Reviews, func(run domain.ReviewRun) bool { return run.ID == job.Command.ReviewRunID })
		if index >= 0 {
			run := result.Reviews[index]
			result.ReviewRequests[run.PR.Repository+"#"+strconv.Itoa(run.PR.Number)] = *job.Operation.Result.ReviewRequest
		}
	}
	return result, nil
}

// Complete persists the operation outcome and its workspace changes together.
func (store *Store) Complete(operation domain.Operation) error {
	return store.update(func(state *diskState) error {
		job, exists := state.Operations[operation.ID]
		if !exists || job.Operation.State != "RUNNING" {
			return fmt.Errorf("operation %s is not running", operation.ID)
		}
		job.Operation = operation
		state.Operations[operation.ID] = job
		if operation.Result == nil {
			return nil
		}
		for _, message := range operation.Result.Messages {
			if !slices.ContainsFunc(state.Messages[job.Owner], func(existing domain.Message) bool { return existing.ID == message.ID }) {
				state.Messages[job.Owner] = append(state.Messages[job.Owner], message)
			}
		}
		if review := operation.Result.Review; review != nil {
			state.Reviews[job.Owner] = append([]domain.ReviewRun{*review}, state.Reviews[job.Owner]...)
		}
		return nil
	})
}
