// Package update reports the running version, checks GitHub Releases for a
// newer one, and replaces the running binary with a checksum-verified
// download.
package update

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"path"
	"strings"

	"golang.org/x/mod/semver"
)

// Info describes the running build. GoReleaser injects it through ldflags.
type Info struct {
	Version, Commit, Date string
}

const (
	apiBase     = "https://api.github.com"
	maxAPIBytes = 4 << 20
)

type release struct {
	TagName string  `json:"tag_name"`
	Assets  []asset `json:"assets"`
}

type asset struct {
	Name string `json:"name"`
	URL  string `json:"browser_download_url"`
}

// fetchLatest returns the latest non-prerelease, non-draft GitHub release.
func fetchLatest(ctx context.Context, client *http.Client, repo, userAgent string) (*release, error) {
	if client == nil {
		client = http.DefaultClient
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, apiBase+"/repos/"+repo+"/releases/latest", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", userAgent)
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, maxAPIBytes))
		if resp.StatusCode == http.StatusForbidden || resp.StatusCode == http.StatusTooManyRequests {
			return nil, fmt.Errorf("github api rate limited or forbidden (%s)", resp.Status)
		}
		return nil, fmt.Errorf("github api: %s", resp.Status)
	}
	var rel release
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxAPIBytes)).Decode(&rel); err != nil {
		return nil, fmt.Errorf("decode release: %w", err)
	}
	if !semver.IsValid(canonical(rel.TagName)) {
		return nil, fmt.Errorf("latest release tag %q is not a semantic version", rel.TagName)
	}
	return &rel, nil
}

// canonical turns "1.2.3" or "v1.2.3" into "v1.2.3".
func canonical(v string) string {
	v = strings.TrimSpace(v)
	if v != "" && v[0] != 'v' {
		return "v" + v
	}
	return v
}

// bare strips the leading "v" from a version.
func bare(v string) string { return strings.TrimPrefix(strings.TrimSpace(v), "v") }

// isNewer reports whether latest is a strictly higher semantic version than
// current. An unparseable current version (such as "dev") is never newer.
func isNewer(latest, current string) bool {
	l, c := canonical(latest), canonical(current)
	return semver.IsValid(l) && semver.IsValid(c) && semver.Compare(l, c) > 0
}

var archAliases = map[string][]string{
	"amd64": {"amd64", "x86_64"},
	"arm64": {"arm64", "aarch64"},
	"386":   {"386", "i386", "x86"},
}

// pickArchive selects the archive for goos/goarch. Names are matched on
// their separator-delimited tokens (GoReleaser's default
// "tool_1.2.3_Linux_x86_64.tar.gz" and "tool_1.2.3_linux_amd64.tar.gz" both
// work) and must start with the binary name.
func pickArchive(assets []asset, binary, goos, goarch string) (asset, error) {
	archs := archAliases[goarch]
	if archs == nil {
		archs = []string{goarch}
	}
	for _, a := range assets {
		name := strings.ToLower(a.Name)
		if !strings.HasSuffix(name, ".tar.gz") && !strings.HasSuffix(name, ".zip") {
			continue
		}
		if !strings.HasPrefix(name, strings.ToLower(binary)+"_") && !strings.HasPrefix(name, strings.ToLower(binary)+"-") {
			continue
		}
		stem := strings.TrimSuffix(strings.TrimSuffix(name, ".zip"), ".tar.gz")
		tokens := map[string]bool{}
		for _, t := range strings.FieldsFunc(stem, func(r rune) bool { return r == '_' || r == '-' }) {
			tokens[t] = true
		}
		if !tokens[goos] {
			continue
		}
		// The architecture is the final component, and may itself contain "_" (x86_64).
		for _, arch := range archs {
			if strings.HasSuffix(stem, "_"+arch) || strings.HasSuffix(stem, "-"+arch) {
				return a, nil
			}
		}
	}
	return asset{}, fmt.Errorf("no release archive for %s/%s", goos, goarch)
}

// pickChecksums finds the checksums asset ("checksums.txt" or "*_checksums.txt").
func pickChecksums(assets []asset) (asset, error) {
	for _, a := range assets {
		n := strings.ToLower(a.Name)
		if n == "checksums.txt" || strings.HasSuffix(n, "_checksums.txt") {
			return a, nil
		}
	}
	return asset{}, errors.New("release has no checksums.txt; refusing to install unverified binary")
}

// checksumFor parses "<sha256>  <file>" lines and returns the digest for name.
func checksumFor(sums []byte, name string) (string, error) {
	for _, line := range strings.Split(string(sums), "\n") {
		f := strings.Fields(line)
		if len(f) == 2 && path.Base(strings.TrimPrefix(f[1], "*")) == name {
			return strings.ToLower(f[0]), nil
		}
	}
	return "", fmt.Errorf("checksums.txt has no entry for %s", name)
}
