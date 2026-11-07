package update

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

const archiveName = "tool_0.2.0_linux_amd64.tar.gz"

func installFixture(t *testing.T) (*fakeGitHub, string, InstallConfig) {
	t.Helper()
	files := map[string][]byte{
		archiveName:                     tarGz(t, "tool", []byte("NEW BINARY")),
		"tool_0.2.0_linux_arm64.tar.gz": tarGz(t, "tool", []byte("ARM BINARY")),
		"tool_0.2.0_windows_amd64.zip":  zipOf(t, "tool.exe", []byte("WIN BINARY")),
	}
	f := newFakeGitHub(t, "v0.2.0", files)
	target := filepath.Join(t.TempDir(), "tool")
	must(t, os.WriteFile(target, []byte("OLD BINARY"), 0o755))
	return f, target, InstallConfig{Repo: "o/tool", Binary: "tool", Current: "0.1.0", Client: f.client}
}

func assertUntouched(t *testing.T, target string) {
	t.Helper()
	got, err := os.ReadFile(target)
	must(t, err)
	if string(got) != "OLD BINARY" {
		t.Errorf("target modified: %q", got)
	}
	entries, _ := os.ReadDir(filepath.Dir(target))
	if len(entries) != 1 {
		t.Errorf("leftover files next to target: %v", entries)
	}
}

func TestUpdateReplacesBinary(t *testing.T) {
	f, target, c := installFixture(t)
	_ = f
	latest, updated, err := update(context.Background(), c, target, "linux", "amd64")
	if err != nil || !updated || latest != "0.2.0" {
		t.Fatalf("%q %v %v", latest, updated, err)
	}
	got, _ := os.ReadFile(target)
	if string(got) != "NEW BINARY" {
		t.Errorf("target = %q", got)
	}
	st, _ := os.Stat(target)
	if runtime.GOOS != "windows" && st.Mode().Perm()&0o111 == 0 {
		t.Errorf("not executable: %v", st.Mode())
	}
	if entries, _ := os.ReadDir(filepath.Dir(target)); len(entries) != 1 {
		t.Errorf("temp files left: %v", entries)
	}
}

func TestUpdateSelectsArchiveForPlatform(t *testing.T) {
	_, target, c := installFixture(t)
	if _, _, err := update(context.Background(), c, target, "linux", "arm64"); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(target); string(got) != "ARM BINARY" {
		t.Errorf("target = %q", got)
	}
}

func TestUpdateZipOnWindowsKeepsOldAside(t *testing.T) {
	_, target, c := installFixture(t)
	if _, _, err := update(context.Background(), c, target, "windows", "amd64"); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(target); string(got) != "WIN BINARY" {
		t.Errorf("target = %q", got)
	}
	if got, _ := os.ReadFile(target + ".old"); string(got) != "OLD BINARY" {
		t.Errorf(".old = %q", got)
	}
}

func TestUpdateChecksumMismatchLeavesBinaryUntouched(t *testing.T) {
	f, target, c := installFixture(t)
	f.sums = "0000000000000000000000000000000000000000000000000000000000000000  " + archiveName + "\n"
	_, updated, err := update(context.Background(), c, target, "linux", "amd64")
	if err == nil || updated {
		t.Fatalf("want checksum error, got updated=%v err=%v", updated, err)
	}
	assertUntouched(t, target)
}

func TestUpdateMissingChecksumEntryFailsClosed(t *testing.T) {
	f, target, c := installFixture(t)
	f.sums = sum([]byte("x")) + "  other_file.tar.gz\n"
	if _, _, err := update(context.Background(), c, target, "linux", "amd64"); err == nil {
		t.Fatal("want error")
	}
	assertUntouched(t, target)
}

func TestUpdateNoChecksumsAssetFailsClosed(t *testing.T) {
	_, target, c := installFixture(t)
	// An API response without checksums.txt.
	c.Client = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		return jsonResponse(`{"tag_name":"v0.2.0","assets":[{"name":"` + archiveName + `","browser_download_url":"http://x/a"}]}`), nil
	})}
	if _, _, err := update(context.Background(), c, target, "linux", "amd64"); err == nil {
		t.Fatal("want error")
	}
	assertUntouched(t, target)
}

func TestUpdateRejectsChecksumsAlias(t *testing.T) {
	_, target, c := installFixture(t)
	c.Client = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		return jsonResponse(`{"tag_name":"v0.2.0","assets":[{"name":"` + archiveName + `","browser_download_url":"http://x/a"},{"name":"tool_checksums.txt","browser_download_url":"http://x/s"}]}`), nil
	})}
	if _, _, err := update(context.Background(), c, target, "linux", "amd64"); err == nil {
		t.Fatal("want error")
	}
	assertUntouched(t, target)
}

func TestUpdateCorruptArchiveWithMatchingChecksum(t *testing.T) {
	f, target, c := installFixture(t)
	bad := []byte("this is not a gzip stream")
	f.files[archiveName] = bad
	f.sums = sum(bad) + "  " + archiveName + "\n"
	if _, _, err := update(context.Background(), c, target, "linux", "amd64"); err == nil {
		t.Fatal("want error")
	}
	assertUntouched(t, target)
}

