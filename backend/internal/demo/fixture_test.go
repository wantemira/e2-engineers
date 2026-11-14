package demo_test

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/wantemira/e2-engineers/backend/internal/demo"
)

func TestDemoControlsSwitchChecksAndResetWithoutChangingFixture(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "mocks"), 0700); err != nil {
		t.Fatal(err)
	}
	original, err := os.ReadFile("../../../mocks/pr-17.json")
	if err != nil {
		t.Fatal(err)
	}
	// Reset must override an already selected corrected fixture too.
	var fixture map[string]json.RawMessage
	if err := json.Unmarshal(original, &fixture); err != nil {
		t.Fatal(err)
	}
	fixture["current_revision"] = json.RawMessage(`"fixed"`)
	original, err = json.Marshal(fixture)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "mocks", "pr-17.json")
	if err := os.WriteFile(path, original, 0600); err != nil {
		t.Fatal(err)
	}
	dataDir := filepath.Join(root, "runtime")

	if err := demo.Configure(root, dataDir, "", "", true); err != nil {
		t.Fatal(err)
	}
	if err := demo.Configure(root, dataDir, "fixed", "", false); err != nil {
		t.Fatal(err)
	}
	if err := demo.SetEnvironment(dataDir, "READY"); err != nil {
		t.Fatal(err)
	}
	for _, status := range []string{"RUNNING", "FAILED", "PASSED"} {
		if err := demo.Configure(root, dataDir, "", status, false); err != nil {
			t.Fatal(err)
		}
		state, err := demo.Load(dataDir)
		if err != nil || state.Revision != "fixed" || state.Checks["fixed"].Status != status {
			t.Fatalf("lost demo selection: %+v, %v", state, err)
		}
	}
	before, err := os.ReadFile(filepath.Join(dataDir, "demo.json"))
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		revision, checks string
		reset            bool
	}{
		{"other", "", false}, {"", "GREEN", false},
		{"", "", false}, {"fixed", "", true},
	} {
		if err := demo.Configure(root, dataDir, tc.revision, tc.checks, tc.reset); err == nil {
			t.Fatalf("invalid demo command was accepted: %+v", tc)
		}
	}
	after, err := os.ReadFile(filepath.Join(dataDir, "demo.json"))
	if err != nil || !bytes.Equal(before, after) {
		t.Fatal("invalid command changed demo state")
	}
	if err := demo.Configure(root, dataDir, "", "", true); err != nil {
		t.Fatal(err)
	}
	state, err := demo.Load(dataDir)
	if err != nil || state.Revision != "initial" || state.Checks["initial"].Status != "UNKNOWN" || state.Checks["fixed"].Status != "UNKNOWN" || state.EnvironmentStatus != "STOPPED" {
		t.Fatalf("reset did not restore the starting scenario: %+v, %v", state, err)
	}
	after, err = os.ReadFile(path)
	if err != nil || !bytes.Equal(original, after) {
		t.Fatal("demo controls changed the source fixture")
	}
}
