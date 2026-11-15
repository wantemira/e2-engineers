package teamconfig

import (
	"encoding/json"
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
	if len(team.Documents) != 8 || len(team.Skills) != 2 || team.Skills[0].Instructions == "" || team.Rules["GO-ERR-01"].ID != "GO-ERR-01" || team.Help == nil {
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

func TestSkillBindingsRejectDisabledAndDuplicateSkills(t *testing.T) {
	root := t.TempDir()
	for _, directory := range []string{"teams", "skills"} {
		if err := os.CopyFS(filepath.Join(root, directory), os.DirFS(filepath.Join("../../..", directory))); err != nil {
			t.Fatal(err)
		}
	}
	path := filepath.Join(root, "teams", "demo-backend", "context.json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var configuration map[string]any
	if err := json.Unmarshal(data, &configuration); err != nil {
		t.Fatal(err)
	}
	for _, ids := range [][]string{{}, {"pr-self-review", "pr-self-review"}, {"../outside"}} {
		configuration["skills"] = ids
		data, err := json.Marshal(configuration)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, data, 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := New(root).Load(); err == nil {
			t.Fatalf("invalid enabled skills accepted: %v", ids)
		}
	}
}
