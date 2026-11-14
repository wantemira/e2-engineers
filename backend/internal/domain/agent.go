package domain

type AssistantInfo struct {
	Mode  string `json:"mode"`
	Model string `json:"model,omitempty"`
}

// AgentDecision contains only read scenarios; review requests require UI confirmation.
type AgentDecision struct {
	Tool    string
	PRRef   string
	SkillID string
	Message Message
}

type AgentTrace struct {
	Model string `json:"model"`
	Tool  string `json:"tool"`
}
