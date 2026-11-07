// Package redact keeps credentials out of anything an AXI tool prints.
//
// Mask removes secret-shaped text by pattern. Scrub additionally removes
// exact values the caller knows are secret. Register adds values that every
// later Mask call removes, which is how error rendering stays safe without
// each call site threading secrets through.
package redact

import (
	"regexp"
	"sort"
	"strings"
	"sync"
)

// Placeholder replaces every redacted value.
const Placeholder = "[REDACTED]"

var (
	// Whole matches are replaced.
	whole = []*regexp.Regexp{
		regexp.MustCompile(`-----BEGIN [A-Z ]*PRIVATE KEY-----[\s\S]*?-----END [A-Z ]*PRIVATE KEY-----`),
		regexp.MustCompile(`(?i)\bauthorization\s*:\s*(?:[A-Za-z]+\s+)?[^\s,;]+`),
		regexp.MustCompile(`\b[Bb]earer\s+[A-Za-z0-9._~+/=-]{8,}`),
		regexp.MustCompile(`\beyJ[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]*`),
	}
	// Group 1 is kept, the remainder (the value) is replaced.
	keepPrefix = []*regexp.Regexp{
		regexp.MustCompile(`(?i)(\b(?:token|password|passwd|secret|api[_-]?key|access[_-]?key|client[_-]?secret)\s*=\s*)(?:"[^"]*"|'[^']*'|[^\s,;&"']+)`),
		regexp.MustCompile(`(?i)("(?:token|password|passwd|secret|api[_-]?key|access[_-]?key|client[_-]?secret)"\s*:\s*")[^"]*`),
		regexp.MustCompile(`(://[^/\s:@]+:)[^/\s@]+(@)`),
	}

	mu         sync.RWMutex
	registered []string
)

// Mask replaces secret-looking text (bearer tokens, JWTs, token=/password=
// pairs, URL passwords, private keys) and every Register-ed value.
func Mask(s string) string {
	if s == "" {
		return s
	}
	for _, re := range whole {
		s = re.ReplaceAllString(s, Placeholder)
	}
	for _, re := range keepPrefix {
		s = re.ReplaceAllString(s, "${1}"+Placeholder+"${2}")
	}
	mu.RLock()
	defer mu.RUnlock()
	return replaceAll(s, registered)
}

// Scrub is Mask plus replacement of the listed secret values. Empty
// values are ignored.
func Scrub(s string, secrets ...string) string {
	return replaceAll(Mask(s), secrets)
}

// Register adds secret values that all later Mask and Scrub calls remove.
// Empty values are ignored. The returned function removes them again.
func Register(secrets ...string) (unregister func()) {
	add := make([]string, 0, len(secrets))
	for _, v := range secrets {
		if v != "" {
			add = append(add, v)
		}
	}
	mu.Lock()
	registered = append(registered, add...)
	mu.Unlock()
	return func() {
		mu.Lock()
		defer mu.Unlock()
		for _, v := range add {
			for i, r := range registered {
				if r == v {
					registered = append(registered[:i], registered[i+1:]...)
					break
				}
			}
		}
	}
}

// replaceAll replaces longer values first so a secret containing another
// secret is removed whole.
func replaceAll(s string, secrets []string) string {
	vals := make([]string, 0, len(secrets))
	for _, v := range secrets {
		if v != "" {
			vals = append(vals, v)
		}
	}
	if len(vals) == 0 {
		return s
	}
	sort.Slice(vals, func(i, j int) bool { return len(vals[i]) > len(vals[j]) })
	pairs := make([]string, 0, 2*len(vals))
	for _, v := range vals {
		pairs = append(pairs, v, Placeholder)
	}
	return strings.NewReplacer(pairs...).Replace(s)
}
