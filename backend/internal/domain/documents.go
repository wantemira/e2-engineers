package domain

import (
	"fmt"
	"strings"
)

type DocumentSection struct{ ID, Title, Body string }

// Sections gives local documents stable numbered anchors; existing rule IDs stay valid.
func Sections(document Document) []DocumentSection {
	intro, rest, _ := strings.Cut(document.Body, "\n## ")
	if strings.HasPrefix(intro, "# ") {
		_, intro, _ = strings.Cut(intro, "\n")
	}
	sections := []DocumentSection{}
	if body := strings.TrimSpace(intro); body != "" {
		sections = append(sections, DocumentSection{Body: body})
	}
	if rest != "" {
		for index, part := range strings.Split(rest, "\n## ") {
			title, body, _ := strings.Cut(part, "\n")
			title = strings.TrimSpace(title)
			sections = append(sections, DocumentSection{ID: fmt.Sprintf("section-%d", index+1), Title: title, Body: strings.TrimSpace(body)})
		}
	}
	return sections
}

// DocumentSource accepts document-id or document-id#exact-heading, never a user URL.
func DocumentSource(team Team, reference string) (SourceLink, string, bool) {
	id, heading, hasHeading := strings.Cut(reference, "#")
	for _, document := range team.Documents {
		if document.ID != id {
			continue
		}
		link := SourceLink{Label: document.Title, URL: "/api/v1/context/documents/" + document.ID}
		if !hasHeading {
			return link, document.Body, true
		}
		for _, section := range Sections(document) {
			if section.Title == heading && heading != "" {
				anchor := section.ID
				if rule, ok := team.Rules[heading]; ok {
					anchor = rule.ID
				}
				link.Label += " · " + heading
				link.URL += "#" + anchor
				return link, section.Body, true
			}
		}
	}
	return SourceLink{}, "", false
}