func TestUpdateArchiveWithoutBinary(t *testing.T) {
	f, target, c := installFixture(t)
	f.files[archiveName] = tarGz(t, "something-else", []byte("x"))
	if _, _, err := update(context.Background(), c, target, "linux", "amd64"); err == nil {
		t.Fatal("want error")
	}
	assertUntouched(t, target)
}

func TestUpdateTruncatedDownloadLeavesBinaryUntouched(t *testing.T) {
	f, target, c := installFixture(t)
	full := f.files[archiveName]
	sums := sum(full) + "  " + archiveName + "\n"
	f.sums = sums
	f.files[archiveName] = full[:len(full)/2] // served short; checksum covers the full file
	if _, _, err := update(context.Background(), c, target, "linux", "amd64"); err == nil {
		t.Fatal("want error")
	}
	assertUntouched(t, target)
}

func TestUpdateCancelledLeavesBinaryUntouched(t *testing.T) {
	_, target, c := installFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, _, err := update(ctx, c, target, "linux", "amd64"); err == nil {
		t.Fatal("want error")
	}
	assertUntouched(t, target)
}

func TestUpdateAlreadyCurrent(t *testing.T) {
	_, target, c := installFixture(t)
	for _, cur := range []string{"0.2.0", "v0.2.0", "0.3.0"} {
		c.Current = cur
		latest, updated, err := update(context.Background(), c, target, "linux", "amd64")
		if err != nil || updated || latest != "0.2.0" {
			t.Errorf("current %s: %q %v %v", cur, latest, updated, err)
		}
	}
	assertUntouched(t, target)
}

func TestUpdateDevBuildAlwaysUpdates(t *testing.T) {
	_, target, c := installFixture(t)
	c.Current = "dev"
	if _, updated, err := update(context.Background(), c, target, "linux", "amd64"); err != nil || !updated {
		t.Fatal(updated, err)
	}
}

func TestUpdateAPIFailure(t *testing.T) {
	f, target, c := installFixture(t)
	f.apiCode = 500
	if _, _, err := update(context.Background(), c, target, "linux", "amd64"); err == nil {
		t.Fatal("want error")
	}
	assertUntouched(t, target)
}

func TestPickArchive(t *testing.T) {
	assets := []asset{
		{Name: "tool_0.2.0_Darwin_arm64.tar.gz"}, {Name: "tool_0.2.0_Darwin_x86_64.tar.gz"},
		{Name: "tool_0.2.0_Linux_arm64.tar.gz"}, {Name: "tool_0.2.0_Linux_x86_64.tar.gz"},
		{Name: "tool_0.2.0_Windows_x86_64.zip"}, {Name: "tool_0.2.0_Windows_arm64.zip"},
		{Name: "checksums.txt"}, {Name: "other_0.2.0_linux_amd64.tar.gz"},
	}
	tests := []struct{ goos, goarch, want string }{
		{"darwin", "arm64", "tool_0.2.0_Darwin_arm64.tar.gz"},
		{"darwin", "amd64", "tool_0.2.0_Darwin_x86_64.tar.gz"},
		{"linux", "amd64", "tool_0.2.0_Linux_x86_64.tar.gz"},
		{"linux", "arm64", "tool_0.2.0_Linux_arm64.tar.gz"},
		{"windows", "amd64", "tool_0.2.0_Windows_x86_64.zip"},
		{"windows", "arm64", "tool_0.2.0_Windows_arm64.zip"},
	}
	for _, tt := range tests {
		got, err := pickArchive(assets, "tool", tt.goos, tt.goarch)
		if err != nil || got.Name != tt.want {
			t.Errorf("%s/%s = %q, %v; want %q", tt.goos, tt.goarch, got.Name, err, tt.want)
		}
	}
	if _, err := pickArchive(assets, "tool", "plan9", "amd64"); err == nil {
		t.Error("want error for unsupported platform")
	}
	if _, err := pickArchive([]asset{{Name: "tool_0.2.0_Linux_x86.tar.gz"}}, "tool", "linux", "386"); err == nil {
		t.Error("want error for unsupported architecture")
	}
	// The running platform resolves for lower-case names too.
	if supportedOS[runtime.GOOS] && archAliases[runtime.GOARCH] != nil {
		own := []asset{{Name: "tool_1.0.0_" + runtime.GOOS + "_" + runtime.GOARCH + ".tar.gz"}}
		if _, err := pickArchive(own, "tool", runtime.GOOS, runtime.GOARCH); err != nil {
			t.Error(err)
		}
	}
}

func TestReplaceFileRestoresOldOnFailure(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "tool")
	must(t, os.WriteFile(target, []byte("OLD"), 0o755))
	// newBin does not exist, so the second rename fails and the old file must come back.
	if err := replaceFile(target, filepath.Join(dir, "missing"), true); err == nil {
		t.Fatal("want error")
	}
	if got, _ := os.ReadFile(target); string(got) != "OLD" {
		t.Errorf("target = %q", got)
	}
}
