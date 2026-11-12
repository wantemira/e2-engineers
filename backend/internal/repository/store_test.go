package repository_test

import (
	"os"
	"path/filepath"
	"testing"

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
	if err != nil || len(workspace.Messages) != 2 || workspace.Messages[1].Text != "Здравствуйте" {
		t.Fatalf("expected original message and one response: %+v, err=%v", workspace, err)
	}
}
