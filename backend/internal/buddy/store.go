package buddy

import (
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

type session struct {
	ID        string    `json:"id"`
	ActorID   string    `json:"actor_id"`
	CreatedAt time.Time `json:"created_at"`
}

type savedOperation struct {
	Operation Operation `json:"operation"`
	Owner     string    `json:"owner"`
	Command   Command   `json:"command"`
	Key       string    `json:"key"`
}

type diskState struct {
	Version     int                       `json:"version"`
	Sessions    map[string]session        `json:"sessions"`
	Operations  map[string]savedOperation `json:"operations"`
	Messages    map[string][]Message      `json:"messages"`
	Reviews     map[string][]ReviewRun    `json:"reviews"`
	Idempotency map[string]string         `json:"idempotency"`
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
			Version: 1, Sessions: map[string]session{}, Operations: map[string]savedOperation{},
			Messages: map[string][]Message{}, Reviews: map[string][]ReviewRun{}, Idempotency: map[string]string{},
		}
	}
	if store.data.Sessions == nil || store.data.Operations == nil || store.data.Messages == nil || store.data.Reviews == nil || store.data.Idempotency == nil {
		return nil, errors.New("application state is missing required collections")
	}
	return store, nil
}

func newID(prefix string) string {
	return prefix + "_" + rand.Text()
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
	if err := writeState(store.path, copy); err != nil {
		return err
	}
	store.data = copy
	return nil
}

func writeState(path string, value any) error {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return fmt.Errorf("create state directory: %w", err)
	}
	file, err := os.CreateTemp(filepath.Dir(path), ".state-*")
	if err != nil {
		return fmt.Errorf("create state file: %w", err)
	}
	name := file.Name()
	defer os.Remove(name)
	defer file.Close()
	if err := file.Chmod(0600); err != nil {
		return fmt.Errorf("set state file mode: %w", err)
	}
	if err := json.NewEncoder(file).Encode(value); err != nil {
		return fmt.Errorf("encode state file: %w", err)
	}
	if err := file.Sync(); err != nil {
		return fmt.Errorf("flush state file: %w", err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("close state file: %w", err)
	}
	if err := os.Rename(name, path); err != nil {
		return fmt.Errorf("replace state file: %w", err)
	}
	return nil
}
