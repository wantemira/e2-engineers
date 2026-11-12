package teamconfig

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/wantemira/e2-engineers/backend/internal/domain"
)

type configuredDocument struct {
	ID    string `json:"id"`
	Title string `json:"title"`
	File  string `json:"file"`
}

type teamConfiguration struct {
	ID            string                 `json:"id"`
	Name          string                 `json:"name"`
	Repository    string                 `json:"repository"`
	RepositoryURL string                 `json:"repository_url"`
	PRNumber      int                    `json:"pr_number"`
	Mentor        domain.Mentor          `json:"mentor"`
	Documents     []configuredDocument   `json:"documents"`
	Rules         map[string]domain.Rule `json:"rules"`
}

type Loader struct {
	projectRoot string
}

func New(projectRoot string) *Loader {
	return &Loader{projectRoot: projectRoot}
}

func (loader *Loader) Load() (domain.Team, error) {
	directory := filepath.Join(loader.projectRoot, "teams", "demo-backend")
	data, err := os.ReadFile(filepath.Join(directory, "context.json"))
	if err != nil {
		return domain.Team{}, fmt.Errorf("read team configuration: %w", err)
	}
	var configuration teamConfiguration
	if err := json.Unmarshal(data, &configuration); err != nil {
		return domain.Team{}, fmt.Errorf("decode team configuration: %w", err)
	}
	if configuration.ID == "" || configuration.Name == "" || configuration.Repository == "" || configuration.RepositoryURL == "" || configuration.PRNumber < 1 || configuration.Mentor.ID == "" || configuration.Mentor.Name == "" {
		return domain.Team{}, fmt.Errorf("team configuration has missing required fields")
	}
	team := domain.Team{
		ID: configuration.ID, Name: configuration.Name, Repository: configuration.Repository,
		PRNumber: configuration.PRNumber, Mentor: configuration.Mentor, Rules: configuration.Rules,
		RepositoryURL: configuration.RepositoryURL,
		Documents:     make([]domain.Document, 0, len(configuration.Documents)),
	}
	seen := make(map[string]bool)
	for _, document := range configuration.Documents {
		if document.ID == "" || document.Title == "" || document.File == "" || filepath.Base(document.File) != document.File || strings.ContainsAny(document.File, `/\`) || seen[document.ID] {
			return domain.Team{}, fmt.Errorf("invalid document configuration %q", document.ID)
		}
		body, err := os.ReadFile(filepath.Join(directory, document.File))
		if err != nil {
			return domain.Team{}, fmt.Errorf("read team document %s: %w", document.ID, err)
		}
		if strings.TrimSpace(string(body)) == "" {
			return domain.Team{}, fmt.Errorf("team document %s is empty", document.ID)
		}
		seen[document.ID] = true
		team.Documents = append(team.Documents, domain.Document{ID: document.ID, Title: document.Title, Body: string(body)})
	}
	for _, id := range []string{"context", "review-rules", "access-guide", "people-guide"} {
		if !seen[id] {
			return domain.Team{}, fmt.Errorf("required team document %s is missing", id)
		}
	}
	for _, id := range []string{"GO-ERR-01", "GO-TEST-01"} {
		rule, ok := team.Rules[id]
		if !ok || rule.ID != id || rule.Label == "" || rule.URL != "/api/v1/context/documents/review-rules#"+id {
			return domain.Team{}, fmt.Errorf("invalid team rule %s", id)
		}
	}
	skill, err := os.ReadFile(filepath.Join(loader.projectRoot, "skills", "pr-self-review", "SKILL.md"))
	if err != nil {
		return domain.Team{}, fmt.Errorf("read self-review skill: %w", err)
	}
	if strings.TrimSpace(string(skill)) == "" {
		return domain.Team{}, fmt.Errorf("self-review skill is empty")
	}
	team.Skill = string(skill)
	return team, nil
}
