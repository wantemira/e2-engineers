package domain

type HelpCatalog struct {
	Topics     []HelpTopic      `json:"topics"`
	Access     []AccessResource `json:"access"`
	Assistants []AssistantRoute `json:"assistants"`
}

type HelpTopic struct {
	ID         string   `json:"id"`
	Keywords   []string `json:"keywords"`
	DocumentID string   `json:"document_id"`
	Section    string   `json:"section"`
}

type AccessResource struct {
	ID         string   `json:"id"`
	Name       string   `json:"name"`
	Aliases    []string `json:"aliases"`
	DocumentID string   `json:"document_id"`
	Section    string   `json:"section"`
	RequestURL string   `json:"request_url"`
}

type AssistantRoute struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Keywords    []string `json:"keywords"`
	DocumentID  string   `json:"document_id"`
	Section     string   `json:"section"`
	URL         string   `json:"url,omitempty"`
}

type HelpTrace struct {
	EscalationOffered  bool           `json:"escalation_offered,omitempty"`
	Investigation      *Investigation `json:"investigation,omitempty"`
	Kind               string         `json:"kind"`
	TargetID           string         `json:"target_id,omitempty"`
	Skill              SkillRef       `json:"skill"`
	InstructionsSHA256 string         `json:"instructions_sha256"`
}
