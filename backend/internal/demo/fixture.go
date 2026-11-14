package demo

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/wantemira/e2-engineers/backend/internal/domain"
	"github.com/wantemira/e2-engineers/backend/internal/storage/jsonfile"
)

type State struct {
	EnvironmentStatus string                   `json:"environment_status,omitempty"`
	Revision          string                   `json:"revision,omitempty"`
	Checks            map[string]domain.Checks `json:"checks,omitempty"`
}

func Load(dataDir string) (State, error) {
	data, err := os.ReadFile(filepath.Join(dataDir, "demo.json"))
	if errors.Is(err, os.ErrNotExist) {
		return State{Checks: map[string]domain.Checks{}}, nil
	}
	if err != nil {
		return State{}, fmt.Errorf("read demo state: %w", err)
	}
	var state State
	if err := json.Unmarshal(data, &state); err != nil {
		return State{}, fmt.Errorf("decode demo state: %w", err)
	}
	if state.Checks == nil {
		state.Checks = map[string]domain.Checks{}
	}
	return state, nil
}

// Configure atomically updates the demo controls read by the running MCP server.
func Configure(root, dataDir, revision, checks string, reset bool) error {
	if revision != "" && revision != "initial" && revision != "fixed" {
		return fmt.Errorf("revision must be initial or fixed")
	}
	labels := map[string]string{
		"UNKNOWN": "Данных о CI пока нет",
		"RUNNING": "Тестовая проверка CI выполняется",
		"FAILED":  "Тестовая проверка CI завершилась ошибкой",
		"PASSED":  "Тестовая проверка CI пройдена",
	}
	if checks != "" && labels[checks] == "" {
		return fmt.Errorf("checks must be UNKNOWN, RUNNING, FAILED or PASSED")
	}
	if reset && (revision != "" || checks != "") {
		return fmt.Errorf("reset is used without revision or checks")
	}
	if !reset && revision == "" && checks == "" {
		return fmt.Errorf("specify revision, checks or reset")
	}

	data, err := os.ReadFile(filepath.Join(root, "mocks", "pr-17.json"))
	if err != nil {
		return fmt.Errorf("read demo fixture: %w", err)
	}
	var fixture struct {
		CurrentRevision string                     `json:"current_revision"`
		Revisions       map[string]json.RawMessage `json:"revisions"`
	}
	if err := json.Unmarshal(data, &fixture); err != nil {
		return fmt.Errorf("decode demo fixture: %w", err)
	}
	for _, required := range []string{"initial", "fixed"} {
		if _, ok := fixture.Revisions[required]; !ok {
			return fmt.Errorf("demo fixture is missing revision %s", required)
		}
	}
	var state State
	if reset {
		state = State{Revision: "initial", Checks: map[string]domain.Checks{}, EnvironmentStatus: "STOPPED"}
		// Empty overrides restore the revision-specific fixture (failed / passed).
	} else {
		state, err = Load(dataDir)
		if err != nil {
			return err
		}
	}
	if revision != "" {
		state.Revision = revision
	}
	current := state.Revision
	if current == "" {
		current = fixture.CurrentRevision
	}
	if _, ok := fixture.Revisions[current]; !ok {
		return fmt.Errorf("demo has an unknown current revision")
	}
	if checks != "" {
		state.Checks[current] = domain.Checks{Status: checks, Label: labels[checks]}
	}
	if err := jsonfile.Write(filepath.Join(dataDir, "demo.json"), state); err != nil {
		return fmt.Errorf("write demo state: %w", err)
	}
	return nil
}

func ValidEnvironmentStatus(status string) bool {
	switch status {
	case "STOPPED", "STARTING", "READY", "FAILED":
		return true
	default:
		return false
	}
}

func SetEnvironment(dataDir, status string) error {
	if !ValidEnvironmentStatus(status) {
		return fmt.Errorf("environment must be STOPPED, STARTING, READY or FAILED")
	}
	state, err := Load(dataDir)
	if err != nil {
		return err
	}
	state.EnvironmentStatus = status
	return jsonfile.Write(filepath.Join(dataDir, "demo.json"), state)
}
