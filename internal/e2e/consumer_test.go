package e2e

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"
)

func TestConsumerCLIBehavior(t *testing.T) {
	repo := repoRoot(t)
	bin := buildConsumer(t, repo)

	out, errOut, code := runConsumer(t, bin, "version")
	if code != 0 || errOut != "" || out != "version: 0.1.0\ncommit: abc1234\ndate: \"2026-11-07T10:00:00Z\"\n" {
		t.Fatalf("toon version: code=%d out=%q err=%q", code, out, errOut)
	}

	out, errOut, code = runConsumer(t, bin, "version", "--json")
	var j map[string]string
	if code != 0 || errOut != "" || json.Unmarshal([]byte(out), &j) != nil ||
		j["version"] != "0.1.0" || j["commit"] != "abc1234" || j["date"] != "2026-11-07T10:00:00Z" {
		t.Fatalf("json version: code=%d out=%q err=%q parsed=%v", code, out, errOut, j)
	}

	out, errOut, code = runConsumer(t, bin, "version", "--yaml")
	var y map[string]string
	if code != 0 || errOut != "" || yaml.Unmarshal([]byte(out), &y) != nil ||
		y["version"] != "0.1.0" || y["commit"] != "abc1234" || y["date"] != "2026-11-07T10:00:00Z" {
		t.Fatalf("yaml version: code=%d out=%q err=%q parsed=%v", code, out, errOut, y)
	}

	out, errOut, code = runConsumer(t, bin, "fail", "--json")
	if code != 1 || errOut != "" || !strings.Contains(out, "[REDACTED]") ||
		strings.Contains(out, "s3cr3t") || strings.Contains(out, "topsecret") || strings.Contains(out, "supersecret") {
		t.Fatalf("redacted error: code=%d out=%q err=%q", code, out, errOut)
	}
}

func TestConsumerSetupHelpers(t *testing.T) {
	bin := buildConsumer(t, repoRoot(t))
	dir := t.TempDir()
	source := filepath.Join(dir, "skill-src")
	dest := filepath.Join(dir, "skills", "consumer")
	must(t, os.MkdirAll(filepath.Join(source, "refs", "deep"), 0o755))
	must(t, os.WriteFile(filepath.Join(source, "SKILL.md"), []byte("# consumer\n"), 0o644))
	must(t, os.WriteFile(filepath.Join(source, "refs", "deep", "notes.txt"), []byte("notes\n"), 0o644))

	out, errOut, code := runConsumer(t, bin, "setup-skill", source, dest)
	if code != 0 || errOut != "" || strings.TrimSpace(out) != "installed" {
		t.Fatalf("setup-skill: code=%d out=%q err=%q", code, out, errOut)
	}
	for rel, want := range map[string]string{"SKILL.md": "# consumer\n", "refs/deep/notes.txt": "notes\n"} {
		got, err := os.ReadFile(filepath.Join(dest, filepath.FromSlash(rel)))
		if err != nil || string(got) != want {
			t.Fatalf("installed skill %s = %q, %v", rel, got, err)
		}
	}

	settings := filepath.Join(dir, "settings.json")
	out, errOut, code = runConsumer(t, bin, "setup-hooks", settings, "consumer", "status")
	if code != 0 || errOut != "" || strings.TrimSpace(out) != "changed" {
		t.Fatalf("first setup-hooks: code=%d out=%q err=%q", code, out, errOut)
	}
	before, err := os.ReadFile(settings)
	must(t, err)
	out, errOut, code = runConsumer(t, bin, "setup-hooks", settings, "consumer", "status")
	if code != 0 || errOut != "" || strings.TrimSpace(out) != "unchanged" {
		t.Fatalf("second setup-hooks: code=%d out=%q err=%q", code, out, errOut)
	}
	after, err := os.ReadFile(settings)
	must(t, err)
	if !bytes.Equal(before, after) {
		t.Fatal("idempotent hook install rewrote settings")
	}
	if got := sessionStartCommands(t, after); len(got) != 1 || got[0] != "consumer status" {
		t.Fatalf("SessionStart commands = %v", got)
	}
}

