package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/spf13/cobra"
	"go.yaml.in/yaml/v3"

	"github.com/lucashalbert/axi-sdk-go/axierr"
	"github.com/lucashalbert/axi-sdk-go/update"
)

var info = update.Info{Version: "0.1.0", Commit: "abc1234", Date: "2026-11-07T10:00:00Z"}

type fakes struct {
	checkCalls int
	checkCfg   update.CheckConfig
	latest     string
	newer      bool
	checkErr   error
	installed  update.InstallConfig
	instLatest string
	instUpd    bool
	instErr    error
}

func (f *fakes) option() Option {
	return option(func(s *settings) {
		s.cacheFile = "/tmp/axi-test/update-check.json"
		s.check = func(_ context.Context, c update.CheckConfig) (string, bool, error) {
			f.checkCalls++
			f.checkCfg = c
			return f.latest, f.newer, f.checkErr
		}
		s.install = func(_ context.Context, c update.InstallConfig) (string, bool, error) {
			f.installed = c
			return f.instLatest, f.instUpd, f.instErr
		}
	})
}

type echoModule struct{ registered *cobra.Command }

func (m *echoModule) Register(root *cobra.Command) {
	m.registered = root
	root.AddCommand(&cobra.Command{Use: "echo", RunE: func(cmd *cobra.Command, _ []string) error {
		_, _ = cmd.OutOrStdout().Write([]byte("hello\n"))
		return nil
	}})
	root.AddCommand(&cobra.Command{Use: "boom", RunE: func(*cobra.Command, []string) error {
		return errors.New("kaboom")
	}})
}

func run(t *testing.T, f *fakes, args ...string) (stdout, stderr string, code int) {
	t.Helper()
	t.Setenv("TOOL_X_NO_UPDATE_CHECK", "")
	root := New("tool-x", info, f.option(), Repo("o/tool-x"), &echoModule{})
	var out, errb bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&errb)
	root.SetArgs(args)
	code = Execute(root)
	return out.String(), errb.String(), code
}

func TestVersionAllFormats(t *testing.T) {
	f := &fakes{}
	out, errOut, code := run(t, f, "version")
	if want := "version: 0.1.0\ncommit: abc1234\ndate: \"2026-11-07T10:00:00Z\"\n"; out != want || code != 0 || errOut != "" {
		t.Errorf("toon: code=%d out=%q err=%q", code, out, errOut)
	}

	out, _, _ = run(t, f, "version", "--json")
	var j map[string]string
	if err := json.Unmarshal([]byte(out), &j); err != nil || j["version"] != "0.1.0" || j["commit"] != "abc1234" || j["date"] != "2026-11-07T10:00:00Z" {
		t.Errorf("json: %q %v", out, err)
	}

	out, _, _ = run(t, f, "version", "--yaml")
	var y map[string]string
	if err := yaml.Unmarshal([]byte(out), &y); err != nil || y["version"] != "0.1.0" || y["commit"] != "abc1234" || y["date"] != "2026-11-07T10:00:00Z" {
		t.Errorf("yaml: %q %v", out, err)
	}
}

func TestVersionDefaultsForUnstampedBuild(t *testing.T) {
	root := New("tool-x", update.Info{}, Repo("o/x"))
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetArgs([]string{"version"})
	if Execute(root) != 0 || out.String() != "version: dev\ncommit: unknown\ndate: unknown\n" {
		t.Errorf("out = %q", out.String())
	}
}

func TestConflictingFormatFlagsExit2(t *testing.T) {
	out, errOut, code := run(t, &fakes{}, "version", "--json", "--yaml")
	if code != 2 || !strings.Contains(out, "mutually exclusive") || errOut != "" {
		t.Errorf("code=%d out=%q err=%q", code, out, errOut)
	}
}

func TestUsageErrorsExit2(t *testing.T) {
	for _, args := range [][]string{{"--nope"}, {"version", "--nope"}, {"nosuchcommand"}, {"version", "extra"}} {
		out, errOut, code := run(t, &fakes{}, args...)
		if code != 2 || errOut != "" || !strings.Contains(out, "error:") || !strings.Contains(out, "exit: 2") {
			t.Errorf("%v: code=%d out=%q err=%q", args, code, out, errOut)
		}
	}
}

