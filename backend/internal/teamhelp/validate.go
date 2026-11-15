package teamhelp

import (
	"fmt"
	"net/url"
	"regexp"
	"strings"

	"github.com/wantemira/e2-engineers/backend/internal/domain"
)

var identifier = regexp.MustCompile(`^[a-z][a-z0-9-]{0,63}$`)

func Validate(team domain.Team) error {
	if team.Help == nil {
		return nil
	}
	if !Enabled(team) {
		return fmt.Errorf("help catalog requires an enabled team_help capability")
	}
	seen := map[string]bool{}
	check := func(kind, id, document, section string) error {
		key := kind + ":" + id
		if !identifier.MatchString(id) || seen[key] {
			return fmt.Errorf("invalid or duplicate help target %s", key)
		}
		seen[key] = true
		_, body, ok := domain.DocumentSource(team, document+"#"+section)
		if section == "" || !ok || strings.TrimSpace(body) == "" {
			return fmt.Errorf("help target %s references a missing section", key)
		}
		return nil
	}
	words := func(values []string) bool {
		if len(values) == 0 {
			return false
		}
		for _, value := range values {
			if strings.TrimSpace(value) == "" || len(value) > 200 {
				return false
			}
		}
		return true
	}
	validURL := func(value string) bool {
		if strings.HasPrefix(value, "/api/v1/context/documents/") {
			for _, document := range team.Documents {
				link, _, _ := domain.DocumentSource(team, document.ID)
				if value == link.URL {
					return true
				}
				for _, section := range domain.Sections(document) {
					link, _, ok := domain.DocumentSource(team, document.ID+"#"+section.Title)
					if ok && value == link.URL {
						return true
					}
				}
			}
			return false
		}
		u, err := url.Parse(value)
		return err == nil && (u.Scheme == "https" || u.Scheme == "http") && u.Hostname() != "" && u.User == nil
	}
	for _, topic := range team.Help.Topics {
		if err := check("docs", topic.ID, topic.DocumentID, topic.Section); err != nil {
			return err
		}
		if !words(topic.Keywords) {
			return fmt.Errorf("invalid help topic keywords")
		}
	}
	aliases := map[string]bool{}
	for _, resource := range team.Help.Access {
		if err := check("access", resource.ID, resource.DocumentID, resource.Section); err != nil {
			return err
		}
		if strings.TrimSpace(resource.Name) == "" || !validURL(resource.RequestURL) || !words(resource.Aliases) {
			return fmt.Errorf("invalid access resource %s", resource.ID)
		}
		for _, alias := range append([]string{resource.Name}, resource.Aliases...) {
			alias = strings.ToLower(alias)
			if aliases[alias] {
				return fmt.Errorf("ambiguous access alias %s", alias)
			}
			aliases[alias] = true
		}
	}
	for _, route := range team.Help.Assistants {
		if err := check("handoff", route.ID, route.DocumentID, route.Section); err != nil {
			return err
		}
		if strings.TrimSpace(route.Name) == "" || strings.TrimSpace(route.Description) == "" || !words(route.Keywords) || (route.URL != "" && !validURL(route.URL)) {
			return fmt.Errorf("invalid assistant route %s", route.ID)
		}
	}
	return nil
}
