package httpapi_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/wantemira/e2-engineers/backend/internal/assistant"
	"github.com/wantemira/e2-engineers/backend/internal/domain"
	"github.com/wantemira/e2-engineers/backend/internal/integration/mcpgit"
	"github.com/wantemira/e2-engineers/backend/internal/repository"
	"github.com/wantemira/e2-engineers/backend/internal/service"
	"github.com/wantemira/e2-engineers/backend/internal/teamconfig"
	"github.com/wantemira/e2-engineers/backend/internal/transport/httpapi"
)

func TestWorkspaceKeepsReviewsAndRequestsScopedToSession(t *testing.T) {
	root := filepath.Join("..", "..", "..", "..")
	directory := t.TempDir()
	fixture := filepath.Join(directory, "pr.json")
	data, err := os.ReadFile(filepath.Join(root, "mocks", "pr-17.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(fixture, data, 0600); err != nil {
		t.Fatal(err)
	}
	const secret = "test-secret"
	gitHandler, err := mcpgit.NewGitHandler(fixture, filepath.Join(directory, "git-state.json"), secret)
	if err != nil {
		t.Fatal(err)
	}
	gitServer := httptest.NewServer(gitHandler)
	t.Cleanup(gitServer.Close)
	statePath := filepath.Join(directory, "application.json")
	store, err := repository.NewStore(statePath)
	if err != nil {
		t.Fatal(err)
	}
	git := mcpgit.NewMCPGit(gitServer.URL, secret)
	teams := teamconfig.New(root)
	localAssistant := assistant.NewLocalAssistant()
	app := service.NewService(store, git, localAssistant, teams)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- app.Run(ctx) }()
	t.Cleanup(func() {
		cancel()
		if err := <-done; err != nil {
			t.Error(err)
		}
	})
	api := httptest.NewServer(httpapi.NewHTTPHandler(app, http.NotFoundHandler(), nil))
	t.Cleanup(api.Close)
	client := sessionClient(t)
	other := sessionClient(t)
	var empty domain.Workspace
	apiCall(t, client, "GET", api.URL+"/api/v1/workspace", "", nil, http.StatusOK, &empty)
	if empty.Messages == nil || empty.Reviews == nil || empty.ReviewRequests == nil {
		t.Fatalf("workspace collections must be present: %+v", empty)
	}

	message := map[string]string{"text": "Проверь PR #17"}
	operationID := submitForTest(t, client, api.URL, "/messages", "message-1", message)
	if duplicate := submitForTest(t, client, api.URL, "/messages", "message-1", message); duplicate != operationID {
		t.Fatalf("duplicate message got %s, want %s", duplicate, operationID)
	}
	apiCall(t, client, "POST", api.URL+"/api/v1/messages", "message-1", map[string]string{"text": "Другое сообщение"}, http.StatusConflict, nil)
	apiCall(t, other, "GET", api.URL+"/api/v1/operations/"+operationID, "", nil, http.StatusNotFound, nil)
	initial := waitForOperation(t, client, api.URL, operationID)
	if initial.Result == nil || initial.Result.Review == nil || len(initial.Result.Review.Findings) == 0 {
		t.Fatalf("expected review with findings: %+v", initial)
	}
	first := initial.Result.Review
	apiCall(t, other, "GET", api.URL+"/api/v1/workspace", "", nil, http.StatusOK, &empty)
	if len(empty.Reviews) != 0 || len(empty.Messages) != 0 {
		t.Fatalf("another session saw the workspace: %+v", empty)
	}

	useFixedRevision(t, fixture, data)
	confirmation := map[string]string{"review_run_id": first.ID, "expected_mentor_id": first.Mentor.ID}
	stale := waitForOperation(t, client, api.URL, submitForTest(t, client, api.URL, "/review-requests", "stale-1", confirmation))
	if stale.State != "FAILED" || stale.Error == nil || stale.Error.Code != "STALE_HEAD" {
		t.Fatalf("expected stale confirmation error: %+v", stale)
	}
	second := waitForOperation(t, client, api.URL, submitForTest(t, client, api.URL, "/self-reviews", "review-2", map[string]string{"pr_ref": "PR #17"}))
	if second.Result == nil || second.Result.Review == nil || len(second.Result.Review.Findings) != 0 || second.Result.Review.HeadSHA == first.HeadSHA {
		t.Fatalf("expected clean report for new revision: %+v", second)
	}
	updated := second.Result.Review
	confirmation["review_run_id"] = updated.ID
	receipt := waitForOperation(t, client, api.URL, submitForTest(t, client, api.URL, "/review-requests", "confirmed-1", confirmation))
	if receipt.Result == nil || receipt.Result.ReviewRequest == nil || receipt.Result.ReviewRequest.HeadSHA != updated.HeadSHA {
		t.Fatalf("expected receipt for current revision: %+v", receipt)
	}

	reopened, err := repository.NewStore(statePath)
	if err != nil {
		t.Fatal(err)
	}
	restoredApp := service.NewService(reopened, git, localAssistant, teams)
	restoredAPI := httptest.NewServer(httpapi.NewHTTPHandler(restoredApp, http.NotFoundHandler(), nil))
	t.Cleanup(restoredAPI.Close)
	var restored domain.Workspace
	apiCall(t, client, "GET", restoredAPI.URL+"/api/v1/workspace", "", nil, http.StatusOK, &restored)
	if len(restored.Reviews) != 2 || restored.ReviewRequests["e2-engineers#17"].ID != receipt.Result.ReviewRequest.ID {
		t.Fatalf("expected saved reviews and receipt after reopening: %+v", restored)
	}
	var doc string
	apiCall(t, client, "GET", restoredAPI.URL+"/api/v1/context/documents/review-rules", "", nil, http.StatusOK, &doc)
	if !strings.Contains(doc, `id="GO-ERR-01"`) {
		t.Fatal("rule link target missing from document")
	}
}

