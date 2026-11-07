package axierr_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/lucashalbert/axi-sdk-go/axierr"
	"github.com/lucashalbert/axi-sdk-go/output"
	"github.com/lucashalbert/axi-sdk-go/redact"
)

func TestExitCode(t *testing.T) {
	boom := errors.New("boom")
	tests := []struct {
		name string
		err  error
		want int
	}{
		{"nil", nil, 0},
		{"usage", axierr.Usage("get", "bad flag", ""), 2},
		{"runtime", axierr.Runtime("get", "pod/x", boom, ""), 1},
		{"plain error", boom, 1},
		{"wrapped usage", fmt.Errorf("outer: %w", axierr.Usage("get", "bad", "")), 2},
		{"wrapped runtime", fmt.Errorf("outer: %w", axierr.Runtime("get", "", boom, "")), 1},
		{"context deadline", context.DeadlineExceeded, 1},
		{"format conflict", output.ErrConflictingFormats, 2},
		{"wrapped format conflict", fmt.Errorf("flags: %w", output.ErrConflictingFormats), 2},
		{"zero code defaults to runtime", &axierr.Error{Op: "x"}, 1},
		{"typed nil", (*axierr.Error)(nil), 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := axierr.ExitCode(tt.err); got != tt.want {
				t.Errorf("ExitCode = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestConstructors(t *testing.T) {
	u := axierr.Usage("cluster info", "no current context", "pass --context")
	if u.Code != 2 || u.Op != "cluster info" || u.Msg != "no current context" || u.Hint != "pass --context" || u.Err != nil {
		t.Errorf("Usage = %+v", u)
	}
	cause := errors.New("connection refused")
	r := axierr.Runtime("get", "pod/web", cause, "check the server")
	if r.Code != 1 || r.Target != "pod/web" || r.Msg != "connection refused" || !errors.Is(r, cause) {
		t.Errorf("Runtime = %+v", r)
	}
	if got := axierr.Runtime("get", "", nil, "").Msg; got == "" {
		t.Error("Runtime with nil cause must still have a message")
	}
}

func TestErrorString(t *testing.T) {
	e := axierr.Runtime("get", "pod/web", errors.New("timeout"), "retry")
	if got, want := e.Error(), "get pod/web: timeout"; got != want {
		t.Errorf("got %q want %q", got, want)
	}
	if got, want := axierr.Usage("run", "bad flag", "").Error(), "run: bad flag"; got != want {
		t.Errorf("got %q want %q", got, want)
	}
}

func TestErrorAs(t *testing.T) {
	var target *axierr.Error
	err := fmt.Errorf("wrap: %w", axierr.Usage("a", "b", ""))
	if !errors.As(err, &target) || target.Op != "a" {
		t.Error("errors.As failed")
	}
}

func TestRenderTOON(t *testing.T) {
	var buf bytes.Buffer
	axierr.Runtime("get", "pod/web", errors.New("timeout"), "raise --timeout").Render(&buf, output.TOON)
	want := "error:\n  op: get\n  target: pod/web\n  message: timeout\n  hint: raise --timeout\n  exit: 1\n"
	if buf.String() != want {
		t.Errorf("got:\n%s\nwant:\n%s", buf.String(), want)
	}
}

func TestRenderOmitsEmptyFields(t *testing.T) {
	var buf bytes.Buffer
	axierr.Usage("run", "bad flag", "").Render(&buf, output.JSON)
	if want := `{"error":{"op":"run","message":"bad flag","exit":2}}` + "\n"; buf.String() != want {
		t.Errorf("got %q want %q", buf.String(), want)
	}
}

func TestRenderNeverLeaksSecrets(t *testing.T) {
	const secret = "s3kr1t-value-123" //nolint:gosec // test fixture, not a credential
	defer redact.Register(secret)()

	cause := fmt.Errorf("auth failed for %s with token=abcdef and Bearer abc123def456", secret)
	errs := []*axierr.Error{
		axierr.Runtime("login "+secret, "user/"+secret, cause, "do not use "+secret),
		axierr.Usage("op", secret, secret),
		{Op: secret, Target: secret, Msg: secret, Hint: secret, Err: cause, Code: 1},
	}
	for _, f := range []output.Format{output.TOON, output.JSON, output.YAML} {
		for i, e := range errs {
			var buf bytes.Buffer
			e.Render(&buf, f)
			out := buf.String()
			for _, leaked := range []string{secret, "abcdef", "abc123def456"} {
				if strings.Contains(out, leaked) {
					t.Errorf("format %s error %d leaked %q:\n%s", f, i, leaked, out)
				}
			}
			if strings.Contains(e.Error(), secret) {
				t.Errorf("Error() leaked secret: %q", e.Error())
			}
		}
	}
}

func TestRenderNilIsNoop(t *testing.T) {
	var buf bytes.Buffer
	(*axierr.Error)(nil).Render(&buf, output.TOON)
	if buf.Len() != 0 {
		t.Errorf("wrote %q", buf.String())
	}
}
