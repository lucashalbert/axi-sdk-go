package mutate

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/lucashalbert/axi-sdk-go/axierr"
	"github.com/lucashalbert/axi-sdk-go/output"
)

type calls struct{ validate, apply int }

func (c *calls) opts(p Plan, a Authorizer, m Mode, out *bytes.Buffer) Options {
	return Options{
		Plan: p, Auth: a, Mode: m, Format: output.JSON, Out: out,
		Validate: func(context.Context) error { c.validate++; return nil },
		Apply:    func(context.Context) error { c.apply++; return nil },
	}
}

func scale() Plan {
	return Plan{
		Operation: "scale", Target: "deployment/web",
		Scope:   map[string]string{"context": "prod", "namespace": "default"},
		Changes: []string{"replicas 2 -> 3"},
	}
}

type doc struct {
	Plan struct {
		Operation   string            `json:"operation"`
		Target      string            `json:"target"`
		Scope       map[string]string `json:"scope"`
		Changes     []string          `json:"changes"`
		Destructive bool              `json:"destructive"`
		Mode        string            `json:"mode"`
		NoOp        bool              `json:"noop"`
		Confirm     string            `json:"confirm"`
	} `json:"plan"`
}

func parse(t *testing.T, out *bytes.Buffer) doc {
	t.Helper()
	var d doc
	if err := json.Unmarshal(out.Bytes(), &d); err != nil {
		t.Fatalf("output is not one JSON document: %v\n%s", err, out.String())
	}
	return d
}

func TestRunApplyPrintsPlanBeforeApplying(t *testing.T) {
	var out bytes.Buffer
	c := &calls{}
	o := c.opts(scale(), Authorizer{}, Apply, &out)
	o.Apply = func(context.Context) error {
		if !strings.Contains(out.String(), "deployment/web") {
			t.Error("plan target was not printed before apply ran")
		}
		c.apply++
		return nil
	}
	if err := Run(t.Context(), o); err != nil {
		t.Fatal(err)
	}
	d := parse(t, &out)
	if c.apply != 1 || c.validate != 0 {
		t.Fatalf("calls = %+v, want one apply", c)
	}
	if d.Plan.Mode != "apply" || d.Plan.Scope["context"] != "prod" || d.Plan.Changes[0] != "replicas 2 -> 3" {
		t.Fatalf("plan = %+v", d.Plan)
	}
}

func TestRunDryRunNeverApplies(t *testing.T) {
	var out bytes.Buffer
	c := &calls{}
	if err := Run(t.Context(), c.opts(scale(), Authorizer{}, DryRun, &out)); err != nil {
		t.Fatal(err)
	}
	if c.apply != 0 || c.validate != 1 {
		t.Fatalf("calls = %+v, want validate only", c)
	}
	if d := parse(t, &out); d.Plan.Mode != "dry-run" {
		t.Fatalf("mode = %q", d.Plan.Mode)
	}
}

func TestRunDryRunWithoutValidate(t *testing.T) {
	var out bytes.Buffer
	c := &calls{}
	o := c.opts(scale(), Authorizer{}, DryRun, &out)
	o.Validate = nil
	if err := Run(t.Context(), o); err != nil || c.apply != 0 {
		t.Fatalf("err=%v calls=%+v", err, c)
	}
}

func TestRunDryRunValidationFailureIsRuntimeError(t *testing.T) {
	var out bytes.Buffer
	c := &calls{}
	o := c.opts(scale(), Authorizer{}, DryRun, &out)
	o.Validate = func(context.Context) error { return errors.New("admission webhook denied") }
	err := Run(t.Context(), o)
	if axierr.ExitCode(err) != axierr.CodeRuntime || !strings.Contains(err.Error(), "admission webhook denied") {
		t.Fatalf("err = %v", err)
	}
	if !strings.Contains(err.Error(), "scale deployment/web") {
		t.Fatalf("error should name op and target: %v", err)
	}
}

func TestRunDestructiveWithoutTokenChangesNothing(t *testing.T) {
	for _, confirm := range []string{"", "deployment/api"} {
		var out bytes.Buffer
		c := &calls{}
		err := Run(t.Context(), c.opts(destructive(), Authorizer{Confirm: confirm}, Apply, &out))
		if axierr.ExitCode(err) != axierr.CodeUsage {
			t.Fatalf("confirm %q: err = %v, want usage error", confirm, err)
		}
		if c.apply != 0 || c.validate != 0 || out.Len() != 0 {
			t.Fatalf("confirm %q: calls=%+v out=%q", confirm, c, out.String())
		}
	}
}

