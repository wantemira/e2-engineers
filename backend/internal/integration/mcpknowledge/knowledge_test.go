package mcpknowledge_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/wantemira/e2-engineers/backend/internal/domain"
	"github.com/wantemira/e2-engineers/backend/internal/integration/mcpknowledge"
	"github.com/wantemira/e2-engineers/backend/internal/teamconfig"
)

func TestSearchReadsCurrentTeamDocumentsAndDoesNotGuess(t *testing.T) {
	team, err := teamconfig.New("../../../..").Load()
	if err != nil {
		t.Fatal(err)
	}
	found := mcpknowledge.Search(team, "Не могу запустить orders: 502")
	if len(found.Hits) == 0 || found.Hits[0].Reference != "troubleshooting#Orders: ответ 502" || !strings.Contains(found.Hits[0].Excerpt, "make training-up") {
		t.Fatalf("bad search: %+v", found)
	}
	if len(mcpknowledge.Search(team, "Как получить доступ к Kubernetes?").Hits) != 0 || len(mcpknowledge.Search(team, "Не могу получить доступ к Kubernetes").Hits) != 0 {
		t.Fatal("unrelated access instructions returned")
	}
	team.Documents = []domain.Document{{ID: "new", Title: "New source", Body: "# New\n\n## Payments\n\npayments timeout UPDATED"}}
	found = mcpknowledge.Search(team, "payments timeout")
	if len(found.Hits) != 1 || !strings.Contains(found.Hits[0].Excerpt, "UPDATED") {
		t.Fatal("new data requires code changes")
	}
	if len(mcpknowledge.Search(team, "orders 502").Hits) != 0 {
		t.Fatal("search leaked documents from another team snapshot")
	}
}
func TestMCPKnowledgeRequiresAuthenticationAndAuthorizedActor(t *testing.T) {
	api := httptest.NewServer(mcpknowledge.NewHandler("secret", teamconfig.New("../../../..")))
	defer api.Close()
	response, err := http.Get(api.URL)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != 401 {
		t.Fatal("anonymous MCP access allowed")
	}
	for _, tc := range []struct {
		actor, secret, query string
		ok                   bool
	}{
		{"intern-1", "secret", "orders 502", true}, {"other-intern", "secret", "orders 502", false}, {"intern-1", "wrong", "orders 502", false}, {"intern-1", "secret", strings.Repeat("x", 2001), false},
	} {
		result, err := mcpknowledge.NewClient(api.URL, tc.secret).Search(context.Background(), tc.actor, tc.query)
		if tc.ok && (err != nil || len(result.Hits) == 0) {
			t.Fatalf("authorized search failed: %v %+v", err, result)
		}
		if !tc.ok && err == nil {
			t.Fatal("unauthorized or invalid search accepted")
		}
	}
}

func TestGenericCISectionsDoNotAnswerUnknownProductAccess(t *testing.T) {
	team, err := teamconfig.New("../../../..").Load()
	if err != nil {
		t.Fatal(err)
	}
	for _, question := range []string{"Как получить доступ к GitLab CI CD?", "Нужен доступ к UnknownProduct CI"} {
		if hits := mcpknowledge.Search(team, question).Hits; len(hits) != 0 {
			t.Fatalf("generic CI matched unknown product: %+v", hits)
		}
	}
	if len(mcpknowledge.Search(team, "про графану что знаешь?").Hits) == 0 {
		t.Fatal("configured alias was not searched")
	}
}