func TestConsumerUpdateCheckCacheIsRepoIsolated(t *testing.T) {
	bin := buildConsumer(t, repoRoot(t))
	cache := filepath.Join(t.TempDir(), "update-check.json")
	out, errOut, code := runConsumerWithEnv(t, bin, []string{"AXI_E2E_RELEASE_FIXTURE=" + checkFixture(t)}, "check-cache", cache)
	if code != 0 || errOut != "" || strings.TrimSpace(out) != "latest= newer=false" {
		t.Fatalf("check-cache: code=%d out=%q err=%q", code, out, errOut)
	}
}

func TestConsumerSelfUpdateRefusesUnsafeReleases(t *testing.T) {
	bin := buildConsumer(t, repoRoot(t))
	original, err := os.ReadFile(bin)
	must(t, err)
	archiveName, archiveBytes := archiveForRuntime(t, "consumer", []byte("NEW BINARY"))

	mismatch := newReleaseServer(t, releaseFixture{
		archiveName:  archiveName,
		archive:      archiveBytes,
		checksumName: "checksums.txt",
		checksums:    strings.Repeat("0", 64) + "  " + archiveName + "\n",
	})
	out, errOut, code := runConsumerWithEnv(t, bin, []string{"AXI_E2E_RELEASE_FIXTURE=" + mismatch}, "self-update")
	if code != 1 || errOut != "" || !strings.Contains(out, "checksum mismatch") {
		t.Fatalf("checksum mismatch: code=%d out=%q err=%q", code, out, errOut)
	}
	assertFileBytes(t, bin, original)

	aliasOnly := newReleaseServer(t, releaseFixture{
		archiveName:  archiveName,
		archive:      archiveBytes,
		checksumName: "consumer_checksums.txt",
		checksums:    sha256Hex(archiveBytes) + "  " + archiveName + "\n",
	})
	out, errOut, code = runConsumerWithEnv(t, bin, []string{"AXI_E2E_RELEASE_FIXTURE=" + aliasOnly}, "self-update")
	if code != 1 || errOut != "" || !strings.Contains(out, "checksums.txt") {
		t.Fatalf("checksum alias: code=%d out=%q err=%q", code, out, errOut)
	}
	assertFileBytes(t, bin, original)
}

func TestDependencyGraphHasNoKubernetesPackages(t *testing.T) {
	cmd := exec.CommandContext(t.Context(), "go", "list", "-deps", "-test", "./...")
	cmd.Dir = repoRoot(t)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("go list dependency graph failed: %v\n%s", err, out)
	}
	for _, dep := range strings.Fields(string(out)) {
		if strings.HasPrefix(dep, "k8s.io/") {
			t.Fatalf("axi-sdk-go must not import Kubernetes packages; found %s", dep)
		}
	}
}

func buildConsumer(t *testing.T, repo string) string {
	t.Helper()
	dir := t.TempDir()
	must(t, os.WriteFile(filepath.Join(dir, "go.mod"), []byte(fmt.Sprintf(`module e2e-consumer

go 1.27

require github.com/lucashalbert/axi-sdk-go v0.0.0

replace github.com/lucashalbert/axi-sdk-go => %s
`, filepath.ToSlash(repo))), 0o644))
	must(t, os.WriteFile(filepath.Join(dir, "main.go"), []byte(consumerMain), 0o644))
	runGo(t, dir, "mod", "tidy")
	bin := filepath.Join(dir, exeName("consumer"))
	runGo(t, dir, "build", "-o", bin, ".")
	return bin
}

func runGo(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.CommandContext(t.Context(), "go", args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("go %s failed: %v\n%s", strings.Join(args, " "), err, out)
	}
}

func runConsumer(t *testing.T, bin string, args ...string) (string, string, int) {
	t.Helper()
	return runConsumerWithEnv(t, bin, nil, args...)
}

func runConsumerWithEnv(t *testing.T, bin string, env []string, args ...string) (string, string, int) {
	t.Helper()
	cmd := exec.CommandContext(t.Context(), bin, args...)
	cmd.Env = append(os.Environ(), append([]string{"CONSUMER_NO_UPDATE_CHECK=1"}, env...)...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	code := 0
	if err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			code = ee.ExitCode()
		} else {
			t.Fatalf("run consumer %v: %v", args, err)
		}
	}
	return stdout.String(), stderr.String(), code
}

func repoRoot(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", ".."))
	must(t, err)
	return root
}

