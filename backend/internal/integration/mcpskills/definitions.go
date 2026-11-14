package mcpskills

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"

	"github.com/wantemira/e2-engineers/backend/internal/domain"
	"github.com/wantemira/e2-engineers/backend/internal/skillcatalog"
)

type definition struct {
	domain.SkillRef
	Contract string                    `json:"contract"`
	Executor string                    `json:"executor"`
	Output   domain.MessageSkillOutput `json:"output,omitempty"`
}

type configuration struct {
	AllowedActors []string     `json:"allowed_actors"`
	Skills        []definition `json:"skills"`
}

func loadDefinitions(path string) (configuration, error) {
	var config configuration
	data, err := os.ReadFile(path)
	if err != nil {
		return config, fmt.Errorf("read internal skills: %w", err)
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&config); err != nil {
		return config, fmt.Errorf("decode internal skills: %w", err)
	}
	if decoder.Decode(new(any)) != io.EOF || len(config.AllowedActors) == 0 || len(config.Skills) == 0 {
		return config, fmt.Errorf("internal skills require actors and definitions")
	}
	seen := map[domain.SkillRef]bool{}
	for _, skill := range config.Skills {
		if !skillcatalog.ValidRef(skill.SkillRef) || seen[skill.SkillRef] {
			return config, fmt.Errorf("invalid or duplicate internal skill %s", skill.ID)
		}
		seen[skill.SkillRef] = true
		adapter, ok := executors[skill.Executor]
		if !ok || adapter.contract != skill.Contract || (adapter.validate != nil && !adapter.validate(skill)) {
			return config, fmt.Errorf("unsupported executor/contract for internal skill %s", skill.ID)
		}
	}
	return config, nil
}
