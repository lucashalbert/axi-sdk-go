package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// buildEcho compiles the example once per test and returns the binary path.
func buildEcho(t *testing.T) string {
	t.Helper()
	name := "echo"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	bin := filepath.Join(t.TempDir(), name)
	cmd := exec.CommandContext(t.Context(), "go", "build", "-o", bin, ".")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("go build: %v\n%s", err, out)
	}
	return bin
}

type result struct {
	out, errOut string
	code        int
}

func runEcho(t *testing.T, bin, file string, args ...string) result {
	t.Helper()
	cmd := exec.CommandContext(t.Context(), bin, append([]string{"--file", file}, args...)...)
	cmd.Env = append(os.Environ(), "ECHO_NO_UPDATE_CHECK=1")
	var o, e bytes.Buffer
	cmd.Stdout, cmd.Stderr = &o, &e
	err := cmd.Run()
	r := result{out: o.String(), errOut: e.String()}
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		r.code = ee.ExitCode()
	} else if err != nil {
		t.Fatal(err)
	}
	return r
}

func readState(t *testing.T, file string) map[string]string {
	t.Helper()
	b, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	m := map[string]string{}
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	return m
}

func TestEchoEndToEnd(t *testing.T) {
	bin := buildEcho(t)
	file := filepath.Join(t.TempDir(), "state.json")

	t.Run("read command on missing key is a runtime error", func(t *testing.T) {
		r := runEcho(t, bin, file, "get", "greeting")
		if r.code != 1 || r.out != "" || !strings.Contains(r.errOut, "not found") {
			t.Fatalf("%+v", r)
		}
	})

	t.Run("dry run prints the plan and changes nothing", func(t *testing.T) {
		r := runEcho(t, bin, file, "set", "greeting", "hello", "--dry-run")
		if r.code != 0 || r.errOut != "" || !strings.HasPrefix(r.out, "plan:\n  operation: set\n  target: entry/greeting\n  scope:\n") ||
			!strings.Contains(r.out, "  changes[1]: create greeting = hello\n  destructive: false\n  mode: dry-run\n") {
			t.Fatalf("%+v", r)
		}
		if _, err := os.Stat(file); !os.IsNotExist(err) {
			t.Fatalf("dry run created the state file: %v", err)
		}
	})

	t.Run("set prints the plan then applies", func(t *testing.T) {
		r := runEcho(t, bin, file, "set", "greeting", "hello", "--json")
		if r.code != 0 || !strings.HasPrefix(r.out, `{"plan":{"operation":"set","target":"entry/greeting"`) ||
			!strings.Contains(r.out, `"mode":"apply"`) {
			t.Fatalf("%+v", r)
		}
		if got := readState(t, file)["greeting"]; got != "hello" {
			t.Fatalf("state = %q", got)
		}
	})

	t.Run("set treats null state as empty", func(t *testing.T) {
		nullFile := filepath.Join(t.TempDir(), "state.json")
		if err := os.WriteFile(nullFile, []byte("null\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		r := runEcho(t, bin, nullFile, "set", "from-null", "ok", "--json")
		if r.code != 0 || !strings.Contains(r.out, `"changes":["create from-null = ok"]`) {
			t.Fatalf("%+v", r)
		}
		if got := readState(t, nullFile)["from-null"]; got != "ok" {
			t.Fatalf("state = %q", got)
		}
	})

	t.Run("set with the same value is a no-op", func(t *testing.T) {
		r := runEcho(t, bin, file, "set", "greeting", "hello", "--yaml")
		if r.code != 0 || !strings.Contains(r.out, "noop: true") || !strings.Contains(r.out, "changes: []") {
			t.Fatalf("%+v", r)
		}
	})

	t.Run("read command", func(t *testing.T) {
		r := runEcho(t, bin, file, "get", "greeting")
		if r.code != 0 || r.out != "key: greeting\nvalue: hello\n" {
			t.Fatalf("%+v", r)
		}
	})

	t.Run("delete without a token is a usage error naming the token", func(t *testing.T) {
		r := runEcho(t, bin, file, "delete", "greeting")
		if r.code != 2 || r.out != "" || !strings.Contains(r.errOut, "--confirm entry/greeting") {
			t.Fatalf("%+v", r)
		}
		if readState(t, file)["greeting"] != "hello" {
			t.Fatal("entry was deleted without confirmation")
		}
	})

	t.Run("delete with a mismatched token is a usage error", func(t *testing.T) {
		r := runEcho(t, bin, file, "delete", "greeting", "--confirm", "entry/other")
		if r.code != 2 || !strings.Contains(r.errOut, "does not match") || !strings.Contains(r.errOut, "--confirm entry/greeting") {
			t.Fatalf("%+v", r)
		}
	})

	t.Run("destructive dry run needs no token and shows it", func(t *testing.T) {
		r := runEcho(t, bin, file, "delete", "greeting", "--dry-run", "--json")
		if r.code != 0 || !strings.Contains(r.out, `"confirm":"entry/greeting"`) {
			t.Fatalf("%+v", r)
		}
		if readState(t, file)["greeting"] != "hello" {
			t.Fatal("dry run deleted the entry")
		}
	})

	t.Run("delete with the exact token applies", func(t *testing.T) {
		r := runEcho(t, bin, file, "delete", "greeting", "--confirm", "entry/greeting")
		if r.code != 0 || !strings.Contains(r.out, "target: entry/greeting") {
			t.Fatalf("%+v", r)
		}
		if _, ok := readState(t, file)["greeting"]; ok {
			t.Fatal("entry still present")
		}
	})

	t.Run("secrets are redacted in plans", func(t *testing.T) {
		r := runEcho(t, bin, file, "set", "cfg", "password=hunter2hunter2", "--dry-run")
		if r.code != 0 || strings.Contains(r.out, "hunter2") || !strings.Contains(r.out, "[REDACTED]") {
			t.Fatalf("%+v", r)
		}
	})

	t.Run("shared commands come from the SDK", func(t *testing.T) {
		r := runEcho(t, bin, file, "version", "--json")
		if r.code != 0 || !strings.Contains(r.out, `"version":"dev"`) {
			t.Fatalf("%+v", r)
		}
		r = runEcho(t, bin, file, "set", "k", "v", "--json", "--yaml")
		if r.code != 2 {
			t.Fatalf("conflicting formats: %+v", r)
		}
	})
}
