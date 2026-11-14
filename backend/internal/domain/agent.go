package domain

type AssistantInfo struct {
	Mode  string `json:"mode"`
	Model string `json:"model,omitempty"`
}

// AgentDecision permits read scenarios and synthetic CI; review requests require UI confirmation.
type AgentDecision struct {
	RunChecks bool
	Tool      string
	PRRef     string
	SkillID   string
	Message   Message
}

type AgentTrace struct {
	Model string `json:"model"`
	Tool  string `json:"tool"`
}
