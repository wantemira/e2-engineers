package teamconfig

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/wantemira/e2-engineers/backend/internal/domain"
)

func loadTestTeam(t *testing.T) domain.Team {
	t.Helper()
	root, err := filepath.Abs("../../..")
	if err != nil {
		t.Fatal(err)
	}
	team, err := New(root).Load()
	if err != nil {
		t.Fatal(err)
	}
	return team
}
func TestLoadTeam(t *testing.T) {
	team := loadTestTeam(t)
	if team.ID != "backend" || team.PRNumber != 17 || team.Mentor.ID != "mentor-backend" || team.RepositoryURL != "https://github.com/wantemira/e2-engineers" {
		t.Fatalf("unexpected team configuration: %+v", team)
	}
	if len(team.Documents) != 4 || team.Skill == "" || team.Rules["GO-ERR-01"].ID != "GO-ERR-01" {
		t.Fatalf("team knowledge was not loaded: %+v", team)
	}
}

func TestLoadTeamRejectsDocumentTraversal(t *testing.T) {
	root := t.TempDir()
	directory := filepath.Join(root, "teams", "demo-backend")
	if err := os.MkdirAll(directory, 0755); err != nil {
		t.Fatal(err)
	}
	original, err := os.ReadFile("../../../teams/demo-backend/context.json")
	if err != nil {
		t.Fatal(err)
	}
	data := strings.Replace(string(original), `"file": "context.md"`, `"file": "../../outside.md"`, 1)
	if err := os.WriteFile(filepath.Join(directory, "context.json"), []byte(data), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := New(root).Load(); err == nil || !strings.Contains(err.Error(), "invalid document") {
		t.Fatalf("expected document path validation, got %v", err)
	}
}

func TestLoadTeamMissingConfiguration(t *testing.T) {
	if _, err := New(t.TempDir()).Load(); err == nil {
		t.Fatal("missing configuration must fail")
	}
}
