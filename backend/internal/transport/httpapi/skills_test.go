package httpapi_test

import (
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/wantemira/e2-engineers/backend/internal/assistant"
	"github.com/wantemira/e2-engineers/backend/internal/integration/mcpskills"
	"github.com/wantemira/e2-engineers/backend/internal/service"
	"github.com/wantemira/e2-engineers/backend/internal/teamconfig"
)

func skillTestClient(t *testing.T, root string, git service.GitTools) *mcpskills.Client {
	t.Helper()
	handler, err := mcpskills.NewHandler(filepath.Join(root, "mocks", "skills.json"), "skill-secret", git, teamconfig.New(root), assistant.NewLocalAssistant())
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	return mcpskills.NewClient(server.URL, "skill-secret")
}