func TestRunDestructiveWithTokenApplies(t *testing.T) {
	var out bytes.Buffer
	c := &calls{}
	p := destructive()
	p.Changes = []string{"delete deployment/web"}
	if err := Run(t.Context(), c.opts(p, Authorizer{Confirm: "deployment/web"}, Apply, &out)); err != nil {
		t.Fatal(err)
	}
	if c.apply != 1 {
		t.Fatalf("calls = %+v", c)
	}
	if d := parse(t, &out); !d.Plan.Destructive || d.Plan.Confirm != "deployment/web" {
		t.Fatalf("plan = %+v", d.Plan)
	}
}

func TestRunDestructiveDryRunShowsToken(t *testing.T) {
	var out bytes.Buffer
	c := &calls{}
	p := destructive()
	p.Changes = []string{"delete deployment/web"}
	if err := Run(t.Context(), c.opts(p, Authorizer{}, DryRun, &out)); err != nil {
		t.Fatal(err)
	}
	if d := parse(t, &out); d.Plan.Confirm != "deployment/web" || c.apply != 0 {
		t.Fatalf("plan = %+v calls=%+v", d.Plan, c)
	}
}

func TestRunNoChangesIsNoOp(t *testing.T) {
	for _, m := range []Mode{Apply, DryRun} {
		var out bytes.Buffer
		c := &calls{}
		p := scale()
		p.Changes = nil
		if err := Run(t.Context(), c.opts(p, Authorizer{}, m, &out)); err != nil {
			t.Fatal(err)
		}
		d := parse(t, &out)
		if !d.Plan.NoOp || c.apply != 0 || c.validate != 0 || d.Plan.Changes == nil {
			t.Fatalf("mode %v: plan=%+v calls=%+v", m, d.Plan, c)
		}
	}
}

func TestRunApplyFailureKeepsAxiError(t *testing.T) {
	var out bytes.Buffer
	c := &calls{}
	o := c.opts(scale(), Authorizer{}, Apply, &out)
	want := axierr.Usage("scale", "replicas must be >= 0", "pass a non-negative count")
	o.Apply = func(context.Context) error { return want }
	if err := Run(t.Context(), o); !errors.Is(err, want) {
		t.Fatalf("err = %v, want passthrough", err)
	}
}

func TestRunMissingApplyIsRuntimeError(t *testing.T) {
	var out bytes.Buffer
	o := Options{Plan: scale(), Out: &out}
	if err := Run(t.Context(), o); axierr.ExitCode(err) != axierr.CodeRuntime {
		t.Fatalf("err = %v", err)
	}
	if out.Len() != 0 {
		t.Fatalf("nothing should print: %q", out.String())
	}
}

func TestRunMasksSecretsInPlanAndErrors(t *testing.T) {
	var out bytes.Buffer
	c := &calls{}
	p := scale()
	p.Changes = []string{"set password=hunter2hunter2"}
	o := c.opts(p, Authorizer{}, Apply, &out)
	o.Apply = func(context.Context) error { return errors.New("denied: token=sekrit12345") }
	err := Run(t.Context(), o)
	if strings.Contains(out.String(), "hunter2") || !strings.Contains(out.String(), "[REDACTED]") {
		t.Fatalf("plan output not masked: %s", out.String())
	}
	if err == nil || strings.Contains(err.Error(), "sekrit") {
		t.Fatalf("error not masked: %v", err)
	}
}

func TestRunFormats(t *testing.T) {
	for _, f := range []output.Format{output.TOON, output.JSON, output.YAML} {
		var out bytes.Buffer
		c := &calls{}
		o := c.opts(scale(), Authorizer{}, DryRun, &out)
		o.Format = f
		if err := Run(t.Context(), o); err != nil {
			t.Fatalf("%v: %v", f, err)
		}
		if !strings.Contains(out.String(), "deployment/web") || !strings.Contains(out.String(), "replicas 2 -> 3") {
			t.Fatalf("%v output: %s", f, out.String())
		}
	}
}

func TestRunContextCancelledBeforeApply(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	var out bytes.Buffer
	c := &calls{}
	err := Run(ctx, c.opts(scale(), Authorizer{}, Apply, &out))
	if !errors.Is(err, context.Canceled) || c.apply != 0 {
		t.Fatalf("err=%v calls=%+v", err, c)
	}
}
