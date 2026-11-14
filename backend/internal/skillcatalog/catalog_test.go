package skillcatalog

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadVersionedSkillAndRejectInvalidDefinitions(t *testing.T) {
	root := t.TempDir()
	directory := filepath.Join(root, "skills", "example")
	if err := os.MkdirAll(directory, 0700); err != nil {
		t.Fatal(err)
	}
	manifest := `{"id":"example","version":"1.0.0","contract":"message.v1","description":"Example","target":{"id":"internal-example","version":"2.0.0"},"triggers":[" Help Me "]}`
	write := func(name, data string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(directory, name), []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
	}
	write("skill.json", manifest)
	write("SKILL.md", "Trusted instructions")
	skill, err := Load(root, "example")
	if err != nil || skill.Target.ID != "internal-example" || skill.Triggers[0] != "help me" || skill.Instructions != "Trusted instructions" {
		t.Fatalf("unexpected definition: %+v, %v", skill, err)
	}
	for _, data := range []string{
		strings.Replace(manifest, `"id":"example"`, `"id":"another"`, 1),
		strings.Replace(manifest, `"1.0.0"`, `"latest"`, 1),
		strings.Replace(manifest, `message.v1`, `unknown.v1`, 1),
		strings.Replace(manifest, `" Help Me "`, `" "`, 1),
		strings.TrimSuffix(manifest, "}") + `,"executable":"cmd.exe"}`,
		manifest + `{}`,
	} {
		write("skill.json", data)
		if _, err := Load(root, "example"); err == nil {
			t.Fatalf("invalid manifest accepted: %s", data)
		}
	}
	write("skill.json", manifest)
	write("SKILL.md", " ")
	if _, err := Load(root, "example"); err == nil {
		t.Fatal("empty instructions accepted")
	}
	for _, id := range []string{"../example", `..\example`, "", "Example"} {
		if _, err := Load(root, id); err == nil {
			t.Fatalf("invalid id accepted: %s", id)
		}
	}
}