func sessionStartCommands(t *testing.T, data []byte) []string {
	t.Helper()
	var doc struct {
		Hooks struct {
			SessionStart []struct {
				Hooks []struct {
					Command string `json:"command"`
				} `json:"hooks"`
			} `json:"SessionStart"`
		} `json:"hooks"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatal(err)
	}
	var cmds []string
	for _, group := range doc.Hooks.SessionStart {
		for _, hook := range group.Hooks {
			cmds = append(cmds, hook.Command)
		}
	}
	return cmds
}

type releaseFixture struct {
	archiveName  string
	archive      []byte
	checksumName string
	checksums    string
}

type fixtureResponse struct {
	Status int
	Body   string
	Body64 string
}

func newReleaseServer(t *testing.T, f releaseFixture) string {
	t.Helper()
	return fixtureEnv(t, map[string]fixtureResponse{
		"/repos/o/tool/releases/latest": {
			Status: 200,
			Body: fmt.Sprintf(`{"tag_name":"v0.2.0","assets":[{"name":%q,"browser_download_url":%q},{"name":%q,"browser_download_url":%q}]}`,
				f.archiveName, "https://fixtures.test/dl/"+f.archiveName, f.checksumName, "https://fixtures.test/dl/"+f.checksumName),
		},
		"/dl/" + f.archiveName:  {Status: 200, Body64: base64.StdEncoding.EncodeToString(f.archive)},
		"/dl/" + f.checksumName: {Status: 200, Body: f.checksums},
	})
}

func checkFixture(t *testing.T) string {
	t.Helper()
	return fixtureEnv(t, map[string]fixtureResponse{
		"/repos/old/tool/releases/latest": {Status: 200, Body: `{"tag_name":"v9.9.9","assets":[]}`},
		"/repos/new/tool/releases/latest": {Status: 500, Body: "offline\n"},
	})
}

func fixtureEnv(t *testing.T, routes map[string]fixtureResponse) string {
	t.Helper()
	b, err := json.Marshal(routes)
	must(t, err)
	return string(b)
}

func archiveForRuntime(t *testing.T, binary string, body []byte) (string, []byte) {
	t.Helper()
	switch runtime.GOOS {
	case "windows":
		name := binary + "_0.2.0_windows_" + runtime.GOARCH + ".zip"
		return name, zipArchive(t, binary+".exe", body)
	default:
		name := binary + "_0.2.0_" + runtime.GOOS + "_" + runtime.GOARCH + ".tar.gz"
		return name, tarGzArchive(t, binary, body)
	}
}

func tarGzArchive(t *testing.T, name string, body []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	must(t, tw.WriteHeader(&tar.Header{Name: name, Mode: 0o755, Size: int64(len(body)), Typeflag: tar.TypeReg}))
	_, err := tw.Write(body)
	must(t, err)
	must(t, tw.Close())
	must(t, gz.Close())
	return buf.Bytes()
}

func zipArchive(t *testing.T, name string, body []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	w, err := zw.Create(name)
	must(t, err)
	_, err = w.Write(body)
	must(t, err)
	must(t, zw.Close())
	return buf.Bytes()
}

func assertFileBytes(t *testing.T, path string, want []byte) {
	t.Helper()
	got, err := os.ReadFile(path)
	must(t, err)
	if !bytes.Equal(got, want) {
		t.Fatalf("%s changed after failed update", path)
	}
}

func sha256Hex(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func exeName(name string) string {
	if runtime.GOOS == "windows" {
		return name + ".exe"
	}
	return name
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

const consumerMain = `package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/lucashalbert/axi-sdk-go/axierr"
	"github.com/lucashalbert/axi-sdk-go/cli"
	"github.com/lucashalbert/axi-sdk-go/setup"
	"github.com/lucashalbert/axi-sdk-go/update"
)

var info = update.Info{Version: "0.1.0", Commit: "abc1234", Date: "2026-11-07T10:00:00Z"}

type module struct{}

func (module) Register(root *cobra.Command) {
	root.AddCommand(&cobra.Command{
		Use:  "fail",
		Args: cobra.NoArgs,
		RunE: func(*cobra.Command, []string) error {
			return axierr.Runtime("consumer fail", "https://user:supersecret@example.test", errors.New("token=s3cr3t password=topsecret"), "rotate token=s3cr3t")
		},
	})
	root.AddCommand(&cobra.Command{
		Use:  "setup-skill SOURCE DEST",
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := setup.InstallSkill(os.DirFS(args[0]), args[1]); err != nil {
				return err
			}
			_, _ = fmt.Fprintln(cmd.OutOrStdout(), "installed")
			return nil
		},
	})
	root.AddCommand(&cobra.Command{
		Use:  "setup-hooks SETTINGS COMMAND...",
		Args: cobra.MinimumNArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			changed, err := setup.InstallHooks(args[0], strings.Join(args[1:], " "))
			if err != nil {
				return err
			}
			if changed {
				_, _ = fmt.Fprintln(cmd.OutOrStdout(), "changed")
			} else {
				_, _ = fmt.Fprintln(cmd.OutOrStdout(), "unchanged")
			}
			return nil
		},
	})
	root.AddCommand(&cobra.Command{
		Use:  "check-cache CACHE",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := releaseClient()
			if err != nil {
				return err
			}
			if _, _, err := update.Check(cmd.Context(), update.CheckConfig{
				Repo: "old/tool", Current: "0.1.0", CacheFile: args[0], TTL: time.Hour, Client: client,
			}); err != nil {
				return err
			}
			if _, _, err := update.Check(cmd.Context(), update.CheckConfig{
				Repo: "new/tool", Current: "0.1.0", CacheFile: args[0], TTL: time.Hour, Client: client,
			}); err == nil {
				return errors.New("new/tool lookup unexpectedly succeeded")
			}
			latest, newer, err := update.Check(cmd.Context(), update.CheckConfig{
				Repo: "new/tool", Current: "0.1.0", CacheFile: args[0], TTL: time.Hour, Client: client,
			})
			if err != nil {
				return err
			}
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "latest=%s newer=%v\n", latest, newer)
			return nil
		},
	})
	root.AddCommand(&cobra.Command{
		Use:  "self-update",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			client, err := releaseClient()
			if err != nil {
				return err
			}
			_, _, err = update.Update(cmd.Context(), update.InstallConfig{
				Repo: "o/tool", Binary: "consumer", Current: info.Version,
				Client: client,
			})
			return err
		},
	})
}

