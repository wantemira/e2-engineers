package repository_test

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/wantemira/e2-engineers/backend/internal/domain"
	"github.com/wantemira/e2-engineers/backend/internal/repository"
)

func TestLoadExistingStateAndRecoverInterruptedOperation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "application.json")
	const existingState = `{
	  "version": 1,
	  "sessions": {"token-hash": {"id": "session-1", "actor_id": "intern-1", "created_at": "2026-11-12T10:00:00Z"}},
	  "operations": {
	    "op-1": {
	      "operation": {"id": "op-1", "state": "RUNNING", "created_at": "2026-11-12T10:01:00Z"},
	      "owner": "session-1", "command": {"kind": "message", "text": "Привет"}, "key": "message-1"
	    }
	  },
	  "messages": {"session-1": [{"id": "user_op-1", "role": "user", "text": "Привет"}]},
	  "reviews": {},
	  "idempotency": {"session-1\u0000message\u0000message-1": "op-1"}
	}`
	if err := os.WriteFile(path, []byte(existingState), 0600); err != nil {
		t.Fatal(err)
	}
	store, err := repository.NewStore(path)
	if err != nil {
		t.Fatal(err)
	}
	owner, found, err := store.FindSession("token-hash")
	if err != nil || !found || owner.ID != "session-1" {
		t.Fatalf("could not restore session: %+v, found=%t, err=%v", owner, found, err)
	}
	if err := store.RecoverOperations(); err != nil {
		t.Fatal(err)
	}
	pending, err := store.Workspace(owner.ID)
	if err != nil || len(pending.PendingOperations) != 1 || pending.PendingOperations[0].State != "QUEUED" {
		t.Fatalf("interrupted operation must be returned to its owner: %+v, %v", pending, err)
	}
	job, found, err := store.ClaimNext()
	if err != nil || !found || job.Operation.ID != "op-1" || job.ActorID != "intern-1" || job.Command.Text != "Привет" || job.Key != "message-1" {
		t.Fatalf("could not restore interrupted operation: %+v, found=%t, err=%v", job, found, err)
	}
	duplicate := job
	duplicate.Operation.ID = "op-duplicate"
	id, err := store.Submit(duplicate, &domain.Message{ID: "user_op-duplicate", Role: "user", Text: "Привет"})
	if err != nil || id != "op-1" {
		t.Fatalf("existing idempotency key resolved to %q, err=%v", id, err)
	}
	job.Operation.State = "SUCCEEDED"
	job.Operation.Result = &domain.Result{Messages: []domain.Message{
		{ID: "user_op-1", Role: "user", Text: "Привет"},
		{ID: "assistant_op-1", Role: "assistant", Text: "Здравствуйте"},
	}}
	if err := store.Complete(job.Operation); err != nil {
		t.Fatal(err)
	}
	reopened, err := repository.NewStore(path)
	if err != nil {
		t.Fatal(err)
	}
	workspace, err := reopened.Workspace(owner.ID)
	if err != nil || len(workspace.Messages) != 2 || workspace.Messages[1].Text != "Здравствуйте" || len(workspace.PendingOperations) != 0 {
		t.Fatalf("expected original message and one response: %+v, err=%v", workspace, err)
	}
}

func TestWorkspaceRestoresOnlyOwnersPendingOperationsInOrder(t *testing.T) {
	path := filepath.Join(t.TempDir(), "application.json")
	store, err := repository.NewStore(path)
	if err != nil {
		t.Fatal(err)
	}
	for index, tc := range []struct {
		id, state, owner string
	}{
		{"running", "RUNNING", "session-1"},
		{"foreign", "RESULT_UNKNOWN", "session-2"},
		{"unknown", "RESULT_UNKNOWN", "session-1"},
		{"done", "SUCCEEDED", "session-1"},
		{"queued", "QUEUED", "session-1"},
		{"failed", "FAILED", "session-1"},
	} {
		_, err := store.Submit(domain.Job{
			Operation: domain.Operation{ID: tc.id, State: tc.state, CreatedAt: time.Unix(int64(index), 0)},
			Owner:     tc.owner, Key: "key-" + tc.id,
			Command: domain.Command{Kind: "request", ReviewRunID: "saved-review", ExpectedMentorID: "mentor-1"},
		}, nil)
		if err != nil {
			t.Fatal(err)
		}
	}
	reopened, err := repository.NewStore(path)
	if err != nil {
		t.Fatal(err)
	}
	workspace, err := reopened.Workspace("session-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(workspace.PendingOperations) != 3 {
		t.Fatalf("unexpected pending operations: %+v", workspace.PendingOperations)
	}
	for i, id := range []string{"running", "unknown", "queued"} {
		pending := workspace.PendingOperations[i]
		if pending.OperationID != id || pending.Key != "key-"+id || pending.Command.ReviewRunID != "saved-review" {
			t.Fatalf("lost operation identity or order: %+v", pending)
		}
	}
	empty, err := reopened.Workspace("new-session")
	if err != nil || empty.PendingOperations == nil || len(empty.PendingOperations) != 0 {
		t.Fatalf("new session must have an empty collection: %+v, %v", empty, err)
	}
}