func TestParseFailureHonorsFormatFlags(t *testing.T) {
	for _, args := range [][]string{{"--bogus", "--json"}, {"version", "--bogus", "--json"}, {"nosuch", "--json=true"}} {
		out, _, code := run(t, &fakes{}, args...)
		var j map[string]map[string]any
		if err := json.Unmarshal([]byte(out), &j); err != nil || code != 2 || j["error"]["exit"] != float64(2) {
			t.Errorf("%v json: code=%d out=%q err=%v", args, code, out, err)
		}
	}
	out, _, code := run(t, &fakes{}, "version", "--bogus", "--yaml")
	var y map[string]map[string]any
	if err := yaml.Unmarshal([]byte(out), &y); err != nil || code != 2 || y["error"]["exit"] != 2 {
		t.Errorf("yaml: code=%d out=%q err=%v", code, out, err)
	}
	for _, args := range [][]string{{"version", "--bogus"}, {"version", "--bogus", "--json", "--yaml"}, {"version", "--bogus", "--json=false"}, {"version", "--bogus", "--", "--json"}} {
		out, _, code = run(t, &fakes{}, args...)
		if code != 2 || !strings.HasPrefix(out, "error:\n") {
			t.Errorf("%v toon: code=%d out=%q", args, code, out)
		}
	}
}

func TestRuntimeErrorExit1AsJSON(t *testing.T) {
	out, errOut, code := run(t, &fakes{}, "boom", "--json")
	if code != 1 || errOut != "" || out != `{"error":{"op":"tool-x","message":"kaboom","exit":1}}`+"\n" {
		t.Errorf("code=%d out=%q err=%q", code, out, errOut)
	}
}

func TestErrorsGoToStdoutInEveryFormat(t *testing.T) {
	out, errOut, code := run(t, &fakes{}, "boom")
	if code != 1 || errOut != "" || out != "error:\n  op: tool-x\n  message: kaboom\n  exit: 1\n" {
		t.Errorf("toon: code=%d out=%q err=%q", code, out, errOut)
	}
	out, errOut, code = run(t, &fakes{}, "boom", "--yaml")
	var y map[string]map[string]any
	if err := yaml.Unmarshal([]byte(out), &y); err != nil || code != 1 || errOut != "" ||
		y["error"]["message"] != "kaboom" || y["error"]["exit"] != 1 {
		t.Errorf("yaml: code=%d out=%q err=%q %v", code, out, errOut, err)
	}
	out, errOut, code = run(t, &fakes{}, "--json", "version", "--nope")
	var j map[string]map[string]any
	if err := json.Unmarshal([]byte(out), &j); err != nil || code != 2 || errOut != "" ||
		j["error"]["exit"] != float64(2) || j["error"]["hint"] == "" {
		t.Errorf("json usage: code=%d out=%q err=%q %v", code, out, errOut, err)
	}
}

func TestErrorOutputIsRedacted(t *testing.T) {
	root := New("tool-x", info, Repo("o/x"), moduleFunc(func(r *cobra.Command) {
		r.AddCommand(&cobra.Command{Use: "leak", RunE: func(*cobra.Command, []string) error {
			return errors.New("auth failed: password=hunter2hunter2")
		}})
	}))
	var out, errb bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&errb)
	root.SetArgs([]string{"leak"})
	if code := Execute(root); code != 1 || strings.Contains(out.String(), "hunter2") || errb.Len() != 0 {
		t.Errorf("code=%d out=%q err=%q", code, out.String(), errb.String())
	}
}

type moduleFunc func(*cobra.Command)

func (f moduleFunc) Register(r *cobra.Command) { f(r) }

func TestVersionFlags(t *testing.T) {
	for _, flag := range []string{"--version", "-v", "-V"} {
		out, errOut, code := run(t, &fakes{latest: "0.2.0", newer: true}, flag)
		if code != 0 || out != "0.1.0\n" {
			t.Errorf("%s: code=%d out=%q", flag, code, out)
		}
		_ = errOut // the update notice may use stderr
	}
	root := New("tool-x", update.Info{}, Repo("o/x"))
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetArgs([]string{"-v"})
	if Execute(root) != 0 || out.String() != "dev\n" {
		t.Errorf("unstamped out = %q", out.String())
	}
}

func TestVersionFlagsDoNotCollide(t *testing.T) {
	// A subcommand's own -v/-V and the version subcommand are untouched.
	root := New("tool-x", info, Repo("o/x"), moduleFunc(func(r *cobra.Command) {
		c := &cobra.Command{Use: "sub", RunE: func(cmd *cobra.Command, _ []string) error {
			v, _ := cmd.Flags().GetBool("verbose")
			_, _ = cmd.OutOrStdout().Write([]byte(map[bool]string{true: "verbose\n", false: "quiet\n"}[v]))
			return nil
		}}
		c.Flags().BoolP("verbose", "v", false, "")
		r.AddCommand(c)
	}))
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetArgs([]string{"sub", "-v"})
	if Execute(root) != 0 || out.String() != "verbose\n" {
		t.Errorf("sub -v out = %q", out.String())
	}
	if o, _, code := run(t, &fakes{}, "version"); code != 0 || !strings.HasPrefix(o, "version: 0.1.0") {
		t.Errorf("version cmd: %d %q", code, o)
	}
	rootWithV := func() *cobra.Command {
		return New("tool-x", info, Repo("o/x"), moduleFunc(func(r *cobra.Command) {
			r.Flags().BoolP("verbose", "v", false, "")
			r.RunE = func(cmd *cobra.Command, _ []string) error {
				verbose, _ := cmd.Flags().GetBool("verbose")
				_, _ = cmd.OutOrStdout().Write([]byte(map[bool]string{true: "verbose\n", false: "quiet\n"}[verbose]))
				return nil
			}
		}))
	}
	for _, flag := range []string{"--version", "-V"} {
		root = rootWithV()
		out.Reset()
		root.SetOut(&out)
		root.SetArgs([]string{flag})
		if Execute(root) != 0 || out.String() != "0.1.0\n" {
			t.Errorf("%s out = %q", flag, out.String())
		}
	}
	root = rootWithV()
	out.Reset()
	root.SetOut(&out)
	root.SetArgs([]string{"-v"})
	if Execute(root) != 0 || out.String() != "verbose\n" {
		t.Errorf("-v out = %q", out.String())
	}
}

