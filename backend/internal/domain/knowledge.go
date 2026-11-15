package domain

// Search hits contain references to team documents, never caller supplied URLs.
type KnowledgeHit struct {
	Reference string     `json:"reference"`
	Title     string     `json:"title"`
	Excerpt   string     `json:"excerpt"`
	Source    SourceLink `json:"source"`
}
type KnowledgeSearch struct {
	Query string         `json:"query"`
	Hits  []KnowledgeHit `json:"hits"`
}
type Investigation struct {
	Question     string          `json:"question,omitempty"`
	Purpose      string          `json:"purpose,omitempty"`
	Search       KnowledgeSearch `json:"search"`
	Observations []string        `json:"observations"`
	CheckedAt    string          `json:"checked_at"`
}