func useFixedRevision(t *testing.T, path string, initial []byte) {
	t.Helper()
	var fixture map[string]json.RawMessage
	if err := json.Unmarshal(initial, &fixture); err != nil {
		t.Fatal(err)
	}
	fixture["current_revision"] = json.RawMessage(`"fixed"`)
	data, err := json.Marshal(fixture)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
}

func sessionClient(t *testing.T) *http.Client {
	t.Helper()
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	return &http.Client{Jar: jar, Timeout: 6 * time.Second}
}

func submitForTest(t *testing.T, client *http.Client, base, path, key string, payload any) string {
	t.Helper()
	var accepted struct {
		ID string `json:"operation_id"`
	}
	apiCall(t, client, "POST", base+"/api/v1"+path, key, payload, http.StatusAccepted, &accepted)
	if accepted.ID == "" {
		t.Fatal("missing accepted operation ID")
	}
	return accepted.ID
}

func waitForOperation(t *testing.T, client *http.Client, base, id string) domain.Operation {
	t.Helper()
	for deadline := time.Now().Add(8 * time.Second); time.Now().Before(deadline); {
		var operation domain.Operation
		apiCall(t, client, "GET", base+"/api/v1/operations/"+id, "", nil, http.StatusOK, &operation)
		if operation.State != "QUEUED" && operation.State != "RUNNING" {
			return operation
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("operation %s did not finish", id)
	return domain.Operation{}
}

func apiCall(t *testing.T, client *http.Client, method, url, key string, payload any, want int, output any) {
	t.Helper()
	var body io.Reader
	if payload != nil {
		encoded, err := json.Marshal(payload)
		if err != nil {
			t.Fatal(err)
		}
		body = bytes.NewReader(encoded)
	}
	req, err := http.NewRequest(method, url, body)
	if err != nil {
		t.Fatal(err)
	}
	if key != "" {
		req.Header.Set("Idempotency-Key", key)
		req.Header.Set("Content-Type", "application/json")
	}
	res, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	data, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatal(err)
	}
	if res.StatusCode != want {
		t.Fatalf("%s %s returned %d, want %d: %s", method, url, res.StatusCode, want, data)
	}
	if text, ok := output.(*string); ok {
		*text = string(data)
	} else if output != nil {
		if err := json.Unmarshal(data, output); err != nil {
			t.Fatalf("decode %s: %v", data, err)
		}
	}
}
