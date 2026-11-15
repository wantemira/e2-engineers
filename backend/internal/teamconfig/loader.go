package teamconfig

import (
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/wantemira/e2-engineers/backend/internal/domain"
	"github.com/wantemira/e2-engineers/backend/internal/skillcatalog"
	"github.com/wantemira/e2-engineers/backend/internal/teamhelp"
)

type configuredDocument struct {
	ID    string `json:"id"`
	Title string `json:"title"`
	File  string `json:"file"`
}

type teamConfiguration struct {
	Help           *domain.HelpCatalog     `json:"help"`
	Skills         []string                `json:"skills"`
	Capabilities   map[string]string       `json:"capabilities"`
	Training       *domain.TrainingContext `json:"training"`
	PeopleAgentURL string                  `json:"people_agent_url"`
	ID             string                  `json:"id"`
	Name           string                  `json:"name"`
	Repository     string                  `json:"repository"`
	RepositoryURL  string                  `json:"repository_url"`
	PRNumber       int                     `json:"pr_number"`
	Mentor         domain.Mentor           `json:"mentor"`
	Documents      []configuredDocument    `json:"documents"`
	Rules          map[string]domain.Rule  `json:"rules"`
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
	if configuration.PeopleAgentURL != "" {
		target, err := url.Parse(configuration.PeopleAgentURL)
		if err != nil || (target.Scheme != "https" && target.Scheme != "http") || target.Hostname() == "" || target.User != nil {
			return domain.Team{}, fmt.Errorf("people_agent_url must be an absolute HTTP(S) URL")
		}
	}
	team := domain.Team{
		Help:           configuration.Help,
		Training:       configuration.Training,
		PeopleAgentURL: configuration.PeopleAgentURL,
		ID:             configuration.ID, Name: configuration.Name, Repository: configuration.Repository,
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
	if training := team.Training; training != nil {
		if training.Service == "" || training.Environment == "" || !seen[training.GuideID] || training.StartCommand == "" || training.TestCommand == "" {
			return domain.Team{}, fmt.Errorf("training configuration is missing service, environment, guide or commands")
		}
	}
	for _, id := range []string{"GO-ERR-01", "GO-TEST-01"} {
		if _, ok := team.Rules[id]; !ok {
			return domain.Team{}, fmt.Errorf("missing team rule %s", id)
		}
	}
	for id, rule := range team.Rules {
		if rule.ID != id || rule.Label == "" || rule.URL != "/api/v1/context/documents/review-rules#"+id {
			return domain.Team{}, fmt.Errorf("invalid team rule %s", id)
		}
	}
	team.Capabilities = configuration.Capabilities
	enabled := make(map[string]domain.SkillDefinition)
	var triggers []string
	for _, id := range configuration.Skills {
		if _, duplicate := enabled[id]; duplicate {
			return domain.Team{}, fmt.Errorf("duplicate skill %s", id)
		}
		skill, err := skillcatalog.Load(loader.projectRoot, id)
		if err != nil {
			return domain.Team{}, err
		}
		for _, trigger := range skill.Triggers {
			for _, previous := range triggers {
				if strings.Contains(trigger, previous) || strings.Contains(previous, trigger) {
					return domain.Team{}, fmt.Errorf("ambiguous skill trigger %q", trigger)
				}
			}
			triggers = append(triggers, trigger)
		}
		enabled[id] = skill
		team.Skills = append(team.Skills, skill)
	}
	for capability, id := range team.Capabilities {
		if _, ok := enabled[id]; !ok || capability == "" {
			return domain.Team{}, fmt.Errorf("capability %s refers to a disabled skill %s", capability, id)
		}
	}
	review, ok := enabled[team.Capabilities["pr_review"]]
	if !ok || review.Contract != domain.ReviewSkillContract {
		return domain.Team{}, fmt.Errorf("pr_review requires an enabled %s skill", domain.ReviewSkillContract)
	}
	if err := teamhelp.Validate(team); err != nil {
		return domain.Team{}, err
	}
	return team, nil
}