func releaseClient() (*http.Client, error) {
	if raw := os.Getenv("AXI_E2E_RELEASE_FIXTURE"); raw != "" {
		routes := map[string]fixtureResponse{}
		if err := json.Unmarshal([]byte(raw), &routes); err != nil {
			return nil, err
		}
		return &http.Client{Transport: fixtureTransport{routes: routes}}, nil
	}
	base := os.Getenv("AXI_E2E_RELEASE_URL")
	if base == "" {
		return nil, errors.New("AXI_E2E_RELEASE_URL or AXI_E2E_RELEASE_FIXTURE is required")
	}
	u, err := url.Parse(base)
	if err != nil {
		return nil, err
	}
	return &http.Client{Transport: rewriteTransport{target: u}}, nil
}

type fixtureResponse struct {
	Status int
	Body   string
	Body64 string
}

type fixtureTransport struct {
	routes map[string]fixtureResponse
}

func (f fixtureTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	fr, ok := f.routes[req.URL.Path]
	if !ok {
		fr = fixtureResponse{Status: http.StatusNotFound, Body: "not found\n"}
	}
	status := fr.Status
	if status == 0 {
		status = http.StatusOK
	}
	body := []byte(fr.Body)
	if fr.Body64 != "" {
		decoded, err := base64.StdEncoding.DecodeString(fr.Body64)
		if err != nil {
			return nil, err
		}
		body = decoded
	}
	return &http.Response{
		StatusCode: status,
		Status:     fmt.Sprintf("%d %s", status, http.StatusText(status)),
		Body:       io.NopCloser(bytes.NewReader(body)),
		Header:     make(http.Header),
		Request:    req,
	}, nil
}

type rewriteTransport struct {
	target *url.URL
}

func (r rewriteTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	req = req.Clone(context.Background())
	req.URL.Scheme = r.target.Scheme
	req.URL.Host = r.target.Host
	return http.DefaultTransport.RoundTrip(req)
}

func main() {
	os.Exit(cli.Execute(cli.New("consumer", info, cli.Repo("o/tool"), module{})))
}
`