func TestVersionFastPathIgnoresModuleOverrides(t *testing.T) {
	for _, tc := range []struct {
		name string
		mod  Module
		flag string
	}{
		{
			name: "module version flag",
			mod: moduleFunc(func(r *cobra.Command) {
				r.Flags().Bool("version", false, "")
			}),
			flag: "--version",
		},
		{
			name: "module persistent version flag",
			mod: moduleFunc(func(r *cobra.Command) {
				r.PersistentFlags().Bool("version", false, "")
			}),
			flag: "--version",
		},
		{
			name: "module V shorthand",
			mod: moduleFunc(func(r *cobra.Command) {
				r.Flags().BoolP("verbose", "V", false, "")
			}),
			flag: "-V",
		},
		{
			name: "module persistent V shorthand",
			mod: moduleFunc(func(r *cobra.Command) {
				r.PersistentFlags().BoolP("verbose", "V", false, "")
			}),
			flag: "-V",
		},
		{
			name: "module hidden V",
			mod: moduleFunc(func(r *cobra.Command) {
				r.Flags().Bool("V", false, "")
			}),
			flag: "-V",
		},
		{
			name: "module hooks",
			mod: moduleFunc(func(r *cobra.Command) {
				r.PersistentPreRunE = func(*cobra.Command, []string) error { return nil }
				r.RunE = func(cmd *cobra.Command, _ []string) error {
					_, _ = cmd.OutOrStdout().Write([]byte("module\n"))
					return nil
				}
			}),
			flag: "--version",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := New("tool-x", info, Repo("o/x"), tc.mod)
			var out bytes.Buffer
			root.SetOut(&out)
			root.SetArgs([]string{tc.flag})
			if Execute(root) != 0 || out.String() != "0.1.0\n" {
				t.Fatalf("%s out = %q", tc.flag, out.String())
			}
		})
	}
}

func TestModuleIsRegisteredAndOptionsAreNot(t *testing.T) {
	m := &echoModule{}
	root := New("tool-x", info, Repo("o/x"), m)
	if m.registered != root {
		t.Error("module not given the root")
	}
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetArgs([]string{"echo"})
	if Execute(root) != 0 || out.String() != "hello\n" {
		t.Errorf("out = %q", out.String())
	}
}

func TestNoticeGoesToStderrOnly(t *testing.T) {
	f := &fakes{latest: "0.2.0", newer: true}
	out, errOut, code := run(t, f, "version")
	if code != 0 {
		t.Fatalf("code = %d", code)
	}
	if strings.Contains(out, "available") {
		t.Errorf("notice leaked to stdout: %q", out)
	}
	if want := "tool-x 0.2.0 is available (running 0.1.0); run 'tool-x update' to install it\n"; errOut != want {
		t.Errorf("stderr = %q", errOut)
	}
	// stdout is identical with and without the notice.
	plain, _, _ := run(t, &fakes{}, "version")
	if out != plain {
		t.Errorf("stdout changed by notice: %q vs %q", out, plain)
	}
}

func TestNoticeUsesDailyCachedBoundedCheck(t *testing.T) {
	f := &fakes{latest: "0.2.0", newer: true}
	run(t, f, "version")
	c := f.checkCfg
	if c.Repo != "o/tool-x" || c.Current != "0.1.0" || c.TTL != 24*time.Hour || c.Timeout != 2*time.Second ||
		!strings.HasSuffix(c.CacheFile, "update-check.json") {
		t.Errorf("check config = %+v", c)
	}
}

func TestNoticeSilentWhenNotNewerOrCheckFails(t *testing.T) {
	_, errOut, code := run(t, &fakes{latest: "0.1.0"}, "version")
	if errOut != "" || code != 0 {
		t.Errorf("not newer: code=%d err=%q", code, errOut)
	}
	_, errOut, code = run(t, &fakes{checkErr: errors.New("offline")}, "version")
	if errOut != "" || code != 0 {
		t.Errorf("offline: code=%d err=%q", code, errOut)
	}
}

