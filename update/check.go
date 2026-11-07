package update

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"golang.org/x/mod/semver"
)

// DefaultTimeout bounds the network part of Check when CheckConfig.Timeout is zero.
const DefaultTimeout = 2 * time.Second

// CheckConfig configures Check.
type CheckConfig struct {
	Repo      string        // "owner/name" on GitHub
	Current   string        // running version, with or without a leading "v"
	CacheFile string        // where to remember the last check; empty disables caching
	TTL       time.Duration // how long a cached answer is reused
	Timeout   time.Duration // bound on the network call; zero means DefaultTimeout
	Client    *http.Client  // nil means http.DefaultClient
}

type cacheEntry struct {
	Repo      string    `json:"repo"`
	Latest    string    `json:"latest"`
	CheckedAt time.Time `json:"checked_at"`
}

// Check reports the latest release version (without a leading "v") and
// whether it is newer than c.Current.
//
// A fresh cache entry answers without touching the network. A failed lookup
// (offline, rate limited, timed out) is cached too, so a broken network costs
// at most one Timeout per TTL; the error is still returned that once, and
// callers on the command path are expected to ignore it.
// A Current that is not a semantic version, such as "dev", is never checked.
func Check(ctx context.Context, c CheckConfig) (latest string, newer bool, err error) {
	if !semver.IsValid(canonical(c.Current)) {
		return "", false, nil
	}
	if ent, ok := readCache(c.CacheFile); ok && ent.Repo == c.Repo && time.Since(ent.CheckedAt) < c.TTL {
		return bare(ent.Latest), ent.Latest != "" && isNewer(ent.Latest, c.Current), nil
	}
	timeout := c.Timeout
	if timeout <= 0 {
		timeout = DefaultTimeout
	}
	lctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	rel, err := fetchLatest(lctx, c.Client, c.Repo, "axi-update-check/"+bare(c.Current))
	if err != nil {
		if ctx.Err() == nil {
			prev, _ := readCache(c.CacheFile)
			latest := ""
			if prev.Repo == c.Repo {
				latest = prev.Latest
			}
			writeCache(c.CacheFile, cacheEntry{Repo: c.Repo, Latest: latest, CheckedAt: time.Now()})
		}
		return "", false, err
	}
	writeCache(c.CacheFile, cacheEntry{Repo: c.Repo, Latest: rel.TagName, CheckedAt: time.Now()})
	return bare(rel.TagName), isNewer(rel.TagName, c.Current), nil
}

func readCache(file string) (cacheEntry, bool) {
	var ent cacheEntry
	if file == "" {
		return ent, false
	}
	b, err := os.ReadFile(file) //nolint:gosec // path is the caller-configured cache file
	if err != nil || json.Unmarshal(b, &ent) != nil {
		return cacheEntry{}, false
	}
	return ent, true
}

// writeCache is best effort: a read-only cache dir must never fail a command.
func writeCache(file string, ent cacheEntry) {
	if file == "" {
		return
	}
	b, err := json.Marshal(ent)
	if err != nil {
		return
	}
	dir := filepath.Dir(file)
	if os.MkdirAll(dir, 0o700) != nil {
		return
	}
	tmp, err := os.CreateTemp(dir, ".update-cache-*")
	if err != nil {
		return
	}
	_, werr := tmp.Write(b)
	cerr := tmp.Close()
	if werr != nil || cerr != nil || os.Rename(tmp.Name(), file) != nil {
		_ = os.Remove(tmp.Name())
	}
}
