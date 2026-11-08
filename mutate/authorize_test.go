package mutate

import (
	"errors"
	"strings"
	"testing"

	"github.com/lucashalbert/axi-sdk-go/axierr"
)

func destructive() Plan {
	return Plan{Operation: "delete", Target: "deployment/web", Destructive: true}
}

func TestAuthorizeDestructive(t *testing.T) {
	cases := []struct {
		name    string
		confirm string
		mode    Mode
		wantErr string // substring of the message, empty means allowed
	}{
		{"exact token", "deployment/web", Apply, ""},
		{"missing token", "", Apply, "requires confirmation"},
		{"mismatched token", "deployment/api", Apply, "does not match"},
		{"case differs", "Deployment/web", Apply, "does not match"},
		{"whitespace differs", " deployment/web", Apply, "does not match"},
		{"dry run needs no token", "", DryRun, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := Authorizer{Confirm: tc.confirm}.Authorize(destructive(), tc.mode)
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("Authorize = %v, want nil", err)
				}
				return
			}
			var ae *axierr.Error
			if !errors.As(err, &ae) {
				t.Fatalf("Authorize = %v, want *axierr.Error", err)
			}
			if axierr.ExitCode(err) != axierr.CodeUsage {
				t.Fatalf("exit code = %d, want %d", axierr.ExitCode(err), axierr.CodeUsage)
			}
			if !strings.Contains(ae.Msg, tc.wantErr) {
				t.Fatalf("msg = %q, want substring %q", ae.Msg, tc.wantErr)
			}
			if !strings.Contains(ae.Hint, "--confirm deployment/web") {
				t.Fatalf("hint = %q, want exact token and flag", ae.Hint)
			}
			if ae.Op != "delete" || ae.Target != "deployment/web" {
				t.Fatalf("op/target = %q/%q", ae.Op, ae.Target)
			}
		})
	}
}

func TestAuthorizeCustomFlag(t *testing.T) {
	err := Authorizer{Flag: "--yes-delete"}.Authorize(destructive(), Apply)
	var ae *axierr.Error
	if !errors.As(err, &ae) || !strings.Contains(ae.Hint, "--yes-delete deployment/web") {
		t.Fatalf("hint = %v", err)
	}
}

func TestAuthorizeNonDestructiveNeedsNoToken(t *testing.T) {
	p := Plan{Operation: "scale", Target: "deployment/web"}
	if err := (Authorizer{}).Authorize(p, Apply); err != nil {
		t.Fatalf("Authorize = %v, want nil", err)
	}
}

func TestAuthorizeRejectsInvalidPlan(t *testing.T) {
	for _, p := range []Plan{
		{Target: "x"},
		{Operation: "scale"},
		{Operation: "delete", Destructive: true},
	} {
		err := Authorizer{Confirm: "x"}.Authorize(p, DryRun)
		if err == nil || axierr.ExitCode(err) != axierr.CodeUsage {
			t.Fatalf("Authorize(%+v) = %v, want usage error", p, err)
		}
	}
}

func TestAuthorizeMasksSecretsInError(t *testing.T) {
	p := Plan{Operation: "delete", Target: "secret/token=hunter2hunter2", Destructive: true}
	err := Authorizer{Confirm: "nope"}.Authorize(p, Apply)
	if err == nil || strings.Contains(err.Error(), "hunter2") {
		t.Fatalf("error leaks secret: %v", err)
	}
}

func TestModeString(t *testing.T) {
	if Apply.String() != "apply" || DryRun.String() != "dry-run" {
		t.Fatalf("got %q, %q", Apply, DryRun)
	}
}
