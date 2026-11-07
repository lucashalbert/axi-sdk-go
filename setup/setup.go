// Package setup installs the pieces that make an AXI tool usable by agents:
// its skill and a session-start hook in the agent's settings.json.
package setup

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// InstallSkill copies every regular file under the root of fsys into dest,
// creating directories and overwriting files that exist, so it can be re-run
// to upgrade a skill. Typically fsys is an embed.FS narrowed with fs.Sub.
func InstallSkill(fsys fs.FS, dest string) error {
	if dest == "" {
		return errors.New("setup: skill destination is empty")
	}
	var copied int
	err := fs.WalkDir(fsys, ".", func(name string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		target := filepath.Join(dest, filepath.FromSlash(name))
		switch {
		case d.IsDir():
			return os.MkdirAll(target, 0o750)
		case d.Type().IsRegular():
			b, err := fs.ReadFile(fsys, name)
			if err != nil {
				return err
			}
			copied++
			return writeFileAtomic(target, b, 0o644)
		}
		return nil // symlinks and devices are not part of a skill
	})
	if err != nil {
		return fmt.Errorf("setup: install skill to %s: %w", dest, err)
	}
	if copied == 0 {
		return errors.New("setup: skill has no files")
	}
	return nil
}

func hookGroup(command string) map[string]any {
	return map[string]any{"hooks": []any{map[string]any{"type": "command", "command": command}}}
}

// HooksSnippet returns the settings.json fragment InstallHooks adds, for
// tools that offer a print-only mode.
func HooksSnippet(command string) string {
	b, _ := json.MarshalIndent(map[string]any{"hooks": map[string]any{"SessionStart": []any{hookGroup(command)}}}, "", "  ")
	return string(b) + "\n"
}

// InstallHooks adds a SessionStart hook running command to the agent
// settings file at settingsPath, creating the file if needed. It reports
// whether the file changed: a second call with the same command is a no-op.
// Unrelated settings are preserved. A file that is not the expected shape is
// an error and is left untouched.
func InstallHooks(settingsPath, command string) (changed bool, err error) {
	if strings.TrimSpace(command) == "" {
		return false, errors.New("setup: hook command is empty")
	}
	doc := map[string]json.RawMessage{}
	mode := os.FileMode(0o600)
	b, err := os.ReadFile(settingsPath) //nolint:gosec // path is the caller-chosen settings file
	switch {
	case err == nil:
		if st, serr := os.Stat(settingsPath); serr == nil {
			mode = st.Mode().Perm()
		}
		if len(bytes.TrimSpace(b)) > 0 {
			if err := json.Unmarshal(b, &doc); err != nil {
				return false, fmt.Errorf("setup: %s is not a JSON object: %w", settingsPath, err)
			}
		}
	case !errors.Is(err, fs.ErrNotExist):
		return false, err
	}

	hooks := map[string]json.RawMessage{}
	if raw, ok := doc["hooks"]; ok {
		if err := json.Unmarshal(raw, &hooks); err != nil || hooks == nil {
			return false, fmt.Errorf("setup: %s: \"hooks\" is not an object", settingsPath)
		}
	}
	var groups []json.RawMessage
	if raw, ok := hooks["SessionStart"]; ok {
		if err := json.Unmarshal(raw, &groups); err != nil {
			return false, fmt.Errorf("setup: %s: \"hooks.SessionStart\" is not an array", settingsPath)
		}
	}
	for _, g := range groups {
		if hasCommand(g, command) {
			return false, nil
		}
	}

	group, _ := json.Marshal(hookGroup(command))
	groups = append(groups, group)
	if hooks["SessionStart"], err = json.Marshal(groups); err != nil {
		return false, err
	}
	if doc["hooks"], err = json.Marshal(hooks); err != nil {
		return false, err
	}
	out, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return false, err
	}
	if err := writeFileAtomic(settingsPath, append(out, '\n'), mode); err != nil {
		return false, err
	}
	return true, nil
}

func hasCommand(group json.RawMessage, command string) bool {
	var g struct {
		Hooks []struct {
			Command string `json:"command"`
		} `json:"hooks"`
	}
	if json.Unmarshal(group, &g) != nil {
		return false
	}
	for _, h := range g.Hooks {
		if h.Command == command {
			return true
		}
	}
	return false
}

// writeFileAtomic writes via a temp file and rename so a crash never leaves
// a half-written settings file.
func writeFileAtomic(path string, data []byte, mode os.FileMode) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".axi-setup-*")
	if err != nil {
		return err
	}
	_, werr := tmp.Write(data)
	cerr := tmp.Close()
	if err := errors.Join(werr, cerr, os.Chmod(tmp.Name(), mode)); err != nil {
		_ = os.Remove(tmp.Name())
		return err
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		_ = os.Remove(tmp.Name())
		return err
	}
	return nil
}