func TestNoticeNotShownWhenCommandFailsOrIsUpdate(t *testing.T) {
	f := &fakes{latest: "0.2.0", newer: true, instLatest: "0.2.0", instUpd: true}
	if _, errOut, _ := run(t, f, "update"); strings.Contains(errOut, "is available") || f.checkCalls != 0 {
		t.Errorf("update printed a notice or checked: %q calls=%d", errOut, f.checkCalls)
	}
	f = &fakes{latest: "0.2.0", newer: true}
	if _, errOut, _ := run(t, f, "boom"); strings.Contains(errOut, "is available") {
		t.Errorf("notice after failed command: %q", errOut)
	}
}

func TestNoticeDisabledByEnv(t *testing.T) {
	for _, v := range []string{"1", "true", "yes"} {
		f := &fakes{latest: "0.2.0", newer: true}
		t.Setenv("TOOL_X_NO_UPDATE_CHECK", v)
		root := New("tool-x", info, f.option(), Repo("o/x"))
		var errb bytes.Buffer
		root.SetOut(&bytes.Buffer{})
		root.SetErr(&errb)
		root.SetArgs([]string{"version"})
		if Execute(root) != 0 || errb.Len() != 0 || f.checkCalls != 0 {
			t.Errorf("%s: err=%q checks=%d", v, errb.String(), f.checkCalls)
		}
	}
}

func TestEnvPrefixOverrideAndDefault(t *testing.T) {
	if got := defaultEnvPrefix("k8s-axi"); got != "K8S_AXI_" {
		t.Errorf("prefix = %q", got)
	}
	f := &fakes{latest: "0.2.0", newer: true}
	t.Setenv("CUSTOM_NO_UPDATE_CHECK", "1")
	root := New("tool-x", info, f.option(), Repo("o/x"), EnvPrefix("CUSTOM_"))
	root.SetOut(&bytes.Buffer{})
	root.SetErr(&bytes.Buffer{})
	root.SetArgs([]string{"version"})
	Execute(root)
	if f.checkCalls != 0 {
		t.Error("custom env prefix not honoured")
	}
}

func TestNoRepoMeansNoNoticeAndUpdateIsUsageError(t *testing.T) {
	f := &fakes{latest: "0.2.0", newer: true}
	s := &settings{name: "tool-x", info: info, envPrefix: "TOOL_X_", cacheFile: "/tmp/x/c.json"}
	f.option().apply(s)
	cmd := s.versionCmd()
	cmd.SetErr(&bytes.Buffer{})
	s.notice(cmd, nil)
	if f.checkCalls != 0 {
		t.Error("checked without a repo")
	}
	up := s.updateCmd()
	up.SetOut(&bytes.Buffer{})
	err := up.RunE(up, nil)
	if err == nil || axierr.ExitCode(err) != 2 {
		t.Errorf("err = %v", err)
	}
}

func TestUpdateCommand(t *testing.T) {
	f := &fakes{instLatest: "0.2.0", instUpd: true}
	out, _, code := run(t, f, "update", "--json")
	if code != 0 || out != `{"current":"0.1.0","latest":"0.2.0","updated":true,"message":"installed 0.2.0"}`+"\n" {
		t.Errorf("code=%d out=%q", code, out)
	}
	if f.installed.Repo != "o/tool-x" || f.installed.Binary != "tool-x" || f.installed.Current != "0.1.0" {
		t.Errorf("install config = %+v", f.installed)
	}
	out, _, _ = run(t, &fakes{instLatest: "0.1.0"}, "update", "--json")
	if !strings.Contains(out, `"updated":false`) || !strings.Contains(out, "already up to date") {
		t.Errorf("out = %q", out)
	}
}

func TestUpdateFailureIsStructuredRuntimeError(t *testing.T) {
	f := &fakes{instErr: errors.New("checksum mismatch for x.tar.gz")}
	out, errOut, code := run(t, f, "update")
	if code != 1 || errOut != "" || !strings.Contains(out, "checksum mismatch") || !strings.Contains(out, "not changed") {
		t.Errorf("code=%d out=%q err=%q", code, out, errOut)
	}
}

func TestRepoFromModulePath(t *testing.T) {
	tests := map[string]string{
		"github.com/lucashalbert/k8s-axi":         "lucashalbert/k8s-axi",
		"github.com/lucashalbert/k8s-axi/cmd/foo": "lucashalbert/k8s-axi",
		"example.com/a/b":                         "",
		"github.com/onlyowner":                    "",
		"":                                        "",
	}
	for in, want := range tests {
		if got := repoFromModulePath(in); got != want {
			t.Errorf("%q = %q, want %q", in, got, want)
		}
	}
}
