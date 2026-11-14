package domain

import (
	"bytes"
	"encoding/json"
)

const (
	ReviewSkillContract  = "pr-review.v1"
	MessageSkillContract = "message.v1"
)

type SkillRef struct {
	ID      string `json:"id"`
	Version string `json:"version"`
}

// SkillDefinition is trusted team configuration, never supplied by a chat message.
type SkillDefinition struct {
	SkillRef
	Contract     string   `json:"contract"`
	Description  string   `json:"description"`
	Target       SkillRef `json:"target"`
	Triggers     []string `json:"triggers,omitempty"`
	Instructions string   `json:"-"`
}

type SkillCall struct {
	Skill        SkillRef       `json:"skill"`
	Contract     string         `json:"contract"`
	Instructions string         `json:"instructions"`
	Input        map[string]any `json:"input"`
}

type SkillReply struct {
	Skill              SkillRef       `json:"skill"`
	Contract           string         `json:"contract"`
	Source             string         `json:"source"`
	RunID              string         `json:"run_id"`
	InstructionsSHA256 string         `json:"instructions_sha256"`
	Output             map[string]any `json:"output"`
}

type SkillExecution struct {
	Skill              SkillRef `json:"skill"`
	Target             SkillRef `json:"target"`
	Contract           string   `json:"contract"`
	Source             string   `json:"source"`
	RunID              string   `json:"run_id"`
	InstructionsSHA256 string   `json:"instructions_sha256"`
}

type ReviewSkillInput struct {
	PRNumber int    `json:"pr_number"`
	HeadSHA  string `json:"head_sha"`
}

type ReviewSkillOutput struct {
	PRNumber int            `json:"pr_number"`
	HeadSHA  string         `json:"head_sha"`
	Checks   Checks         `json:"checks"`
	Analysis ReviewAnalysis `json:"analysis"`
}

type MessageSkillInput struct {
	Text string `json:"text"`
}

type MessageSkillOutput struct {
	Text string `json:"text"`
}

func SkillPayload(value any) (map[string]any, error) {
	data, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	var payload map[string]any
	err = json.Unmarshal(data, &payload)
	return payload, err
}

func DecodeSkillPayload(payload map[string]any, value any) error {
	data, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	return decoder.Decode(value)
}
