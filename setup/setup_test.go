package setup_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/lucashalbert/axi-sdk-go/setup"
)

func readJSON(t *testing.T, path string) map[string]any {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("invalid json: %v\n%s", err, b)
	}
	return m
}

func sessionStartCommands(t *testing.T, m map[string]any) []string {
	t.Helper()
	var cmds []string
	groups := m["hooks"].(map[string]any)["SessionStart"].([]any)
	for _, g := range groups {
		for _, h := range g.(map[string]any)["hooks"].([]any) {
			cmds = append(cmds, h.(map[string]any)["command"].(string))
		}
	}
	return cmds
}

func TestInstallHooksCreatesFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "settings.json")
	changed, err := setup.InstallHooks(path, "k8s-axi cluster info")
	if err != nil || !changed {
		t.Fatalf("changed=%v err=%v", changed, err)
	}
	cmds := sessionStartCommands(t, readJSON(t, path))
	if len(cmds) != 1 || cmds[0] != "k8s-axi cluster info" {
		t.Errorf("cmds = %v", cmds)
	}
	g := readJSON(t, path)["hooks"].(map[string]any)["SessionStart"].([]any)[0].(map[string]any)["hooks"].([]any)[0].(map[string]any)
	if g["type"] != "command" {
		t.Errorf("hook = %v", g)
	}
}

func TestInstallHooksIsIdempotent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	if _, err := setup.InstallHooks(path, "tool status"); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(path)
	changed, err := setup.InstallHooks(path, "tool status")
	if err != nil || changed {
		t.Fatalf("second install: changed=%v err=%v", changed, err)
	}
	after, _ := os.ReadFile(path)
	if string(before) != string(after) {
		t.Error("file rewritten although nothing changed")
	}
	if cmds := sessionStartCommands(t, readJSON(t, path)); len(cmds) != 1 {
		t.Errorf("cmds = %v", cmds)
	}
}

func TestInstallHooksPreservesUnrelatedSettings(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	existing := `{
  "model": "opus",
  "permissions": {"allow": ["Bash(ls)"]},
  "hooks": {
    "PreToolUse": [{"matcher": "Bash", "hooks": [{"type": "command", "command": "guard"}]}],
    "SessionStart": [{"hooks": [{"type": "command", "command": "other-tool"}]}]
  }
}`
	if err := os.WriteFile(path, []byte(existing), 0o600); err != nil {
		t.Fatal(err)
	}
	changed, err := setup.InstallHooks(path, "k8s-axi cluster info")
	if err != nil || !changed {
		t.Fatalf("changed=%v err=%v", changed, err)
	}
	m := readJSON(t, path)
	if m["model"] != "opus" {
		t.Errorf("model lost: %v", m["model"])
	}
	if _, ok := m["permissions"].(map[string]any)["allow"]; !ok {
		t.Error("permissions lost")
	}
	if _, ok := m["hooks"].(map[string]any)["PreToolUse"]; !ok {
		t.Error("PreToolUse hooks lost")
	}
	cmds := sessionStartCommands(t, m)
	if strings.Join(cmds, ",") != "other-tool,k8s-axi cluster info" {
		t.Errorf("cmds = %v", cmds)
	}
}

func TestInstallHooksEmptyFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	if err := os.WriteFile(path, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if changed, err := setup.InstallHooks(path, "x"); err != nil || !changed {
		t.Fatalf("changed=%v err=%v", changed, err)
	}
}

func TestInstallHooksRefusesToClobber(t *testing.T) {
	tests := map[string]string{
		"invalid json":         `{"model": `,
		"not an object":        `[1,2]`,
		"hooks not object":     `{"hooks": []}`,
		"SessionStart not arr": `{"hooks": {"SessionStart": {}}}`,
	}
	for name, content := range tests {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "settings.json")
			if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
				t.Fatal(err)
			}
			if changed, err := setup.InstallHooks(path, "x"); err == nil || changed {
				t.Fatalf("changed=%v err=%v, want error", changed, err)
			}
			if got, _ := os.ReadFile(path); string(got) != content {
				t.Errorf("file modified: %q", got)
			}
		})
	}
}

func TestInstallHooksRequiresCommand(t *testing.T) {
	if _, err := setup.InstallHooks(filepath.Join(t.TempDir(), "s.json"), " "); err == nil {
		t.Error("want error for empty command")
	}
}

func TestHooksSnippetMatchesInstall(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	if _, err := setup.InstallHooks(path, "tool status"); err != nil {
		t.Fatal(err)
	}
	var snippet map[string]any
	if err := json.Unmarshal([]byte(setup.HooksSnippet("tool status")), &snippet); err != nil {
		t.Fatal(err)
	}
	want, _ := json.Marshal(readJSON(t, path))
	got, _ := json.Marshal(snippet)
	if string(want) != string(got) {
		t.Errorf("snippet %s != installed %s", got, want)
	}
}

func TestInstallSkillCopiesTree(t *testing.T) {
	fsys := fstest.MapFS{
		"SKILL.md":            {Data: []byte("# skill")},
		"refs/deep/notes.txt": {Data: []byte("notes")},
	}
	dest := filepath.Join(t.TempDir(), "skills", "tool")
	if err := setup.InstallSkill(fsys, dest); err != nil {
		t.Fatal(err)
	}
	for rel, want := range map[string]string{"SKILL.md": "# skill", "refs/deep/notes.txt": "notes"} {
		got, err := os.ReadFile(filepath.Join(dest, filepath.FromSlash(rel)))
		if err != nil || string(got) != want {
			t.Errorf("%s = %q, %v", rel, got, err)
		}
	}
}

func TestInstallSkillOverwritesAndIsRepeatable(t *testing.T) {
	dest := t.TempDir()
	if err := setup.InstallSkill(fstest.MapFS{"SKILL.md": {Data: []byte("v1")}}, dest); err != nil {
		t.Fatal(err)
	}
	if err := setup.InstallSkill(fstest.MapFS{"SKILL.md": {Data: []byte("v2")}}, dest); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(filepath.Join(dest, "SKILL.md")); string(got) != "v2" {
		t.Errorf("got %q", got)
	}
}

func TestInstallSkillValidatesInput(t *testing.T) {
	if err := setup.InstallSkill(fstest.MapFS{"a": {Data: []byte("x")}}, ""); err == nil {
		t.Error("want error for empty dest")
	}
	if err := setup.InstallSkill(fstest.MapFS{}, t.TempDir()); err == nil {
		t.Error("want error for empty skill")
	}
}
