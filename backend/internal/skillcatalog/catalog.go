// Package skillcatalog loads versioned, declarative skill bindings from Git.
package skillcatalog

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/wantemira/e2-engineers/backend/internal/domain"
)

var identifier = regexp.MustCompile(`^[a-z][a-z0-9-]{0,63}$`)
var version = regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+$`)

func ValidRef(ref domain.SkillRef) bool {
	return identifier.MatchString(ref.ID) && version.MatchString(ref.Version)
}

func Load(projectRoot, id string) (domain.SkillDefinition, error) {
	var skill domain.SkillDefinition
	if !identifier.MatchString(id) {
		return skill, fmt.Errorf("invalid skill id %q", id)
	}
	root, err := os.OpenRoot(filepath.Join(projectRoot, "skills", id))
	if err != nil {
		return skill, fmt.Errorf("open skill %s: %w", id, err)
	}
	defer root.Close()
	data, err := root.ReadFile("skill.json")
	if err != nil {
		return skill, fmt.Errorf("read skill %s: %w", id, err)
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&skill); err != nil {
		return skill, fmt.Errorf("decode skill %s: %w", id, err)
	}
	if decoder.Decode(new(any)) != io.EOF || skill.ID != id || !ValidRef(skill.SkillRef) || !ValidRef(skill.Target) || strings.TrimSpace(skill.Description) == "" {
		return skill, fmt.Errorf("invalid skill manifest %s", id)
	}
	if skill.Contract != domain.ReviewSkillContract && skill.Contract != domain.MessageSkillContract {
		return skill, fmt.Errorf("unsupported skill contract %q", skill.Contract)
	}
	if skill.Contract == domain.ReviewSkillContract && len(skill.Triggers) != 0 {
		return skill, fmt.Errorf("review skills use the pr_review capability, not chat triggers")
	}
	for i, trigger := range skill.Triggers {
		trigger = strings.ToLower(strings.TrimSpace(trigger))
		if trigger == "" || len(trigger) > 200 {
			return skill, fmt.Errorf("invalid chat trigger in skill %s", id)
		}
		skill.Triggers[i] = trigger
	}
	instructions, err := root.ReadFile("SKILL.md")
	if err != nil {
		return skill, fmt.Errorf("read instructions for %s: %w", id, err)
	}
	if len(instructions) > 64*1024 || strings.TrimSpace(string(instructions)) == "" {
		return skill, fmt.Errorf("skill %s instructions must contain 1..65536 bytes", id)
	}
	skill.Instructions = string(instructions)
	return skill, nil
}
