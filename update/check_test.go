package update

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func cfg(f *fakeGitHub, dir, current string) CheckConfig {
	return CheckConfig{Repo: "o/tool", Current: current, CacheFile: filepath.Join(dir, "sub", "cache.json"),
		TTL: time.Hour, Timeout: 2 * time.Second, Client: f.client}
}

func TestCheckDetectsNewer(t *testing.T) {
	tests := []struct {
		tag, current string
		newer        bool
	}{
		{"v0.2.0", "0.1.0", true},
		{"v0.2.0", "v0.2.0", false},
		{"v0.1.9", "0.2.0", false},
		{"v1.0.0", "1.0.0-rc.1", true},
		{"v0.10.0", "0.9.0", true}, // numeric, not lexical
	}
	for _, tt := range tests {
		f := newFakeGitHub(t, tt.tag, nil)
		latest, newer, err := Check(context.Background(), cfg(f, t.TempDir(), tt.current))
		if err != nil || newer != tt.newer || latest != bare(tt.tag) {
			t.Errorf("tag %s cur %s: got %q %v %v", tt.tag, tt.current, latest, newer, err)
		}
	}
}

func TestCheckSkipsNonSemverCurrent(t *testing.T) {
	f := newFakeGitHub(t, "v9.9.9", nil)
	for _, cur := range []string{"", "dev", "(devel)"} {
		latest, newer, err := Check(context.Background(), cfg(f, t.TempDir(), cur))
		if latest != "" || newer || err != nil {
			t.Errorf("current %q: %q %v %v", cur, latest, newer, err)
		}
	}
	if f.apiCalls.Load() != 0 {
		t.Error("dev builds must not hit the network")
	}
}

func TestCheckCacheHitSkipsNetwork(t *testing.T) {
	f := newFakeGitHub(t, "v0.2.0", nil)
	c := cfg(f, t.TempDir(), "0.1.0")
	for i := 0; i < 3; i++ {
		latest, newer, err := Check(context.Background(), c)
		if err != nil || !newer || latest != "0.2.0" {
			t.Fatalf("call %d: %q %v %v", i, latest, newer, err)
		}
	}
	if n := f.apiCalls.Load(); n != 1 {
		t.Errorf("api calls = %d, want 1", n)
	}
}

func TestCheckStaleCacheRefetches(t *testing.T) {
	f := newFakeGitHub(t, "v0.3.0", nil)
	c := cfg(f, t.TempDir(), "0.1.0")
	writeCache(c.CacheFile, cacheEntry{Repo: c.Repo, Latest: "v0.2.0", CheckedAt: time.Now().Add(-2 * time.Hour)})
	latest, _, err := Check(context.Background(), c)
	if err != nil || latest != "0.3.0" || f.apiCalls.Load() != 1 {
		t.Errorf("latest=%q err=%v calls=%d", latest, err, f.apiCalls.Load())
	}
}

func TestCheckCorruptCacheIgnored(t *testing.T) {
	f := newFakeGitHub(t, "v0.2.0", nil)
	c := cfg(f, t.TempDir(), "0.1.0")
	must(t, os.MkdirAll(filepath.Dir(c.CacheFile), 0o700))
	must(t, os.WriteFile(c.CacheFile, []byte("{not json"), 0o600))
	if _, newer, err := Check(context.Background(), c); err != nil || !newer {
		t.Errorf("newer=%v err=%v", newer, err)
	}
}

func TestCheckTimeoutReturnsPromptlyAndIsCached(t *testing.T) {
	f := newFakeGitHub(t, "v0.2.0", nil)
	block := make(chan struct{})
	t.Cleanup(func() { close(block) })
	f.srv.Config.Handler = blockingHandler(block)

	c := cfg(f, t.TempDir(), "0.1.0")
	c.Timeout = 100 * time.Millisecond
	start := time.Now()
	_, newer, err := Check(context.Background(), c)
	if err == nil || newer {
		t.Fatalf("want timeout error, got newer=%v err=%v", newer, err)
	}
	if d := time.Since(start); d > 2*time.Second {
		t.Errorf("took %v, timeout not enforced", d)
	}
	// The failure is remembered: no second wait within the TTL, and no error.
	start = time.Now()
	latest, newer, err := Check(context.Background(), c)
	if err != nil || newer || latest != "" || time.Since(start) > 50*time.Millisecond {
		t.Errorf("second call: %q %v %v after %v", latest, newer, err, time.Since(start))
	}
}

func TestCheckRateLimited(t *testing.T) {
	f := newFakeGitHub(t, "v0.2.0", nil)
	f.apiCode = 403
	c := cfg(f, t.TempDir(), "0.1.0")
	_, newer, err := Check(context.Background(), c)
	if err == nil || newer {
		t.Errorf("newer=%v err=%v", newer, err)
	}
}

func TestCheckFailureKeepsPreviousLatest(t *testing.T) {
	f := newFakeGitHub(t, "v0.2.0", nil)
	f.apiCode = 500
	c := cfg(f, t.TempDir(), "0.1.0")
	writeCache(c.CacheFile, cacheEntry{Repo: c.Repo, Latest: "v0.2.0", CheckedAt: time.Now().Add(-2 * time.Hour)})
	_, _, err := Check(context.Background(), c)
	if err == nil {
		t.Fatal("want error")
	}
	latest, newer, err := Check(context.Background(), c)
	if err != nil || !newer || latest != "0.2.0" {
		t.Errorf("after failure: %q %v %v", latest, newer, err)
	}
}

func TestCheckFailureDoesNotReuseDifferentRepoLatest(t *testing.T) {
	f := newFakeGitHub(t, "v0.2.0", nil)
	c := cfg(f, t.TempDir(), "0.1.0")
	c.Repo = "new/tool"
	writeCache(c.CacheFile, cacheEntry{Repo: "old/tool", Latest: "v9.9.9", CheckedAt: time.Now().Add(-2 * time.Hour)})
	if _, _, err := Check(context.Background(), c); err == nil {
		t.Fatal("want error")
	}
	latest, newer, err := Check(context.Background(), c)
	if err != nil || newer || latest != "" {
		t.Errorf("after failure: %q %v %v", latest, newer, err)
	}
}

func TestCheckWithoutCacheFile(t *testing.T) {
	f := newFakeGitHub(t, "v0.2.0", nil)
	c := cfg(f, t.TempDir(), "0.1.0")
	c.CacheFile = ""
	for i := 0; i < 2; i++ {
		if _, newer, err := Check(context.Background(), c); err != nil || !newer {
			t.Fatal(newer, err)
		}
	}
	if f.apiCalls.Load() != 2 {
		t.Errorf("calls = %d", f.apiCalls.Load())
	}
}

func TestCacheFileIsJSON(t *testing.T) {
	f := newFakeGitHub(t, "v0.2.0", nil)
	c := cfg(f, t.TempDir(), "0.1.0")
	_, _, _ = Check(context.Background(), c)
	b, err := os.ReadFile(c.CacheFile)
	must(t, err)
	var ent cacheEntry
	if err := json.Unmarshal(b, &ent); err != nil || ent.Latest != "v0.2.0" {
		t.Errorf("cache = %s (%v)", b, err)
	}
}
