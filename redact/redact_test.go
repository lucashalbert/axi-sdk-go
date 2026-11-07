package redact_test

import (
	"strings"
	"testing"

	"github.com/lucashalbert/axi-sdk-go/redact"
)

const jwt = "eyJhbGciOiJSUzI1NiJ9.eyJzdWIiOiJhZG1pbiJ9.c2lnbmF0dXJlMTIz"

func TestMask(t *testing.T) {
	tests := []struct{ name, in, want string }{
		{"bearer token", "Authorization: Bearer abc123def456", "[REDACTED]"},
		{"bearer in prose", "got header Bearer abc123def456 from client", "got header [REDACTED] from client"},
		{"jwt alone", "token was " + jwt + " ok", "token was [REDACTED] ok"},
		{"basic auth header", "Authorization: Basic dXNlcjpwYXNz", "[REDACTED]"},
		{"token pair", "login failed token=s3cr3t&user=bob", "login failed token=[REDACTED]&user=bob"},
		{"password pair", "password=hunter2 next", "password=[REDACTED] next"},
		{"case-insensitive key", "API_KEY=abc Password=xyz", "API_KEY=[REDACTED] Password=[REDACTED]"},
		{"quoted pair", `password="two words" x`, `password=[REDACTED] x`},
		{"json pair", `{"token": "abc", "name": "ok"}`, `{"token": "[REDACTED]", "name": "ok"}`},
		{"url userinfo", "dial https://admin:p4ss@example.com:6443/api failed", "dial https://admin:[REDACTED]@example.com:6443/api failed"},
		{"private key", "x\n-----BEGIN RSA PRIVATE KEY-----\nMIIB\nabc\n-----END RSA PRIVATE KEY-----\ny", "x\n[REDACTED]\ny"},
		{"ordinary text", "pod web-1 not ready: connection refused (token expired is a reason)", "pod web-1 not ready: connection refused (token expired is a reason)"},
		{"bearer word alone", "bearer of bad news", "bearer of bad news"},
		{"empty", "", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := redact.Mask(tt.in); got != tt.want {
				t.Errorf("Mask(%q)\n got %q\nwant %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestScrubListedSecrets(t *testing.T) {
	got := redact.Scrub("value abcdef and abc and again abcdef", "abc", "abcdef", "")
	if want := "value [REDACTED] and [REDACTED] and again [REDACTED]"; got != want {
		t.Errorf("got %q want %q", got, want)
	}
}

func TestScrubAlsoMasksPatterns(t *testing.T) {
	got := redact.Scrub("password=zzz and mysecretvalue", "mysecretvalue")
	if want := "password=[REDACTED] and [REDACTED]"; got != want {
		t.Errorf("got %q want %q", got, want)
	}
}

func TestScrubOrdinaryTextUntouched(t *testing.T) {
	in := "deployment api has 3/3 ready"
	if got := redact.Scrub(in, "unrelated"); got != in {
		t.Errorf("got %q", got)
	}
	if got := redact.Scrub(in); got != in {
		t.Errorf("got %q", got)
	}
}

func TestRegisterAppliesToMask(t *testing.T) {
	unregister := redact.Register("hunter2-registered", "")
	got := redact.Mask("pw is hunter2-registered ok")
	if want := "pw is " + redact.Placeholder + " ok"; got != want {
		t.Errorf("got %q want %q", got, want)
	}
	unregister()
	if got := redact.Mask("pw is hunter2-registered ok"); strings.Contains(got, redact.Placeholder) {
		t.Errorf("secret still masked after unregister: %q", got)
	}
}

func TestPlaceholder(t *testing.T) {
	if redact.Placeholder != "[REDACTED]" {
		t.Errorf("Placeholder = %q", redact.Placeholder)
	}
}
